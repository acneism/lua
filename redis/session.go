package redis

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"math"
	"strconv"
	"strings"

	"github.com/acneism/lua"
)

type session struct {
	engine  *Engine
	state   *lua.State
	globals *lua.Table
	active  *execution
	raise   lua.Value
	handler lua.Value
}

const errorHandlerSource = "local dbg = debug\n" +
	"debug = nil\n" +
	"function __redis__err__handler(err)\n" +
	"  local i = dbg.getinfo(2,'nSl')\n" +
	"  if i and i.what == 'C' then\n" +
	"    i = dbg.getinfo(3,'nSl')\n" +
	"  end\n" +
	"  if type(err) ~= 'table' then\n" +
	"    err = {err='ERR ' .. tostring(err)}" +
	"  end" +
	"  if i then\n" +
	"    err['source'] = i.source\n" +
	"    err['line'] = i.currentline\n" +
	"  end" +
	"  return err\n" +
	"end\n"

var errorHandlerProto = func() *lua.Proto {
	proto, err := lua.Compile([]byte(errorHandlerSource), "@err_handler_def")
	if err != nil {
		panic(err)
	}
	return proto
}()

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
	original := s.state.Globals()
	globals := s.state.NewTable(0, 2)
	s.globals = globals
	s.raise = original.RawGet(lua.String("error"))
	_ = globals.RawSet(lua.String("_G"), globals.Value())
	for _, name := range []string{"assert", "collectgarbage", "dofile", "error", "gcinfo", "getfenv", "getmetatable", "loadfile", "load", "loadstring", "next", "pcall", "print", "rawequal", "rawget", "rawset", "select", "setfenv", "setmetatable", "tonumber", "tostring", "type", "unpack", "xpcall", "_VERSION", "ipairs", "pairs", "newproxy", "coroutine"} {
		_ = globals.RawSet(lua.String(name), allowed(name, original.RawGet(lua.String(name))))
	}
	for _, name := range []string{"table", "string", "math", "debug", "cjson", "struct", "cmsgpack", "bit"} {
		value := original.RawGet(lua.String(name))
		if name == "debug" {
			value = s.debugLibrary()
		} else if value.IsNil() {
			value = s.state.NewTable(0, 0).Value()
		}
		_ = globals.RawSet(lua.String(name), value)
	}
	s.openRedis()
	if _, err := s.state.Call(s.state.Load(errorHandlerProto, globals)); err != nil {
		panic(err)
	}
	s.handler = globals.RawGet(lua.String("__redis__err__handler"))
	meta := s.state.NewTable(0, 0)
	_ = meta.RawSet(lua.String("__index"), s.state.Function("__index", func(state *lua.State, args []lua.Value) ([]lua.Value, error) {
		message := "Second argument to luaProtectedTableError must be a string or number"
		if len(args) != 2 {
			message = "Wrong number of arguments to luaProtectedTableError"
		} else if args[1].Type() == lua.TypeString || args[1].Type() == lua.TypeNumber {
			message = "Script attempted to access nonexistent global variable '" + cstring(args[1].String()) + "'"
		}
		_, err := state.Call(s.raise, lua.String(message), lua.Number(2))
		if failure, ok := err.(*lua.Error); ok {
			return nil, &lua.Error{Value: failure.Value, Message: failure.Message, Line: failure.Line, Abort: failure.Abort, Internal: failure.Internal}
		}
		return nil, err
	}))
	_ = globals.SetMetatable(meta)
	protect(globals)
	s.state.SetGlobals(globals)
	return s
}

func protect(table *lua.Table) {
	if table.ReadOnly() {
		return
	}
	table.SetReadOnly(true)
	for key, value, err := table.Next(lua.Nil()); !key.IsNil() && err == nil; key, value, err = table.Next(key) {
		if child := value.Table(); child != nil {
			protect(child)
		}
	}
	if meta := table.Metatable(); meta != nil {
		protect(meta)
	}
}

func (s *session) debugLibrary() lua.Value {
	library := s.state.NewTable(0, 0)
	_ = library.RawSet(lua.String("getinfo"), s.state.Function("getinfo", func(state *lua.State, args []lua.Value) ([]lua.Value, error) {
		level := 0
		if len(args) > 0 {
			number, _ := numeric(args[0])
			level = int(number)
		}
		frame, ok := state.Frame(level)
		if !ok {
			return []lua.Value{lua.Nil()}, nil
		}
		info := state.NewTable(0, 0)
		_ = info.RawSet(lua.String("what"), lua.String(frame.What))
		_ = info.RawSet(lua.String("source"), lua.String(frame.Source))
		_ = info.RawSet(lua.String("currentline"), lua.Number(float64(frame.Line)))
		return []lua.Value{info.Value()}, nil
	}))
	return library.Value()
}

func (s *session) field(value lua.Value, name string) (string, bool) {
	item, err := s.state.Index(value, lua.String(name))
	if err != nil {
		return "", false
	}
	switch item.Type() {
	case lua.TypeString, lua.TypeNumber:
		return cstring(item.String()), true
	}
	return "", false
}

func safeText(text string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(text)
}

func (s *session) errorReply(value lua.Value, name string) Reply {
	if value.Table() == nil {
		message := "execution failure"
		if value.Type() == lua.TypeString || value.Type() == lua.TypeNumber {
			message = cstring(value.String())
		}
		if len(message) > 100 {
			message = message[:100]
		}
		return Error(safeText("ERR " + strings.TrimRight("Error running script "+name+", "+message, "\r\n")))
	}
	message, found := s.field(value, "err")
	source, hasSource := s.field(value, "source")
	line, hasLine := s.field(value, "line")
	s.field(value, "ignore_error_stats_update")
	if !found {
		message = "ERR unknown error"
	}
	if hasSource && hasLine {
		message += " script: " + name + ", on " + source + ":" + line + "."
	}
	return Error(safeText(message))
}

func allowed(name string, value lua.Value) lua.Value {
	switch name {
	case "dofile", "getfenv", "loadfile", "print", "setfenv", "newproxy":
		return lua.Nil()
	}
	return value
}

func (s *session) arguments(keys, args [][]byte) {
	globals := s.globals
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

func (s *session) commandFailure(text string) error {
	failure := s.failure(text).(*lua.Error)
	_ = failure.Value.Table().RawSet(lua.String("ignore_error_stats_update"), lua.Bool(true))
	return failure
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
			number, _ := numeric(args[0])
			proto := int(number)
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
			number, _ := numeric(args[0])
			flags := int(number)
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
	table := s.state.NewTable(0, 0)
	constants := map[string]lua.Value{"LOG_DEBUG": lua.Number(0), "LOG_VERBOSE": lua.Number(1), "LOG_NOTICE": lua.Number(2), "LOG_WARNING": lua.Number(3), "REPL_NONE": lua.Number(0), "REPL_AOF": lua.Number(1), "REPL_SLAVE": lua.Number(2), "REPL_REPLICA": lua.Number(2), "REPL_ALL": lua.Number(3), "REDIS_VERSION_NUM": lua.Number(float64(s.engine.config.VersionNum)), "REDIS_VERSION": lua.String(s.engine.config.Version)}
	for _, name := range []string{"call", "pcall", "log", "LOG_DEBUG", "LOG_VERBOSE", "LOG_NOTICE", "LOG_WARNING", "REDIS_VERSION_NUM", "REDIS_VERSION", "setresp", "sha1hex", "error_reply", "status_reply", "set_repl", "REPL_NONE", "REPL_AOF", "REPL_SLAVE", "REPL_REPLICA", "REPL_ALL", "acl_check_cmd", "breakpoint", "debug", "replicate_commands"} {
		value := constants[name]
		if function := functions[name]; function != nil {
			value = s.state.Function(name, function)
		}
		_ = table.RawSet(lua.String(name), value)
	}
	_ = s.globals.RawSet(lua.String("redis"), table.Value())
}

func numeric(value lua.Value) (float64, bool) {
	if value.Type() == lua.TypeNumber {
		return value.Float(), true
	}
	if value.Type() == lua.TypeString {
		return parseNumeric(value.Text())
	}
	return 0, false
}

func parseNumeric(text string) (float64, bool) {
	text = strings.Trim(cstring(text), " \t\n\r\v\f")
	if text == "" {
		return 0, false
	}
	unsigned := strings.ToLower(text)
	sign := 1.0
	if unsigned[0] == '-' || unsigned[0] == '+' {
		if unsigned[0] == '-' {
			sign = -1
		}
		unsigned = unsigned[1:]
	}
	if unsigned == "inf" || unsigned == "infinity" {
		return math.Inf(int(sign)), true
	}
	if unsigned == "nan" {
		return math.Copysign(math.NaN(), sign), true
	}
	if strings.HasPrefix(unsigned, "nan(") && strings.HasSuffix(unsigned, ")") {
		valid := true
		for _, c := range unsigned[4 : len(unsigned)-1] {
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' {
				valid = false
				break
			}
		}
		if valid {
			return math.Copysign(math.NaN(), sign), true
		}
	}
	if strings.ContainsRune(text, '_') {
		return 0, false
	}
	if strings.HasPrefix(unsigned, "0x") && !strings.ContainsRune(unsigned, 'p') {
		text += "p0"
	}
	n, err := strconv.ParseFloat(text, 64)
	if err == nil {
		return n, true
	}
	if failure, ok := err.(*strconv.NumError); ok && failure.Err == strconv.ErrRange {
		return n, true
	}
	return 0, false
}

func (s *session) log(_ *lua.State, args []lua.Value) ([]lua.Value, error) {
	if len(args) < 2 {
		return nil, s.failure("redis.log() requires two arguments or more.")
	}
	number, ok := numeric(args[0])
	if !ok {
		return nil, s.failure("First argument must be a number (log level).")
	}
	level := int(number)
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
	result := func(failure error) ([]lua.Value, error) {
		if raise || acl {
			return nil, failure
		}
		return []lua.Value{failure.(*lua.Error).Value}, nil
	}
	fail := func(message string) ([]lua.Value, error) { return result(s.failure(message)) }
	reject := func(message string) ([]lua.Value, error) {
		if acl {
			return fail(message)
		}
		return result(s.commandFailure(message))
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
		return reject("Unknown Redis command called from script")
	}
	if metadata.Arity > 0 && len(argv) != metadata.Arity || metadata.Arity < 0 && len(argv) < -metadata.Arity {
		if acl {
			return fail("Wrong number of args for redis.acl_check_cmd()")
		}
		return reject("Wrong number of args calling Redis command from script")
	}
	if acl {
		return []lua.Value{lua.Bool(host.Check(argv) == nil)}, nil
	}
	if metadata.NoScript {
		return reject("This Redis command is not allowed from script")
	}
	if failure := host.Check(argv); failure != nil {
		if failure.Err != nil {
			s.active.abort = failure
			return nil, &lua.Error{Abort: failure.Err}
		}
		return reject("-" + failure.Str)
	}
	if (metadata.Write || metadata.MayReplicate) && s.active.run.ReadOnly {
		return reject("Write commands are not allowed from read-only scripts.")
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
		return reject("-" + reply.Str)
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
				message, _ := s.field(value, "err")
				s.field(value, "source")
				s.field(value, "line")
				s.field(value, "ignore_error_stats_update")
				return Error(strings.TrimRight(message, "\r\n"))
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
