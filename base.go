package lua

import (
	"fmt"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

func argument(args []Value, index int) Value {
	if index < len(args) {
		return args[index]
	}
	return Nil()
}

func (s *State) argError(index int, message string) {
	f := s.current.frames[len(s.current.frames)-1]
	name := ""
	method := false
	if f.site != nil {
		info := s.resolveName(f.site.left)
		name = info.name
		method = f.site.method || info.kind == "method"
	}
	if name == "" {
		name = "?"
	}
	if method {
		index--
		if index == 0 {
			s.raise(fmt.Sprintf("calling '%s' on bad self (%s)", name, message), 1)
		}
	}
	s.raise(fmt.Sprintf("bad argument #%d to '%s' (%s)", index, name, message), 1)
}

func (s *State) checkAny(args []Value, index int) Value {
	if index >= len(args) {
		s.argError(index+1, "value expected")
	}
	return args[index]
}

func (s *State) expected(args []Value, index int, expected string) {
	got := "no value"
	if index < len(args) {
		got = args[index].Type().String()
	}
	s.argError(index+1, expected+" expected, got "+got)
}

func (s *State) checkNumber(args []Value, index int) float64 {
	if index < len(args) && args[index].object == &numberMarker {
		return args[index].number
	}
	value, ok := argument(args, index).toNumber()
	if index >= len(args) || !ok {
		s.expected(args, index, "number")
	}
	return value
}

func (s *State) checkText(args []Value, index int) string {
	if index < len(args) && args[index].object == nil && args[index].text != nil {
		return args[index].text.text
	}
	value, ok := argument(args, index).toString()
	if index >= len(args) || !ok {
		s.expected(args, index, "string")
	}
	return value
}

func (s *State) checkTable(args []Value, index int) *Table {
	value := argument(args, index).Table()
	if value == nil {
		s.expected(args, index, "table")
	}
	return value
}

func (s *State) optionalInt(args []Value, index, fallback int) int {
	if index >= len(args) || args[index].IsNil() {
		return fallback
	}
	return int(luaInteger(s.checkNumber(args, index)))
}

func (s *State) openBase() {
	next := s.Function("next", func(s *State, args []Value) ([]Value, error) {
		key, value, err := s.checkTable(args, 0).Next(argument(args, 1))
		if err != nil {
			return nil, err
		}
		if key.IsNil() {
			return s.values1(Nil()), nil
		}
		return s.values2(key, value), nil
	})
	ipairs := s.Function("ipairs", func(s *State, args []Value) ([]Value, error) {
		index := int(s.checkNumber(args, 1)) + 1
		table := s.checkTable(args, 0)
		value := table.RawGet(Number(float64(index)))
		if value.IsNil() {
			return nil, nil
		}
		return s.values2(Number(float64(index)), value), nil
	})
	s.ipairsStep = ipairs.object.function
	s.globals.set(String("next"), next)
	functions := map[string]GoFunction{
		"assert": baseAssert, "error": baseError, "tonumber": baseToNumber,
		"tostring": baseToString, "select": baseSelect, "unpack": baseUnpack,
		"pcall": basePCall, "xpcall": baseXPCall, "loadstring": baseLoadString,
		"load": baseLoad, "collectgarbage": baseCollectGarbage,
		"type": func(s *State, args []Value) ([]Value, error) {
			return s.values1(typeNames[s.checkAny(args, 0).Type()]), nil
		},
		"rawequal": func(s *State, args []Value) ([]Value, error) {
			return s.values1(Bool(s.checkAny(args, 0).Equal(s.checkAny(args, 1)))), nil
		},
		"rawget": func(s *State, args []Value) ([]Value, error) {
			return s.values1(s.checkTable(args, 0).RawGet(s.checkAny(args, 1))), nil
		},
		"rawset": func(s *State, args []Value) ([]Value, error) {
			table := s.checkTable(args, 0)
			key := s.checkAny(args, 1)
			value := s.checkAny(args, 2)
			if err := table.RawSet(key, value); err != nil {
				return nil, err
			}
			return s.values1(table.Value()), nil
		},
		"getmetatable": func(s *State, args []Value) ([]Value, error) {
			value := s.checkAny(args, 0)
			var table *Table
			switch value.Type() {
			case TypeString:
				table = s.stringMeta
			case TypeTable:
				table = value.Table().metatable
			}
			if table == nil {
				return s.values1(Nil()), nil
			}
			if protected := table.RawGet(String("__metatable")); !protected.IsNil() {
				return s.values1(protected), nil
			}
			return s.values1(table.Value()), nil
		},
		"setmetatable": func(s *State, args []Value) ([]Value, error) {
			table := s.checkTable(args, 0)
			meta := s.checkAny(args, 1)
			if !meta.IsNil() && meta.Type() != TypeTable {
				s.argError(2, "nil or table expected")
			}
			if table.readonly {
				return nil, plainError("Attempt to modify a readonly table")
			}
			if table.metatable != nil && !table.metatable.RawGet(String("__metatable")).IsNil() {
				s.raise("cannot change a protected metatable", 1)
			}
			table.metatable = meta.Table()
			return s.values1(table.Value()), nil
		},
		"pairs": func(s *State, args []Value) ([]Value, error) {
			table := s.checkTable(args, 0)
			return s.values3(next, table.Value(), Nil()), nil
		},
		"ipairs": func(s *State, args []Value) ([]Value, error) {
			table := s.checkTable(args, 0)
			return s.values3(ipairs, table.Value(), Number(0)), nil
		},
		"gcinfo": func(_ *State, _ []Value) ([]Value, error) {
			var stats runtime.MemStats
			runtime.ReadMemStats(&stats)
			return []Value{Number(float64(stats.Alloc) / 1024)}, nil
		},
	}
	names := make([]string, 0, len(functions))
	for name := range functions {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		s.globals.set(String(name), s.Function(name, functions[name]))
	}
	s.globals.RawGet(String("pairs")).object.function.upvalues = []*upvalue{{value: next}}
	s.globals.RawGet(String("ipairs")).object.function.upvalues = []*upvalue{{value: ipairs}}
	s.globals.set(String("_G"), s.globals.Value())
	s.globals.set(String("_VERSION"), String("Lua 5.1"))
}

func baseAssert(s *State, args []Value) ([]Value, error) {
	value := s.checkAny(args, 0)
	if !value.Truth() {
		message := "assertion failed!"
		if len(args) > 1 && !args[1].IsNil() {
			message = cString(s.checkText(args, 1))
		}
		s.raise(message, 1)
	}
	return args, nil
}

func baseError(s *State, args []Value) ([]Value, error) {
	value := argument(args, 0)
	level := s.optionalInt(args, 1, 1)
	if text, ok := value.toString(); ok && level > 0 {
		s.raise(text, level)
	}
	panic(s.processPanic(&Error{Value: value, Message: value.String(), Line: s.currentLine()}))
}

func baseToString(s *State, args []Value) ([]Value, error) {
	value := s.checkAny(args, 0)
	if method := s.metaFor(value, eventToString); !method.IsNil() {
		return s.values1(first(s.invoke(method, []Value{value}))), nil
	}
	return s.values1(String(value.String())), nil
}

func baseToNumber(s *State, args []Value) ([]Value, error) {
	base := s.optionalInt(args, 1, 10)
	if base == 10 {
		value := s.checkAny(args, 0)
		if n, ok := value.toNumber(); ok {
			return s.values1(Number(n)), nil
		}
		return s.values1(Nil()), nil
	}
	text := strings.Trim(cString(s.checkText(args, 0)), " \t\n\r\v\f")
	if base < 2 || base > 36 {
		s.argError(2, "base out of range")
	}
	negative := strings.HasPrefix(text, "-")
	if strings.HasPrefix(text, "-") || strings.HasPrefix(text, "+") {
		text = text[1:]
	}
	if base == 16 && len(text) >= 2 && text[0] == '0' && (text[1] == 'x' || text[1] == 'X') {
		text = text[2:]
	}
	if text == "" || strings.ContainsAny(text, "_+-") {
		return s.values1(Nil()), nil
	}
	n, err := strconv.ParseUint(text, base, 64)
	if err != nil {
		if parsed, ok := err.(*strconv.NumError); !ok || parsed.Err != strconv.ErrRange {
			return s.values1(Nil()), nil
		}
	} else if negative {
		n = -n
	}
	return s.values1(Number(float64(n))), nil
}

func baseSelect(s *State, args []Value) ([]Value, error) {
	if len(args) > 0 && args[0].Type() == TypeString && strings.HasPrefix(args[0].Text(), "#") {
		return s.values1(Number(float64(len(args) - 1))), nil
	}
	index := int(s.checkNumber(args, 0))
	if index < 0 {
		index += len(args)
	}
	if index > len(args)-1 {
		index = len(args)
	}
	if index < 1 {
		s.argError(1, "index out of range")
	}
	return args[index:], nil
}

func baseUnpack(s *State, args []Value) ([]Value, error) {
	table := s.checkTable(args, 0)
	start, end := s.optionalInt(args, 1, 1), s.optionalInt(args, 2, table.Len())
	if end < start {
		return nil, nil
	}
	if end-start >= 8000 || end-start < 0 {
		s.raise("too many results to unpack", 1)
	}
	values := make([]Value, end-start+1)
	for index := range values {
		s.tick()
		values[index] = table.RawGet(Number(float64(start + index)))
	}
	return values, nil
}

func basePCall(s *State, args []Value) ([]Value, error) {
	if len(args) == 0 {
		return s.values2(Bool(false), String("attempt to call a boolean value")), nil
	}
	var values []Value
	var err error
	if s.current.handlerActive {
		values, err = s.protectedHandler(args[0], args[1:], Nil(), false)
	} else {
		values, err = s.protected(args[0], args[1:])
	}
	if abortError(err) {
		return nil, err
	}
	if err != nil {
		failure := err.(*Error)
		value := failure.Value
		if value.Type() == TypeTable {
			field := s.get(value, String("err"), nameInfo{})
			if field.Type() == TypeString || field.Type() == TypeNumber {
				return s.values2(Bool(false), field), nil
			}
			return s.values3(Bool(false), value, field), nil
		}
		return s.values2(Bool(false), value), nil
	}
	return s.prepend(Bool(true), values), nil
}

func baseXPCall(s *State, args []Value) ([]Value, error) {
	fn := s.checkAny(args, 0)
	handler := s.checkAny(args, 1)
	values, err := s.protectedHandler(fn, nil, handler, true)
	if abortError(err) {
		return nil, err
	}
	if err == nil {
		return s.prepend(Bool(true), values), nil
	}
	return s.values2(Bool(false), err.(*Error).Value), nil
}

func baseLoadString(s *State, args []Value) ([]Value, error) {
	source := s.checkText(args, 0)
	chunk := source
	if len(args) > 1 && !args[1].IsNil() {
		chunk = s.checkText(args, 1)
	}
	s.checkString(len(source))
	proto, err := compile([]byte(source), chunk, s.current.cDepth)
	if err != nil {
		if failure, ok := err.(*Error); ok && failure.memory {
			if raised, ok := s.processPanic(failure).(*Error); ok {
				return s.values2(Nil(), raised.Value), nil
			}
		}
		return s.values2(Nil(), String(err.Error())), nil
	}
	return s.values1(s.Load(proto, s.globals)), nil
}

func baseLoad(s *State, args []Value) ([]Value, error) {
	reader := argument(args, 0)
	if reader.Type() != TypeFunction {
		s.expected(args, 0, "function")
	}
	chunk := "=(load)"
	if len(args) > 1 && !args[1].IsNil() {
		chunk = s.checkText(args, 1)
	}
	return s.loadReader(reader, chunk)
}

func (s *State) loadReader(reader Value, chunk string) (values []Value, err error) {
	t := s.current
	depth, used, top := len(t.frames), t.used, t.top
	cDepth, nativeDepth, boundary := t.cDepth, t.nativeDepth, t.boundary
	defer func() {
		if raised := recover(); raised != nil {
			raised = s.processPanic(raised)
			failure, ok := raised.(*Error)
			if !ok {
				panic(raised)
			}
			t.close(used)
			clear(t.stack[used:t.used])
			t.frames = t.frames[:depth]
			t.used, t.top, t.cDepth, t.nativeDepth, t.boundary = used, top, cDepth, nativeDepth, boundary
			t.restoreCallLimit(depth)
			if abortError(failure) {
				values, err = nil, failure
			} else {
				values, err = []Value{Nil(), failure.Value}, nil
			}
		}
	}()
	var source strings.Builder
	for {
		s.tick()
		value := first(s.invoke(reader, nil))
		if value.IsNil() || value.Type() == TypeString && value.Text() == "" {
			break
		}
		text, ok := value.toString()
		if !ok {
			s.raise("reader function must return a string", 1)
		}
		if source.Len() > s.options.MaxString-len(text) {
			s.raise("string length overflow", 1)
		}
		source.WriteString(text)
	}
	return baseLoadString(s, []Value{String(source.String()), String(chunk)})
}

func baseCollectGarbage(s *State, args []Value) ([]Value, error) {
	option := "collect"
	if len(args) > 0 && !args[0].IsNil() {
		option = s.checkText(args, 0)
	}
	switch option {
	case "count":
		var stats runtime.MemStats
		runtime.ReadMemStats(&stats)
		return s.values1(Number(float64(stats.Alloc) / 1024)), nil
	case "step":
		s.collect()
		return s.values1(Bool(true)), nil
	case "setpause", "setstepmul":
		return s.values1(Number(200)), nil
	case "collect":
		s.collect()
		return s.values1(Number(0)), nil
	case "stop", "restart":
		return s.values1(Number(0)), nil
	default:
		s.argError(1, "invalid option '"+option+"'")
	}
	return nil, nil
}
