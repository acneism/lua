package redis

import (
	"math"

	"github.com/acneism/lua"
)

func bitArgument(state *lua.State, args []lua.Value, index int) uint32 {
	return uint32(math.Float64bits(state.CheckNumber(args, index) + 6755399441055744.0))
}

func bitResult(state *lua.State, value uint32) ([]lua.Value, error) {
	return state.Result(lua.Number(float64(int32(value)))), nil
}

func bitFold(operation func(a, b uint32) uint32) lua.GoFunction {
	return func(state *lua.State, args []lua.Value) ([]lua.Value, error) {
		value := bitArgument(state, args, 0)
		for index := len(args) - 1; index > 0; index-- {
			value = operation(value, bitArgument(state, args, index))
		}
		return bitResult(state, value)
	}
}

func bitShift(operation func(value, count uint32) uint32) lua.GoFunction {
	return func(state *lua.State, args []lua.Value) ([]lua.Value, error) {
		value := bitArgument(state, args, 0)
		return bitResult(state, operation(value, bitArgument(state, args, 1)&31))
	}
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
		{"band", bitFold(func(a, b uint32) uint32 { return a & b })},
		{"bor", bitFold(func(a, b uint32) uint32 { return a | b })},
		{"bxor", bitFold(func(a, b uint32) uint32 { return a ^ b })},
		{"lshift", bitShift(func(value, count uint32) uint32 { return value << count })},
		{"rshift", bitShift(func(value, count uint32) uint32 { return value >> count })},
		{"arshift", bitShift(func(value, count uint32) uint32 { return uint32(int32(value) >> count) })},
		{"rol", bitShift(func(value, count uint32) uint32 { return value<<count | value>>(32-count) })},
		{"ror", bitShift(func(value, count uint32) uint32 { return value<<(32-count) | value>>count })},
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
