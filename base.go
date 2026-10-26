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
	if f.site != nil {
		name = f.site.left.name
	}
	if name == "" {
		name = "?"
	}
	if f.site != nil && f.site.method {
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
	value, ok := argument(args, index).toNumber()
	if index >= len(args) || !ok {
		s.expected(args, index, "number")
	}
	return value
}

func (s *State) checkText(args []Value, index int) string {
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
			return []Value{Nil()}, nil
		}
		return []Value{key, value}, nil
	})
	ipairs := s.Function("ipairs", func(s *State, args []Value) ([]Value, error) {
		table := s.checkTable(args, 0)
		index := int(s.checkNumber(args, 1)) + 1
		value := table.RawGet(Number(float64(index)))
		if value.IsNil() {
			return nil, nil
		}
		return []Value{Number(float64(index)), value}, nil
	})
	s.globals.set(String("next"), next)
	functions := map[string]GoFunction{
		"assert": baseAssert, "error": baseError, "tonumber": baseToNumber,
		"tostring": baseToString, "select": baseSelect, "unpack": baseUnpack,
		"pcall": basePCall, "xpcall": baseXPCall, "loadstring": baseLoadString,
		"load": baseLoad, "collectgarbage": baseCollectGarbage,
		"type": func(s *State, args []Value) ([]Value, error) {
			return []Value{String(s.checkAny(args, 0).Type().String())}, nil
		},
		"rawequal": func(s *State, args []Value) ([]Value, error) {
			return []Value{Bool(s.checkAny(args, 0).Equal(s.checkAny(args, 1)))}, nil
		},
		"rawget": func(s *State, args []Value) ([]Value, error) {
			return []Value{s.checkTable(args, 0).RawGet(s.checkAny(args, 1))}, nil
		},
		"rawset": func(s *State, args []Value) ([]Value, error) {
			table := s.checkTable(args, 0)
			key := s.checkAny(args, 1)
			value := s.checkAny(args, 2)
			if err := table.RawSet(key, value); err != nil {
				return nil, err
			}
			return []Value{table.Value()}, nil
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
				return []Value{Nil()}, nil
			}
			if protected := table.RawGet(String("__metatable")); !protected.IsNil() {
				return []Value{protected}, nil
			}
			return []Value{table.Value()}, nil
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
			return []Value{table.Value()}, nil
		},
		"pairs": func(s *State, args []Value) ([]Value, error) {
			table := s.checkTable(args, 0)
			return []Value{next, table.Value(), Nil()}, nil
		},
		"ipairs": func(s *State, args []Value) ([]Value, error) {
			table := s.checkTable(args, 0)
			return []Value{ipairs, table.Value(), Number(0)}, nil
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
	s.globals.set(String("_G"), s.globals.Value())
	s.globals.set(String("_VERSION"), String("Lua 5.1"))
}

func baseAssert(s *State, args []Value) ([]Value, error) {
	value := s.checkAny(args, 0)
	if !value.Truth() {
		message := "assertion failed!"
		if len(args) > 1 && !args[1].IsNil() {
			message = s.checkText(args, 1)
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
	panic(&Error{Value: value, Message: value.String(), Line: s.currentLine()})
}

func baseToString(s *State, args []Value) ([]Value, error) {
	value := s.checkAny(args, 0)
	if method := s.meta(value, "__tostring"); !method.IsNil() {
		return []Value{first(s.invoke(method, []Value{value}))}, nil
	}
	return []Value{String(value.String())}, nil
}

func baseToNumber(s *State, args []Value) ([]Value, error) {
	base := s.optionalInt(args, 1, 10)
	if base == 10 {
		value := s.checkAny(args, 0)
		if n, ok := value.toNumber(); ok {
			return []Value{Number(n)}, nil
		}
		return []Value{Nil()}, nil
	}
	text := strings.Trim(s.checkText(args, 0), " \t\n\r\v\f")
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
		return []Value{Nil()}, nil
	}
	n, err := strconv.ParseUint(text, base, 64)
	if err != nil {
		if parsed, ok := err.(*strconv.NumError); !ok || parsed.Err != strconv.ErrRange {
			return []Value{Nil()}, nil
		}
	} else if negative {
		n = -n
	}
	return []Value{Number(float64(n))}, nil
}

func baseSelect(s *State, args []Value) ([]Value, error) {
	if len(args) > 0 && args[0].Type() == TypeString && strings.HasPrefix(args[0].Text(), "#") {
		return []Value{Number(float64(len(args) - 1))}, nil
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
		return []Value{Bool(false), String("attempt to call a boolean value")}, nil
	}
	values, err := s.protectedHandler(args[0], args[1:], Nil(), false)
	if abortError(err) {
		return nil, err
	}
	if err != nil {
		failure := err.(*Error)
		value := failure.Value
		if value.Type() == TypeTable {
			if text := value.Table().RawGet(String("err")); text.Type() == TypeString {
				return []Value{Bool(false), text}, nil
			}
			return []Value{Bool(false), value, Nil()}, nil
		}
		return []Value{Bool(false), value}, nil
	}
	return append([]Value{Bool(true)}, values...), nil
}

func baseXPCall(s *State, args []Value) ([]Value, error) {
	fn := s.checkAny(args, 0)
	handler := s.checkAny(args, 1)
	values, err := s.protectedHandler(fn, nil, handler, true)
	if abortError(err) {
		return nil, err
	}
	if err == nil {
		return append([]Value{Bool(true)}, values...), nil
	}
	return []Value{Bool(false), err.(*Error).Value}, nil
}

func baseLoadString(s *State, args []Value) ([]Value, error) {
	source := s.checkText(args, 0)
	chunk := source
	if len(args) > 1 && !args[1].IsNil() {
		chunk = s.checkText(args, 1)
	}
	s.checkString(len(source))
	proto, err := compile([]byte(source), chunk, max(0, s.current.cDepth-1))
	if err != nil {
		return []Value{Nil(), String(err.Error())}, nil
	}
	env := s.current.frames[len(s.current.frames)-1].function.env
	return []Value{s.Load(proto, env)}, nil
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
	var source strings.Builder
	for {
		s.tick()
		value := first(s.invoke(reader, nil))
		if value.IsNil() || value.Type() == TypeString && value.Text() == "" {
			break
		}
		text, ok := value.toString()
		if !ok {
			failure := "reader function must return a string"
			frames := s.current.frames
			if len(frames) >= 2 && frames[len(frames)-2].function.proto != nil {
				failure = locatedError(frames[len(frames)-2].function.proto.chunk, frames[len(frames)-2].sourceLine(), failure).Error()
			}
			return []Value{Nil(), String(failure)}, nil
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
		return []Value{Number(float64(stats.Alloc) / 1024)}, nil
	case "step":
		return []Value{Bool(false)}, nil
	case "setpause", "setstepmul":
		return []Value{Number(200)}, nil
	case "collect", "stop", "restart":
		return []Value{Number(0)}, nil
	default:
		s.argError(1, "invalid option '"+option+"'")
	}
	return nil, nil
}
