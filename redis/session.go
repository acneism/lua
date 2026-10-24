package redis

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/acneism/lua"
)

type session struct {
	engine *Engine
	state  *lua.State
	active *execution
	raise  lua.Value
}

const (
	luaMaxStack        = 8000
	luaReplyBaseSlots  = 2
	luaReplySpareSlots = 4
	luaReplyStackLimit = luaMaxStack - luaReplyBaseSlots - luaReplySpareSlots + 1
)

func newSession(engine *Engine) *session {
	s := &session{engine: engine}
	s.state = lua.NewState(lua.Options{Math: engine.config.Math, HookEvery: engine.config.HookEvery, Memory: lua.NewBudget(engine.config.MemoryLimit), Hook: func() error {
		if s.active != nil && s.active.killed.Load() {
			return errors.New("script killed")
		}
		return nil
	}})
	globals := s.state.Globals()
	s.raise = globals.RawGet(lua.String("error"))
	meta := s.state.NewTable(0, 1)
	_ = meta.RawSet(lua.String("__index"), s.state.Function("__index", func(state *lua.State, args []lua.Value) ([]lua.Value, error) {
		name := "nil"
		if len(args) > 1 {
			name = args[1].String()
		}
		_, err := state.Call(s.raise, lua.String("Script attempted to access nonexistent global variable '"+name+"'"), lua.Number(2))
		if failure, ok := err.(*lua.Error); ok {
			return nil, &lua.Error{Value: failure.Value, Message: failure.Message, Line: failure.Line, Abort: failure.Abort, Internal: failure.Internal}
		}
		return nil, err
	}))
	meta.SetReadOnly(true)
	_ = globals.SetMetatable(meta)
	for _, name := range []string{"bit", "cjson", "cmsgpack", "struct"} {
		_ = globals.RawSet(lua.String(name), s.state.NewTable(0, 0).Value())
	}
	_ = globals.RawSet(lua.String("__redis__err__handler"), s.state.Function("__redis__err__handler", func(_ *lua.State, args []lua.Value) ([]lua.Value, error) {
		if len(args) == 0 {
			return []lua.Value{lua.Nil()}, nil
		}
		return args[:1], nil
	}))
	s.openRedis()
	for key, value, err := globals.Next(lua.Nil()); !key.IsNil() && err == nil; key, value, err = globals.Next(key) {
		if table := value.Table(); table != nil && table != globals {
			table.SetReadOnly(true)
		}
	}
	globals.SetReadOnly(true)
	return s
}

func (s *session) arguments(keys, args [][]byte) {
	globals := s.state.Globals()
	globals.SetReadOnly(false)
	defer globals.SetReadOnly(true)
	for i, values := range [][][]byte{keys, args} {
		table := s.state.NewTable(len(values), 0)
		for j, value := range values {
			_ = table.RawSet(lua.Number(float64(j+1)), lua.String(string(value)))
		}
		name := "KEYS"
		if i == 1 {
			name = "ARGV"
		}
		_ = globals.RawSet(lua.String(name), table.Value())
	}
}

func (s *session) record(field string, value lua.Value) lua.Value {
	table := s.state.NewTable(0, 1)
	_ = table.RawSet(lua.String(field), value)
	return table.Value()
}
func cstring(text string) string {
	if index := strings.IndexByte(text, 0); index >= 0 {
		return text[:index]
	}
	return text
}

func redisErrorText(text string) string {
	text = cstring(text)
	code := "ERR"
	if strings.HasPrefix(text, "-") {
		text = text[1:]
		if index := strings.IndexByte(text, ' '); index >= 0 {
			code, text = text[:index], text[index+1:]
		}
	}
	return code + " " + strings.Trim(text, "\r\n")
}

func (s *session) failure(text string) error {
	text = redisErrorText(text)
	return &lua.Error{Value: s.record("err", lua.String(text)), Message: text}
}

func (s *session) openRedis() {
	functions := map[string]lua.GoFunction{
		"call":          func(_ *lua.State, args []lua.Value) ([]lua.Value, error) { return s.command(args, true, false) },
		"pcall":         func(_ *lua.State, args []lua.Value) ([]lua.Value, error) { return s.command(args, false, false) },
		"acl_check_cmd": func(_ *lua.State, args []lua.Value) ([]lua.Value, error) { return s.command(args, true, true) },
		"error_reply": func(_ *lua.State, args []lua.Value) ([]lua.Value, error) {
			if len(args) != 1 || args[0].Type() != lua.TypeString {
				return []lua.Value{s.record("err", lua.String(redisErrorText("wrong number or type of arguments")))}, nil
			}
			text := cstring(args[0].Text())
			if !strings.HasPrefix(text, "-") {
				text = "-" + text
			}
			return []lua.Value{s.record("err", lua.String(redisErrorText(text)))}, nil
		},
		"status_reply": func(_ *lua.State, args []lua.Value) ([]lua.Value, error) {
			if len(args) != 1 || args[0].Type() != lua.TypeString {
				return []lua.Value{s.record("err", lua.String(redisErrorText("wrong number or type of arguments")))}, nil
			}
			return []lua.Value{s.record("ok", args[0])}, nil
		},
		"sha1hex": func(_ *lua.State, args []lua.Value) ([]lua.Value, error) {
			if len(args) != 1 {
				return nil, s.failure("wrong number of arguments")
			}
			text := ""
			if args[0].Type() == lua.TypeString || args[0].Type() == lua.TypeNumber {
				text = args[0].String()
			}
			digest := sha1.Sum([]byte(text))
			return []lua.Value{lua.String(hex.EncodeToString(digest[:]))}, nil
		},
		"setresp": func(_ *lua.State, args []lua.Value) ([]lua.Value, error) {
			if len(args) != 1 {
				return nil, s.failure("redis.setresp() requires one argument.")
			}
			proto := int(numeric(args[0]))
			if proto != 2 && proto != 3 {
				return nil, s.failure("RESP version must be 2 or 3.")
			}
			s.active.resp = proto
			return nil, nil
		},
		"set_repl": func(_ *lua.State, args []lua.Value) ([]lua.Value, error) {
			if len(args) != 1 {
				return nil, s.failure("redis.set_repl() requires one argument.")
			}
			flags := int(numeric(args[0]))
			if flags & ^3 != 0 {
				return nil, s.failure("Invalid replication flags. Use REPL_AOF, REPL_REPLICA, REPL_ALL or REPL_NONE.")
			}
			s.engine.mu.Lock()
			s.active.info.Replication = flags
			s.engine.mu.Unlock()
			return nil, nil
		},
		"replicate_commands": func(_ *lua.State, _ []lua.Value) ([]lua.Value, error) { return []lua.Value{lua.Bool(true)}, nil },
		"breakpoint":         func(_ *lua.State, _ []lua.Value) ([]lua.Value, error) { return nil, nil },
		"debug":              func(_ *lua.State, _ []lua.Value) ([]lua.Value, error) { return nil, nil },
		"log":                s.log,
	}
	table := s.state.NewTable(0, len(functions)+11)
	names := make([]string, 0, len(functions))
	for name := range functions {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		_ = table.RawSet(lua.String(name), s.state.Function(name, functions[name]))
	}
	for name, value := range map[string]int{"LOG_DEBUG": 0, "LOG_VERBOSE": 1, "LOG_NOTICE": 2, "LOG_WARNING": 3, "REPL_NONE": 0, "REPL_AOF": 1, "REPL_SLAVE": 2, "REPL_REPLICA": 2, "REPL_ALL": 3, "REDIS_VERSION_NUM": s.engine.config.VersionNum} {
		_ = table.RawSet(lua.String(name), lua.Number(float64(value)))
	}
	_ = table.RawSet(lua.String("REDIS_VERSION"), lua.String(s.engine.config.Version))
	_ = s.state.Globals().RawSet(lua.String("redis"), table.Value())
}

func numeric(value lua.Value) float64 {
	if value.Type() == lua.TypeNumber {
		return value.Float()
	}
	if value.Type() == lua.TypeString {
		if n, err := strconv.ParseFloat(strings.TrimSpace(value.Text()), 64); err == nil {
			return n
		}
	}
	return 0
}

func (s *session) log(_ *lua.State, args []lua.Value) ([]lua.Value, error) {
	if len(args) < 2 {
		return nil, s.failure("redis.log() requires two arguments or more.")
	}
	if args[0].Type() != lua.TypeNumber && args[0].Type() != lua.TypeString {
		return nil, s.failure("First argument must be a number (log level).")
	}
	if args[0].Type() == lua.TypeString {
		if _, err := strconv.ParseFloat(strings.TrimSpace(args[0].Text()), 64); err != nil {
			return nil, s.failure("First argument must be a number (log level).")
		}
	}
	level := int(numeric(args[0]))
	if level < 0 || level > 3 {
		return nil, s.failure("Invalid debug level.")
	}
	var message strings.Builder
	for i, value := range args[1:] {
		if value.Type() == lua.TypeString || value.Type() == lua.TypeNumber {
			if i > 0 {
				message.WriteByte(' ')
			}
			message.WriteString(value.String())
		}
	}
	if s.active.run.Host != nil {
		s.active.run.Host.Log(level, message.String())
	}
	return nil, nil
}

func commandNumber(number float64) string {
	if math.IsNaN(number) {
		if math.Signbit(number) {
			return "-nan"
		}
		return "nan"
	}
	if math.IsInf(number, 1) {
		return "inf"
	}
	if math.IsInf(number, -1) {
		return "-inf"
	}
	if number >= -0x1p62 && number <= 0x1p62 && math.Trunc(number) == number {
		return strconv.FormatInt(int64(number), 10)
	}
	negative := math.Signbit(number)
	number = math.Abs(number)
	text := strconv.FormatFloat(number, 'e', -1, 64)
	index := strings.IndexByte(text, 'e')
	digits := strings.ReplaceAll(text[:index], ".", "")
	exponent, _ := strconv.Atoi(text[index+1:])
	k := exponent - len(digits) + 1
	sign := ""
	if negative {
		sign = "-"
	}
	absolute := exponent
	if absolute < 0 {
		absolute = -absolute
	}
	if k >= 0 && absolute < len(digits)+7 {
		return sign + digits + strings.Repeat("0", k)
	}
	if k < 0 && (k > -7 || absolute < 4) {
		point := len(digits) + k
		if point <= 0 {
			return sign + "0." + strings.Repeat("0", -point) + digits
		}
		return sign + digits[:point] + "." + digits[point:]
	}
	result := sign + digits[:1]
	if len(digits) > 1 {
		result += "." + digits[1:]
	}
	result += "e"
	if exponent >= 0 {
		result += "+"
	}
	return result + strconv.Itoa(exponent)
}

func (s *session) command(args []lua.Value, raise, acl bool) ([]lua.Value, error) {
	fail := func(message string) ([]lua.Value, error) {
		failure := s.failure(message)
		if raise || acl {
			return nil, failure
		}
		return []lua.Value{failure.(*lua.Error).Value}, nil
	}
	if len(args) == 0 {
		return fail("Please specify at least one argument for this redis lib call")
	}
	argv := make([][]byte, len(args))
	for i, value := range args {
		switch value.Type() {
		case lua.TypeString:
			argv[i] = []byte(value.Text())
		case lua.TypeNumber:
			argv[i] = []byte(commandNumber(value.Float()))
		default:
			return fail("Lua redis lib command arguments must be strings or integers")
		}
	}
	host := s.active.run.Host
	var metadata Command
	var found bool
	if host != nil {
		metadata, found = host.Lookup(argv)
	}
	if !found {
		if acl {
			return fail("Invalid command passed to redis.acl_check_cmd()")
		}
		return fail("Unknown Redis command called from script")
	}
	if metadata.Arity > 0 && len(argv) != metadata.Arity || metadata.Arity < 0 && len(argv) < -metadata.Arity {
		if acl {
			return fail("Wrong number of args for redis.acl_check_cmd()")
		}
		return fail("Wrong number of args calling Redis command from script")
	}
	if acl {
		return []lua.Value{lua.Bool(host.Check(argv) == nil)}, nil
	}
	if metadata.NoScript {
		return fail("This Redis command is not allowed from script")
	}
	if failure := host.Check(argv); failure != nil {
		if failure.Err != nil {
			s.active.abort = failure
			return nil, &lua.Error{Abort: failure.Err}
		}
		return fail("-" + failure.Str)
	}
	if metadata.Write && s.active.run.ReadOnly {
		return fail("Write commands are not allowed from read-only scripts.")
	}
	if metadata.Write {
		s.active.written.Store(true)
	}
	s.engine.mu.Lock()
	s.active.info.Command = cloneArguments(argv)
	s.engine.mu.Unlock()
	reply := host.Call(argv, s.active.resp)
	if reply.Err != nil {
		s.active.abort = &reply
		return nil, &lua.Error{Abort: reply.Err}
	}
	if reply.Type == ErrorReply {
		return fail("-" + reply.Str)
	}
	return []lua.Value{s.toLua(reply, 0)}, nil
}

func (s *session) fromLua(value lua.Value, depth int) Reply {
	if depth >= luaReplyStackLimit {
		return Error("ERR reached lua stack limit")
	}
	switch value.Type() {
	case lua.TypeString:
		return Bulk(value.Text())
	case lua.TypeNumber:
		n := value.Float()
		if math.IsNaN(n) || n >= 0x1p63 || n < -0x1p63 {
			return Integer(math.MinInt64)
		}
		return Integer(int64(n))
	case lua.TypeBoolean:
		if s.active.resp == 3 {
			return Boolean(value.Truth())
		}
		if value.Truth() {
			return Integer(1)
		}
		return Null()
	case lua.TypeTable:
		table := value.Table()
		for _, field := range []string{"err", "ok", "double", "big_number"} {
			item := table.RawGet(lua.String(field))
			switch {
			case field == "err" && item.Type() == lua.TypeString:
				return Error(cstring(item.Text()))
			case field == "ok" && item.Type() == lua.TypeString:
				return Status(cstring(item.Text()))
			case field == "double" && item.Type() == lua.TypeNumber:
				return Double(item.Float())
			case field == "big_number" && item.Type() == lua.TypeString:
				return BigNumber(item.Text())
			}
		}
		if object := table.RawGet(lua.String("verbatim_string")).Table(); object != nil {
			format, text := object.RawGet(lua.String("format")), object.RawGet(lua.String("string"))
			if format.Type() == lua.TypeString && text.Type() == lua.TypeString {
				return Verbatim(format.Text(), text.Text())
			}
		}
		if object := table.RawGet(lua.String("map")).Table(); object != nil {
			result := Map()
			for key, item, err := object.Next(lua.Nil()); !key.IsNil() && err == nil; key, item, err = object.Next(key) {
				result.Pairs = append(result.Pairs, Pair{s.fromLua(key, depth+4), s.fromLua(item, depth+3)})
			}
			return result
		}
		if object := table.RawGet(lua.String("set")).Table(); object != nil {
			result := Set()
			for key, _, err := object.Next(lua.Nil()); !key.IsNil() && err == nil; key, _, err = object.Next(key) {
				result.Elements = append(result.Elements, s.fromLua(key, depth+3))
			}
			return result
		}
		result := Array()
		for index := 1; ; index++ {
			item := table.RawGet(lua.Number(float64(index)))
			if item.IsNil() {
				break
			}
			result.Elements = append(result.Elements, s.fromLua(item, depth+1))
		}
		return result
	default:
		return Null()
	}
}

func (s *session) toLua(reply Reply, depth int) lua.Value {
	if depth >= luaReplyStackLimit {
		return lua.Nil()
	}
	switch reply.Type {
	case BulkReply:
		return lua.String(reply.Str)
	case IntegerReply:
		return lua.Number(float64(reply.Int))
	case StatusReply:
		return s.record("ok", lua.String(reply.Str))
	case ErrorReply:
		return s.record("err", lua.String(reply.Str))
	case NullReply, NullArrayReply:
		if s.active.resp == 3 {
			return lua.Nil()
		}
		return lua.Bool(false)
	case BooleanReply:
		if s.active.resp == 3 {
			return lua.Bool(reply.Bool)
		}
		if reply.Bool {
			return lua.Number(1)
		}
		return lua.Number(0)
	case DoubleReply:
		if s.active.resp == 3 {
			return s.record("double", lua.Number(reply.Float))
		}
		return lua.String(doubleText(reply.Float))
	case BigNumberReply:
		if s.active.resp == 3 {
			return s.record("big_number", lua.String(reply.Str))
		}
		return lua.String(reply.Str)
	case VerbatimReply:
		if s.active.resp == 3 {
			table := s.state.NewTable(0, 2)
			_ = table.RawSet(lua.String("format"), lua.String(reply.Format))
			_ = table.RawSet(lua.String("string"), lua.String(reply.Str))
			return s.record("verbatim_string", table.Value())
		}
		return lua.String(reply.Str)
	case MapReply:
		table := s.state.NewTable(0, len(reply.Pairs))
		for index, pair := range reply.Pairs {
			if s.active.resp == 3 {
				_ = table.RawSet(s.toLua(pair.Key, depth+1), s.toLua(pair.Value, depth+1))
			} else {
				_ = table.RawSet(lua.Number(float64(index*2+1)), s.toLua(pair.Key, depth+1))
				_ = table.RawSet(lua.Number(float64(index*2+2)), s.toLua(pair.Value, depth+1))
			}
		}
		if s.active.resp == 3 {
			return s.record("map", table.Value())
		}
		return table.Value()
	case ArrayReply, SetReply:
		table := s.state.NewTable(len(reply.Elements), 0)
		for index, item := range reply.Elements {
			if reply.Type == SetReply && s.active.resp == 3 {
				_ = table.RawSet(s.toLua(item, depth+1), lua.Bool(true))
			} else {
				_ = table.RawSet(lua.Number(float64(index+1)), s.toLua(item, depth+1))
			}
		}
		if reply.Type == SetReply && s.active.resp == 3 {
			return s.record("set", table.Value())
		}
		return table.Value()
	default:
		return lua.Nil()
	}
}
