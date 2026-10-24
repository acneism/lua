package lua

import (
	"math"
	"sort"
)

func luaInteger(number float64) int64 {
	if math.IsNaN(number) || number >= 0x1p63 || number < -0x1p63 {
		return math.MinInt64
	}
	return int64(number)
}

func (s *State) library(name string, functions map[string]GoFunction) *Table {
	table := s.NewTable(0, len(functions)+2)
	names := make([]string, 0, len(functions))
	for key := range functions {
		names = append(names, key)
	}
	sort.Strings(names)
	for _, key := range names {
		table.set(String(key), s.Function(key, functions[key]))
	}
	s.globals.set(String(name), table.Value())
	return table
}

func (s *State) checkInt(args []Value, index int) int {
	return int(int32(luaInteger(s.checkNumber(args, index))))
}

func (s *State) optionalInt32(args []Value, index, fallback int) int {
	if index >= len(args) || args[index].IsNil() {
		return fallback
	}
	return s.checkInt(args, index)
}

func (s *State) optionalText(args []Value, index int, fallback string) string {
	if index >= len(args) || args[index].IsNil() {
		return fallback
	}
	return s.checkText(args, index)
}

func (s *State) checkFunction(args []Value, index int) Value {
	value := argument(args, index)
	if value.Type() != TypeFunction {
		s.expected(args, index, "function")
	}
	return value
}

func (s *State) stringResult(text string) ([]Value, error) {
	s.checkString(len(text))
	return []Value{String(text)}, nil
}
