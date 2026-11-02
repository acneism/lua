package redis

import (
	"math"

	"github.com/acneism/lua"
)

func bitArgument(state *lua.State, args []lua.Value, index int) uint32 {
	if index < len(args) && args[index].Type() == lua.TypeNumber {
		return uint32(math.Float64bits(args[index].Float() + 6755399441055744.0))
	}
	return uint32(math.Float64bits(state.CheckNumber(args, index) + 6755399441055744.0))
}

func bitResult(state *lua.State, value uint32) ([]lua.Value, error) {
	return state.Result(lua.Number(float64(int32(value)))), nil
}

func bitAnd(state *lua.State, args []lua.Value) ([]lua.Value, error) {
	value := bitArgument(state, args, 0)
	for index := len(args) - 1; index > 0; index-- {
		value &= bitArgument(state, args, index)
	}
	return bitResult(state, value)
}

func bitOr(state *lua.State, args []lua.Value) ([]lua.Value, error) {
	value := bitArgument(state, args, 0)
	for index := len(args) - 1; index > 0; index-- {
		value |= bitArgument(state, args, index)
	}
	return bitResult(state, value)
}

func bitXor(state *lua.State, args []lua.Value) ([]lua.Value, error) {
	value := bitArgument(state, args, 0)
	for index := len(args) - 1; index > 0; index-- {
		value ^= bitArgument(state, args, index)
	}
	return bitResult(state, value)
}

func bitShiftArguments(state *lua.State, args []lua.Value) (uint32, uint32) {
	value := bitArgument(state, args, 0)
	return value, bitArgument(state, args, 1) & 31
}

func bitToHex(state *lua.State, args []lua.Value) ([]lua.Value, error) {
	value := bitArgument(state, args, 0)
	count := int32(8)
	if len(args) >= 2 {
		count = int32(bitArgument(state, args, 1))
	}
	digits := "0123456789abcdef"
	if count == math.MinInt32 {
		count = math.MinInt32 + 1
	}
	if count < 0 {
		count = -count
		digits = "0123456789ABCDEF"
	}
	count = min(count, 8)
	var buffer [8]byte
	for index := count - 1; index >= 0; index-- {
		buffer[index] = digits[value&15]
		value >>= 4
	}
	return state.Result(lua.String(string(buffer[:count]))), nil
}

func (s *session) openBit() lua.Value {
	functions := []struct {
		name     string
		function lua.GoFunction
	}{
		{"tobit", func(state *lua.State, args []lua.Value) ([]lua.Value, error) {
			return bitResult(state, bitArgument(state, args, 0))
		}},
		{"bnot", func(state *lua.State, args []lua.Value) ([]lua.Value, error) {
			return bitResult(state, ^bitArgument(state, args, 0))
		}},
		{"band", bitAnd},
		{"bor", bitOr},
		{"bxor", bitXor},
		{"lshift", func(state *lua.State, args []lua.Value) ([]lua.Value, error) {
			value, count := bitShiftArguments(state, args)
			return bitResult(state, value<<count)
		}},
		{"rshift", func(state *lua.State, args []lua.Value) ([]lua.Value, error) {
			value, count := bitShiftArguments(state, args)
			return bitResult(state, value>>count)
		}},
		{"arshift", func(state *lua.State, args []lua.Value) ([]lua.Value, error) {
			value, count := bitShiftArguments(state, args)
			return bitResult(state, uint32(int32(value)>>count))
		}},
		{"rol", func(state *lua.State, args []lua.Value) ([]lua.Value, error) {
			value, count := bitShiftArguments(state, args)
			return bitResult(state, value<<count|value>>(32-count))
		}},
		{"ror", func(state *lua.State, args []lua.Value) ([]lua.Value, error) {
			value, count := bitShiftArguments(state, args)
			return bitResult(state, value<<(32-count)|value>>count)
		}},
		{"bswap", func(state *lua.State, args []lua.Value) ([]lua.Value, error) {
			value := bitArgument(state, args, 0)
			return bitResult(state, value>>24|value>>8&0xff00|value&0xff00<<8|value<<24)
		}},
		{"tohex", bitToHex},
	}
	table := s.state.NewTable(0, len(functions))
	for _, entry := range functions {
		_ = table.RawSet(lua.Intern(entry.name), s.state.Function(entry.name, entry.function))
	}
	return table.Value()
}
