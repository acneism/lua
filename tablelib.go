package lua

import (
	"fmt"
	"strings"
)

func (s *State) tableWrite(table *Table, index int, value Value) {
	if err := table.RawSet(Number(float64(index)), value); err != nil {
		panic(s.processPanic(plainError(err.Error())))
	}
}

func tableRead(table *Table, index int) Value { return table.RawGet(Number(float64(index))) }

func (s *State) openTable() {
	s.library("table", map[string]GoFunction{
		"getn": func(s *State, args []Value) ([]Value, error) {
			return []Value{Number(float64(s.checkTable(args, 0).Len()))}, nil
		},
		"setn": func(s *State, args []Value) ([]Value, error) {
			s.checkTable(args, 0)
			s.raise("'setn' is obsolete", 1)
			return nil, nil
		},
		"maxn": func(s *State, args []Value) ([]Value, error) {
			table := s.checkTable(args, 0)
			result := 0.0
			for key, _, err := table.Next(Nil()); !key.IsNil(); key, _, err = table.Next(key) {
				if err != nil {
					panic(s.processPanic(plainError(err.Error())))
				}
				s.tick()
				if key.Type() == TypeNumber && key.number > result {
					result = key.number
				}
			}
			return []Value{Number(result)}, nil
		},
		"insert": tableInsert,
		"remove": tableRemove,
		"concat": tableConcat,
		"sort":   tableSort,
		"foreach": func(s *State, args []Value) ([]Value, error) {
			table, fn := s.checkTable(args, 0), s.checkFunction(args, 1)
			for key, value, err := table.Next(Nil()); !key.IsNil(); key, value, err = table.Next(key) {
				if err != nil {
					panic(s.processPanic(plainError(err.Error())))
				}
				result := s.invokeOne(fn, []Value{key, value})
				if !result.IsNil() {
					return []Value{result}, nil
				}
			}
			return nil, nil
		},
		"foreachi": func(s *State, args []Value) ([]Value, error) {
			table, fn := s.checkTable(args, 0), s.checkFunction(args, 1)
			length := table.Len()
			for index := 1; index <= length; index++ {
				result := s.invokeOne(fn, []Value{Number(float64(index)), tableRead(table, index)})
				if !result.IsNil() {
					return []Value{result}, nil
				}
			}
			return nil, nil
		},
	})
}

func tableInsert(s *State, args []Value) ([]Value, error) {
	table := s.checkTable(args, 0)
	end := table.Len() + 1
	position := end
	switch len(args) {
	case 2:
	case 3:
		position = s.checkInt(args, 1)
		end = max(end, position)
		for index := end; index > position; index-- {
			s.tick()
			s.tableWrite(table, index, tableRead(table, index-1))
		}
	default:
		s.raise("wrong number of arguments to 'insert'", 1)
	}
	s.tableWrite(table, position, args[len(args)-1])
	return nil, nil
}

func tableRemove(s *State, args []Value) ([]Value, error) {
	table := s.checkTable(args, 0)
	end := table.Len()
	position := s.optionalInt32(args, 1, end)
	if position < 1 || position > end {
		return nil, nil
	}
	result := tableRead(table, position)
	for ; position < end; position++ {
		s.tick()
		s.tableWrite(table, position, tableRead(table, position+1))
	}
	s.tableWrite(table, end, Nil())
	return []Value{result}, nil
}

func tableConcat(s *State, args []Value) ([]Value, error) {
	separator := s.optionalText(args, 1, "")
	table := s.checkTable(args, 0)
	start := s.optionalInt32(args, 2, 1)
	end := s.optionalInt32(args, 3, table.Len())
	var out strings.Builder
	for index := start; index <= end; index++ {
		s.tick()
		text, ok := tableRead(table, index).toString()
		if !ok {
			s.raise(fmt.Sprintf("invalid value (%s) at index %d in table for 'concat'", tableRead(table, index).Type(), index), 1)
		}
		if index != start {
			s.checkString(out.Len() + len(separator))
			out.WriteString(separator)
		}
		s.checkString(out.Len() + len(text))
		out.WriteString(text)
	}
	return []Value{String(out.String())}, nil
}

func tableSort(s *State, args []Value) ([]Value, error) {
	table := s.checkTable(args, 0)
	fn := argument(args, 1)
	if !fn.IsNil() {
		s.checkFunction(args, 1)
	}
	less := func(a, b Value) bool {
		s.tick()
		if fn.IsNil() {
			return s.less(a, b, false)
		}
		return s.invokeOne(fn, []Value{a, b}).Truth()
	}
	swap := func(a, b int) {
		left, right := tableRead(table, a), tableRead(table, b)
		s.tableWrite(table, a, right)
		s.tableWrite(table, b, left)
	}
	var sortRange func(int, int)
	sortRange = func(lower, upper int) {
		for lower < upper {
			if less(tableRead(table, upper), tableRead(table, lower)) {
				swap(lower, upper)
			}
			if upper-lower == 1 {
				break
			}
			middle := (lower + upper) / 2
			if less(tableRead(table, middle), tableRead(table, lower)) {
				swap(middle, lower)
			} else if less(tableRead(table, upper), tableRead(table, middle)) {
				swap(middle, upper)
			}
			if upper-lower == 2 {
				break
			}
			pivot := tableRead(table, middle)
			swap(middle, upper-1)
			i, j := lower, upper-1
			for {
				i++
				for less(tableRead(table, i), pivot) {
					if i > upper {
						s.raise("invalid order function for sorting", 1)
					}
					i++
				}
				j--
				for less(pivot, tableRead(table, j)) {
					if j < lower {
						s.raise("invalid order function for sorting", 1)
					}
					j--
				}
				if j < i {
					break
				}
				swap(i, j)
			}
			swap(upper-1, i)
			if i-lower < upper-i {
				sortRange(lower, i-1)
				lower = i + 1
			} else {
				sortRange(i+1, upper)
				upper = i - 1
			}
		}
	}
	sortRange(1, table.Len())
	return nil, nil
}
