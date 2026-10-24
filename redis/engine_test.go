package redis_test

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/acneism/lua/redis"
)

type testHost struct {
	lookup func([][]byte) (redis.Command, bool)
	check  func([][]byte) *redis.Reply
	call   func([][]byte, int) redis.Reply
	log    func(int, string)
}

func (h *testHost) Lookup(argv [][]byte) (redis.Command, bool) {
	if h.lookup != nil {
		return h.lookup(argv)
	}
	return redis.Command{Name: string(argv[0]), Arity: -1}, true
}
func (h *testHost) Check(argv [][]byte) *redis.Reply {
	if h.check != nil {
		return h.check(argv)
	}
	return nil
}
func (h *testHost) Call(argv [][]byte, proto int) redis.Reply {
	if h.call != nil {
		return h.call(argv, proto)
	}
	return redis.Status("OK")
}
func (h *testHost) Log(level int, message string) {
	if h.log != nil {
		h.log(level, message)
	}
}

func TestScriptCache(t *testing.T) {
	e := redis.NewEngine(redis.Config{EvalCacheMax: 2})
	pinned := e.ScriptLoad([]byte("return 71"))
	if pinned.Type != redis.BulkReply || len(pinned.Str) != 40 {
		t.Fatal(pinned)
	}
	for i := range 5 {
		if reply := e.Eval([]byte(fmt.Sprintf("return %d", i)), nil, nil, redis.Run{}); reply.Int != int64(i) {
			t.Fatal(reply)
		}
	}
	if reply := e.EvalSHA(strings.ToUpper(pinned.Str), nil, nil, redis.Run{}); reply.Int != 71 {
		t.Fatal(reply)
	}
	if reply := e.ScriptExists(pinned.Str, "missing"); len(reply.Elements) != 2 || reply.Elements[0].Int != 1 || reply.Elements[1].Int != 0 {
		t.Fatal(reply)
	}
	a := e.ScriptLoad([]byte("return 72"))
	e.ScriptFlush()
	if reply := e.ScriptExists(pinned.Str, a.Str); reply.Elements[0].Int != 0 || reply.Elements[1].Int != 0 {
		t.Fatal(reply)
	}
	if reply := e.EvalSHA(pinned.Str, nil, nil, redis.Run{}); reply.Type != redis.ErrorReply {
		t.Fatal(reply)
	}
}

func TestEvalCacheLRU(t *testing.T) {
	e := redis.NewEngine(redis.Config{EvalCacheMax: 2})
	first := []byte("return 1")
	second := []byte("return 2")
	third := []byte("return 3")
	sha := func(source []byte) string {
		temporary := redis.NewEngine(redis.Config{})
		return temporary.ScriptLoad(source).Str
	}
	e.Eval(first, nil, nil, redis.Run{})
	e.Eval(second, nil, nil, redis.Run{})
	e.EvalSHA(sha(first), nil, nil, redis.Run{})
	e.Eval(third, nil, nil, redis.Run{})
	entries := e.ScriptExists(sha(first), sha(second), sha(third)).Elements
	if entries[0].Int != 1 || entries[1].Int != 0 || entries[2].Int != 1 {
		t.Fatal(entries)
	}
	e.ScriptLoad(third)
	for i := range 10 {
		e.Eval([]byte(fmt.Sprintf("return %d+100", i)), nil, nil, redis.Run{})
	}
	if e.ScriptExists(sha(third)).Elements[0].Int != 1 {
		t.Fatal("ScriptLoad did not pin an EVAL entry")
	}
}

func TestHostAbortEscapesProtectedCalls(t *testing.T) {
	cause := errors.New("restart transaction with more keys")
	original := redis.Abort(cause)
	original.Str = "preserve opaque reply"
	original.Int = 91
	host := &testHost{call: func(_ [][]byte, _ int) redis.Reply { return original }}
	for _, source := range []string{`return redis.call('GET','x')`, `pcall(function() redis.call('GET','x') end) return 'caught'`, `xpcall(function() redis.call('GET','x') end,function() return 'caught' end) return 'caught'`, `local c=coroutine.create(function() return redis.call('GET','x') end) coroutine.resume(c) return 'caught'`, `return redis.pcall('GET','x')`} {
		e := redis.NewEngine(redis.Config{})
		reply := e.Eval([]byte(source), nil, nil, redis.Run{Host: host})
		if reply.Type != original.Type || reply.Err != cause || reply.Str != original.Str || reply.Int != original.Int {
			t.Fatalf("abort changed or caught: %s -> %+v", source, reply)
		}
		if result := e.Eval([]byte("return 73"), nil, nil, redis.Run{}); result.Int != 73 {
			t.Fatal(result)
		}
	}
}

func TestHostCheckBeforeReadOnly(t *testing.T) {
	calls := 0
	host := &testHost{lookup: func(_ [][]byte) (redis.Command, bool) { return redis.Command{Name: "SET", Arity: 3, Write: true}, true }, check: func(_ [][]byte) *redis.Reply { reply := redis.Error("DENIED from host check"); return &reply }, call: func(_ [][]byte, _ int) redis.Reply { calls++; return redis.Status("OK") }}
	reply := redis.NewEngine(redis.Config{}).Eval([]byte("return redis.call('SET','k','v')"), nil, nil, redis.Run{Host: host, ReadOnly: true})
	if !strings.HasPrefix(reply.Str, "DENIED from host check script:") || calls != 0 {
		t.Fatal(reply, calls)
	}
	host.check = func(_ [][]byte) *redis.Reply { reply := redis.Abort(errors.New("check abort")); return &reply }
	reply = redis.NewEngine(redis.Config{}).Eval([]byte("return redis.pcall('SET','k','v')"), nil, nil, redis.Run{Host: host})
	if reply.Type != redis.AbortReply || calls != 0 {
		t.Fatal(reply, calls)
	}
}

func TestPoolIsolationAndConcurrentRuns(t *testing.T) {
	e := redis.NewEngine(redis.Config{})
	const count = 8
	entered := make(chan struct{}, count)
	release := make(chan struct{})
	host := &testHost{call: func(argv [][]byte, _ int) redis.Reply {
		entered <- struct{}{}
		<-release
		return redis.Bulk(string(argv[1]))
	}}
	var group sync.WaitGroup
	for i := range count {
		group.Add(1)
		go func() {
			defer group.Done()
			value := fmt.Sprint(i)
			reply := e.Eval([]byte("return redis.call('ECHO',ARGV[1])"), nil, [][]byte{[]byte(value)}, redis.Run{Host: host})
			if reply.Str != value {
				t.Errorf("concurrent state changed: %+v", reply)
			}
		}()
	}
	for range count {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			close(release)
			t.Fatal("runs serialized")
		}
	}
	running := e.Running()
	if len(running) != count {
		t.Error(len(running))
	}
	if len(running) > 0 && len(running[0].Command) > 0 {
		running[0].Command[0][0] = 'x'
	}
	close(release)
	group.Wait()
	if len(e.Running()) != 0 {
		t.Fatal("run retained")
	}
	for i := range 2 {
		reply := e.Eval([]byte("KEYS[1]='changed' ARGV[2]='leak' redis.setresp(3) redis.set_repl(0) return ARGV[1]"), [][]byte{[]byte("key")}, [][]byte{[]byte(fmt.Sprint(i))}, redis.Run{})
		if reply.Str != fmt.Sprint(i) {
			t.Fatal(reply)
		}
	}
	reply := e.Eval([]byte("return {#KEYS,#ARGV,false}"), nil, nil, redis.Run{})
	if len(reply.Elements) != 3 || reply.Elements[0].Int != 0 || reply.Elements[1].Int != 0 || reply.Elements[2].Type != redis.NullReply {
		t.Fatal(reply)
	}
}

func TestKillAndWriterPolicy(t *testing.T) {
	for _, allow := range []bool{false, true} {
		e := redis.NewEngine(redis.Config{HookEvery: 1, KillWriters: allow})
		started := make(chan struct{})
		release := make(chan struct{})
		finished := make(chan redis.Reply, 1)
		cause := errors.New("writer stopped by host")
		host := &testHost{lookup: func(_ [][]byte) (redis.Command, bool) { return redis.Command{Name: "SET", Arity: 3, Write: true}, true }, call: func(_ [][]byte, _ int) redis.Reply {
			close(started)
			<-release
			if !allow {
				return redis.Abort(cause)
			}
			return redis.Status("OK")
		}}
		go func() {
			finished <- e.Eval([]byte("redis.call('SET','k','v') while true do end"), nil, nil, redis.Run{Host: host})
		}()
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("host not reached")
		}
		kill := e.Kill(redis.Script)
		if !allow {
			if !strings.HasPrefix(kill.Str, "UNKILLABLE ") {
				t.Error(kill)
			}
		} else if kill.Type != redis.StatusReply {
			t.Error(kill)
		}
		close(release)
		select {
		case reply := <-finished:
			if allow && !strings.Contains(reply.Str, "Script killed by user with SCRIPT KILL") || !allow && reply.Err != cause {
				t.Error(reply)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("kill did not interrupt")
		}
		if e.Kill(redis.Function).Type != redis.ErrorReply {
			t.Fatal("inactive kind killed")
		}
	}
}

func TestMemoryBudgetDoesNotAccumulateAcrossRuns(t *testing.T) {
	e := redis.NewEngine(redis.Config{MemoryLimit: 1024})
	for range 10 {
		if reply := e.Eval([]byte("return {}"), nil, nil, redis.Run{}); reply.Type != redis.ArrayReply {
			t.Fatal(reply)
		}
	}
	if reply := e.Eval([]byte("local t={} for i=1,10000 do t[i]={i} end return t"), nil, nil, redis.Run{}); reply.Type != redis.AbortReply || reply.Err == nil {
		t.Fatal(reply)
	}
}
