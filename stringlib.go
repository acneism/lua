package lua

import (
	"strings"
)

func relativePosition(position, length int) int {
	if position < 0 {
		position += length + 1
	}
	return max(position, 0)
}

func (s *State) openString() {
	table := s.library("string", []string{"byte", "char", "dump", "find", "format", "gfind", "gmatch", "gsub", "len", "lower", "match", "rep", "reverse", "sub", "upper"}, map[string]GoFunction{
		"len": func(s *State, args []Value) ([]Value, error) {
			return s.values1(Number(float64(len(s.checkText(args, 0))))), nil
		},
		"sub": func(s *State, args []Value) ([]Value, error) {
			text := s.checkText(args, 0)
			start := max(1, relativePosition(int(luaInteger(s.checkNumber(args, 1))), len(text)))
			end := min(len(text), relativePosition(s.optionalInt(args, 2, -1), len(text)))
			if start > end {
				return s.values1(String("")), nil
			}
			return s.stringResult(text[start-1 : end])
		},
		"byte": func(s *State, args []Value) ([]Value, error) {
			text := s.checkText(args, 0)
			start := relativePosition(s.optionalInt(args, 1, 1), len(text))
			end := min(len(text), relativePosition(s.optionalInt(args, 2, start), len(text)))
			start = max(start, 1)
			if start > end {
				return nil, nil
			}
			if end-start+1+len(args) > 8000 {
				s.raise("stack overflow (string slice too long)", 1)
			}
			values := make([]Value, end-start+1)
			for index := range values {
				s.tick()
				values[index] = Number(float64(text[start+index-1]))
			}
			return values, nil
		},
		"char": func(s *State, args []Value) ([]Value, error) {
			s.checkString(len(args))
			text := make([]byte, len(args))
			for index := range args {
				value := s.checkInt(args, index)
				if value < 0 || value > 255 {
					s.argError(index+1, "invalid value")
				}
				text[index] = byte(value)
			}
			return s.values1(String(string(text))), nil
		},
		"rep": func(s *State, args []Value) ([]Value, error) {
			text, count := s.checkText(args, 0), s.checkInt(args, 1)
			if count <= 0 || text == "" {
				return s.values1(String("")), nil
			}
			if count > s.options.MaxString/len(text) {
				s.raise("string length overflow", 1)
			}
			s.checkString(len(text) * count)
			var out strings.Builder
			block := text
			if len(text) < 16384 {
				block = strings.Repeat(text, min(count, 16384/len(text)))
			}
			remaining := len(text) * count
			for remaining > 0 {
				s.pollHook()
				offset := out.Len() % len(block)
				size := min(remaining, 16384, len(block)-offset)
				out.WriteString(block[offset : offset+size])
				remaining -= size
			}
			return s.values1(String(out.String())), nil
		},
		"reverse": func(s *State, args []Value) ([]Value, error) {
			text := []byte(s.checkText(args, 0))
			s.checkString(len(text))
			for left, right := 0, len(text)-1; left < right; left, right = left+1, right-1 {
				s.tick()
				text[left], text[right] = text[right], text[left]
			}
			return s.values1(String(string(text))), nil
		},
		"lower":  func(s *State, args []Value) ([]Value, error) { return stringCase(s, args, false) },
		"upper":  func(s *State, args []Value) ([]Value, error) { return stringCase(s, args, true) },
		"find":   func(s *State, args []Value) ([]Value, error) { return stringFind(s, args, true) },
		"match":  func(s *State, args []Value) ([]Value, error) { return stringFind(s, args, false) },
		"gmatch": stringGMatch, "gsub": stringGSub, "format": stringFormat,
		"dump": stringDump,
	})
	table.set(String("gfind"), table.RawGet(String("gmatch")))
	s.stringMeta.set(String("__index"), table.Value())
}

func stringCase(s *State, args []Value, upper bool) ([]Value, error) {
	text := []byte(s.checkText(args, 0))
	s.checkString(len(text))
	for index, value := range text {
		s.tick()
		if upper && value >= 'a' && value <= 'z' {
			text[index] -= 'a' - 'A'
		} else if !upper && value >= 'A' && value <= 'Z' {
			text[index] += 'a' - 'A'
		}
	}
	return []Value{String(string(text))}, nil
}

func stringDump(s *State, args []Value) ([]Value, error) {
	fn := s.checkFunction(args, 0).object.function
	if fn.proto == nil {
		s.raise("unable to dump given function", 1)
	}
	return s.stringResult(string(dumpRedis(fn.proto.canonical)))
}
