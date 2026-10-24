package redis

import (
	"container/list"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/acneism/lua"
)

type Config struct {
	Version      string
	VersionNum   int
	EvalCacheMax int
	HookEvery    int
	MemoryLimit  int64
	KillWriters  bool
	Math         *lua.Math
}

type Run struct {
	Host     Host
	ReadOnly bool
	Proto    int
}
type Command struct {
	Name            string
	Arity           int
	Write, NoScript bool
}
type Host interface {
	Lookup(argv [][]byte) (Command, bool)
	Check(argv [][]byte) *Reply
	Call(argv [][]byte, proto int) Reply
	Log(level int, msg string)
}

type Kind uint8

const (
	Script Kind = iota
	Function
)

type RunInfo struct {
	Kind        Kind
	SHA         string
	Name        string
	Command     [][]byte
	Started     time.Time
	ReadOnly    bool
	Replication int
}

type compiledScript struct {
	sha   string
	proto *lua.Proto
	flags Flags
	lru   *list.Element
}
type execution struct {
	run             Run
	info            RunInfo
	killed, written atomic.Bool
	abort           *Reply
	resp            int
}
type Engine struct {
	config  Config
	mu      sync.Mutex
	scripts map[string]*compiledScript
	lru     list.List
	free    []*session
	running map[*execution]struct{}
}

func NewEngine(config Config) *Engine {
	if config.Version == "" {
		config.Version = "7.2.16"
	}
	if config.VersionNum == 0 {
		config.VersionNum = 0x070210
	}
	if config.EvalCacheMax == 0 {
		config.EvalCacheMax = 500
	}
	return &Engine{config: config, scripts: make(map[string]*compiledScript), running: make(map[*execution]struct{})}
}

func scriptSHA(script []byte) string {
	digest := sha1.Sum(script)
	return hex.EncodeToString(digest[:])
}

func (e *Engine) compile(script []byte, pin bool) (*compiledScript, *Reply) {
	sha := scriptSHA(script)
	e.mu.Lock()
	defer e.mu.Unlock()
	if cached := e.scripts[sha]; cached != nil {
		if cached.lru != nil {
			if pin {
				e.lru.Remove(cached.lru)
				cached.lru = nil
			} else {
				e.lru.MoveToFront(cached.lru)
			}
		}
		return cached, nil
	}
	flags, offset, failure := parseFlags(script)
	if failure != nil {
		return nil, failure
	}
	proto, err := lua.Compile(script[offset:], "@user_script")
	if err != nil {
		reply := Error("ERR Error compiling script (new function): " + err.Error())
		return nil, &reply
	}
	entry := &compiledScript{sha: sha, proto: proto, flags: flags}
	e.scripts[sha] = entry
	if !pin {
		entry.lru = e.lru.PushFront(entry)
		for e.config.EvalCacheMax > 0 && e.lru.Len() > e.config.EvalCacheMax {
			last := e.lru.Back()
			delete(e.scripts, last.Value.(*compiledScript).sha)
			e.lru.Remove(last)
		}
	}
	return entry, nil
}

func (e *Engine) ScriptLoad(script []byte) Reply {
	entry, failure := e.compile(script, true)
	if failure != nil {
		return *failure
	}
	return Bulk(entry.sha)
}
func (e *Engine) ScriptFlags(script []byte) (Flags, *Reply) {
	flags, _, failure := parseFlags(script)
	return flags, failure
}
func (e *Engine) ScriptExists(shas ...string) Reply {
	e.mu.Lock()
	defer e.mu.Unlock()
	result := Array()
	for _, sha := range shas {
		value := int64(0)
		if e.scripts[strings.ToLower(sha)] != nil {
			value = 1
		}
		result.Elements = append(result.Elements, Integer(value))
	}
	return result
}
func (e *Engine) ScriptFlush() { e.mu.Lock(); defer e.mu.Unlock(); clear(e.scripts); e.lru.Init() }
func (e *Engine) Eval(script []byte, keys, args [][]byte, run Run) Reply {
	entry, failure := e.compile(script, false)
	if failure != nil {
		return *failure
	}
	return e.execute(entry, keys, args, run)
}
func (e *Engine) EvalSHA(sha string, keys, args [][]byte, run Run) Reply {
	e.mu.Lock()
	entry := e.scripts[strings.ToLower(sha)]
	if entry != nil && entry.lru != nil {
		e.lru.MoveToFront(entry.lru)
	}
	e.mu.Unlock()
	if entry == nil {
		return Error("NOSCRIPT No matching script. Please use EVAL.")
	}
	return e.execute(entry, keys, args, run)
}

func (e *Engine) acquire() *session {
	e.mu.Lock()
	if size := len(e.free); size > 0 {
		s := e.free[size-1]
		e.free = e.free[:size-1]
		e.mu.Unlock()
		return s
	}
	e.mu.Unlock()
	return newSession(e)
}

func (e *Engine) execute(entry *compiledScript, keys, args [][]byte, run Run) Reply {
	if run.Proto != 3 {
		run.Proto = 2
	}
	run.ReadOnly = run.ReadOnly || entry.flags&NoWrites != 0
	x := &execution{run: run, resp: 2, info: RunInfo{Kind: Script, SHA: entry.sha, Started: time.Now(), ReadOnly: run.ReadOnly, Replication: 3}}
	s := e.acquire()
	s.active = x
	e.mu.Lock()
	e.running[x] = struct{}{}
	e.mu.Unlock()
	defer func() {
		s.active = nil
		e.mu.Lock()
		delete(e.running, x)
		if e.config.MemoryLimit <= 0 {
			e.free = append(e.free, s)
		}
		e.mu.Unlock()
	}()
	s.arguments(keys, args)
	values, err := s.state.Call(s.state.Load(entry.proto, nil))
	if x.abort != nil {
		return *x.abort
	}
	if err != nil {
		var failure *lua.Error
		if errors.As(err, &failure) {
			if x.killed.Load() {
				return scriptError("ERR Script killed by user with SCRIPT KILL...", entry.sha, failure.Line)
			}
			if failure.Abort != nil {
				return Abort(failure.Abort)
			}
			message := "ERR " + failure.Error()
			if table := failure.Value.Table(); table != nil {
				if field := table.RawGet(lua.String("err")); field.Type() == lua.TypeString {
					message = field.Text()
				}
			}
			return scriptError(message, entry.sha, failure.Line)
		}
		return Error("ERR " + err.Error())
	}
	if len(values) == 0 {
		return Null()
	}
	return s.fromLua(values[0], 0)
}

func scriptError(message, sha string, line int) Reply {
	return Error(fmt.Sprintf("%s script: %s, on @user_script:%d.", message, sha, max(1, line)))
}

func cloneArguments(argv [][]byte) [][]byte {
	result := make([][]byte, len(argv))
	for i, arg := range argv {
		result[i] = append([]byte(nil), arg...)
	}
	return result
}

func (e *Engine) Running() []RunInfo {
	e.mu.Lock()
	defer e.mu.Unlock()
	result := make([]RunInfo, 0, len(e.running))
	for x := range e.running {
		info := x.info
		info.Command = cloneArguments(info.Command)
		result = append(result, info)
	}
	return result
}

func (e *Engine) Kill(kind Kind) Reply {
	e.mu.Lock()
	defer e.mu.Unlock()
	found := false
	for x := range e.running {
		if x.info.Kind == kind {
			found = true
			if x.written.Load() && !e.config.KillWriters {
				return Error("UNKILLABLE Sorry the script already executed write commands against the dataset. You can either wait the script termination or kill the server in a hard way using the SHUTDOWN NOSAVE command.")
			}
		}
	}
	if !found {
		return Error("NOTBUSY No scripts in execution right now.")
	}
	for x := range e.running {
		if x.info.Kind == kind {
			x.killed.Store(true)
		}
	}
	return Status("OK")
}

func ParseNumKeys(text string, arguments [][]byte) ([][]byte, [][]byte, *Reply) {
	n, ok := parseKeyCount(text)
	if !ok {
		failure := Error("ERR value is not an integer or out of range")
		return nil, nil, &failure
	}
	if n < 0 {
		failure := Error("ERR Number of keys can't be negative")
		return nil, nil, &failure
	}
	if n > int64(len(arguments)) {
		failure := Error("ERR Number of keys can't be greater than number of args")
		return nil, nil, &failure
	}
	return arguments[:int(n)], arguments[int(n):], nil
}
