package lua

import "strings"

type patternCapture struct{ start, length int }

type patternMatcher struct {
	state           *State
	source, pattern string
	level           int
	captures        [32]patternCapture
}

func (m *patternMatcher) fail(message string) { m.state.raise(message, 1) }

func (m *patternMatcher) char(index int) byte {
	if index >= len(m.pattern) {
		return 0
	}
	return m.pattern[index]
}

func (m *patternMatcher) classEnd(index int) int {
	switch m.char(index) {
	case '%':
		if m.char(index+1) == 0 {
			m.fail("malformed pattern (ends with '%')")
		}
		return index + 2
	case '[':
		index++
		if m.char(index) == '^' {
			index++
		}
		for {
			if m.char(index) == 0 {
				m.fail("malformed pattern (missing ']')")
			}
			if m.char(index) == '%' && m.char(index+1) != 0 {
				index++
			}
			index++
			if m.char(index) == ']' {
				return index + 1
			}
		}
	default:
		return index + 1
	}
}

func patternClass(value, class byte) bool {
	lower := class
	if lower >= 'A' && lower <= 'Z' {
		lower += 'a' - 'A'
	}
	var match bool
	switch lower {
	case 'a':
		match = value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
	case 'c':
		match = value < 32 || value == 127
	case 'd':
		match = isDigit(value)
	case 'l':
		match = value >= 'a' && value <= 'z'
	case 'p':
		match = value >= 33 && value <= 126 && !isAlpha(value) && !isDigit(value) || value == '_'
	case 's':
		match = value == ' ' || value >= '\t' && value <= '\r'
	case 'u':
		match = value >= 'A' && value <= 'Z'
	case 'w':
		match = isAlpha(value) && value != '_' || isDigit(value)
	case 'x':
		match = isDigit(value) || value >= 'a' && value <= 'f' || value >= 'A' && value <= 'F'
	case 'z':
		match = value == 0
	default:
		return value == class
	}
	if class >= 'A' && class <= 'Z' {
		return !match
	}
	return match
}

func (m *patternMatcher) bracket(value byte, start, end int) bool {
	positive := true
	index := start + 1
	if m.char(index) == '^' {
		positive = false
		index++
	}
	for index < end-1 {
		if m.char(index) == '%' {
			index++
			if patternClass(value, m.char(index)) {
				return positive
			}
		} else if m.char(index+1) == '-' && index+2 < end-1 {
			if m.char(index) <= value && value <= m.char(index+2) {
				return positive
			}
			index += 2
		} else if m.char(index) == value {
			return positive
		}
		index++
	}
	return !positive
}

func (m *patternMatcher) single(value byte, start, end int) bool {
	switch m.char(start) {
	case '.':
		return true
	case '%':
		return patternClass(value, m.char(start+1))
	case '[':
		return m.bracket(value, start, end)
	default:
		return value == m.char(start)
	}
}

func (m *patternMatcher) match(source, pattern int) int {
	for {
		m.state.tick()
		switch m.char(pattern) {
		case 0:
			return source
		case '(':
			if m.level >= len(m.captures) {
				m.fail("too many captures")
			}
			length, next := -1, pattern+1
			if m.char(next) == ')' {
				length, next = -2, next+1
			}
			level := m.level
			m.captures[level] = patternCapture{source, length}
			m.level++
			result := m.match(source, next)
			if result < 0 {
				m.level--
			}
			return result
		case ')':
			level := m.level - 1
			for level >= 0 && m.captures[level].length != -1 {
				level--
			}
			if level < 0 {
				m.fail("invalid pattern capture")
			}
			m.captures[level].length = source - m.captures[level].start
			result := m.match(source, pattern+1)
			if result < 0 {
				m.captures[level].length = -1
			}
			return result
		case '$':
			if m.char(pattern+1) == 0 {
				if source == len(m.source) {
					return source
				}
				return -1
			}
		case '%':
			switch m.char(pattern + 1) {
			case 'b':
				open, close := m.char(pattern+2), m.char(pattern+3)
				if open == 0 || close == 0 {
					m.fail("unbalanced pattern")
				}
				if source >= len(m.source) || m.source[source] != open {
					return -1
				}
				depth := 1
				source++
				for source < len(m.source) {
					m.state.tick()
					if m.source[source] == close {
						depth--
						if depth == 0 {
							break
						}
					} else if m.source[source] == open {
						depth++
					}
					source++
				}
				if depth != 0 {
					return -1
				}
				source++
				pattern += 4
				continue
			case 'f':
				pattern += 2
				if m.char(pattern) != '[' {
					m.fail("missing '[' after '%f' in pattern")
				}
				end := m.classEnd(pattern)
				previous, current := byte(0), byte(0)
				if source > 0 {
					previous = m.source[source-1]
				}
				if source < len(m.source) {
					current = m.source[source]
				}
				if m.bracket(previous, pattern, end) || !m.bracket(current, pattern, end) {
					return -1
				}
				pattern = end
				continue
			default:
				if isDigit(m.char(pattern + 1)) {
					index := int(m.char(pattern+1)) - '1'
					if index < 0 || index >= m.level || m.captures[index].length == -1 {
						m.fail("invalid capture index")
					}
					capture := m.captures[index]
					if capture.length < 0 || len(m.source)-source < capture.length || m.source[source:source+capture.length] != m.source[capture.start:capture.start+capture.length] {
						return -1
					}
					source += capture.length
					pattern += 2
					continue
				}
			}
		}
		end := m.classEnd(pattern)
		matched := source < len(m.source) && m.single(m.source[source], pattern, end)
		switch m.char(end) {
		case '?':
			if matched {
				if result := m.match(source+1, end+1); result >= 0 {
					return result
				}
			}
			pattern = end + 1
		case '*', '+':
			if m.char(end) == '+' {
				if !matched {
					return -1
				}
				source++
			}
			length := 0
			for source+length < len(m.source) && m.single(m.source[source+length], pattern, end) {
				m.state.tick()
				length++
			}
			for ; length >= 0; length-- {
				if result := m.match(source+length, end+1); result >= 0 {
					return result
				}
			}
			return -1
		case '-':
			for {
				if result := m.match(source, end+1); result >= 0 {
					return result
				}
				if source >= len(m.source) || !m.single(m.source[source], pattern, end) {
					return -1
				}
				source++
			}
		default:
			if !matched {
				return -1
			}
			source++
			pattern = end
		}
	}
}

func (m *patternMatcher) capture(index, start, end int) Value {
	if index >= m.level {
		if index == 0 {
			return String(m.source[start:end])
		}
		m.fail("invalid capture index")
	}
	capture := m.captures[index]
	switch capture.length {
	case -1:
		m.fail("unfinished capture")
	case -2:
		return Number(float64(capture.start + 1))
	}
	return String(m.source[capture.start : capture.start+capture.length])
}

func (m *patternMatcher) values(start, end int, whole bool) []Value {
	count := m.level
	if count == 0 && whole {
		count = 1
	}
	values := make([]Value, count)
	for index := range values {
		values[index] = m.capture(index, start, end)
	}
	return values
}

func stringFind(s *State, args []Value, find bool) ([]Value, error) {
	source, pattern := s.checkText(args, 0), s.checkText(args, 1)
	start := min(len(source), max(0, relativePosition(s.optionalInt(args, 2, 1), len(source))-1))
	if find && (argument(args, 3).Truth() || !strings.ContainsAny(cString(pattern), "^$*+?.([%-")) {
		index := strings.Index(source[start:], pattern)
		if index < 0 {
			return []Value{Nil()}, nil
		}
		index += start
		return []Value{Number(float64(index + 1)), Number(float64(index + len(pattern)))}, nil
	}
	m := patternMatcher{state: s, source: source, pattern: pattern}
	position, anchored := 0, false
	if m.char(0) == '^' {
		position, anchored = 1, true
	}
	for ; start <= len(source); start++ {
		m.level = 0
		if end := m.match(start, position); end >= 0 {
			if find {
				return append([]Value{Number(float64(start + 1)), Number(float64(end))}, m.values(start, end, false)...), nil
			}
			return m.values(start, end, true), nil
		}
		if anchored {
			break
		}
	}
	return []Value{Nil()}, nil
}

func stringGMatch(s *State, args []Value) ([]Value, error) {
	source, pattern := s.checkText(args, 0), s.checkText(args, 1)
	next := 0
	fn := s.Function("", func(s *State, _ []Value) ([]Value, error) {
		m := patternMatcher{state: s, source: source, pattern: pattern}
		for start := next; start <= len(source); start++ {
			m.level = 0
			if end := m.match(start, 0); end >= 0 {
				next = end
				if end == start {
					next++
				}
				return m.values(start, end, true), nil
			}
		}
		next = len(source) + 1
		return nil, nil
	})
	return []Value{fn}, nil
}

func stringGSub(s *State, args []Value) ([]Value, error) {
	source, pattern := s.checkText(args, 0), s.checkText(args, 1)
	replacement := argument(args, 2)
	limit := s.optionalInt32(args, 3, len(source)+1)
	if replacement.kind != TypeString && replacement.kind != TypeNumber && replacement.kind != TypeFunction && replacement.kind != TypeTable {
		s.argError(3, "string/function/table expected")
	}
	m := patternMatcher{state: s, source: source, pattern: pattern}
	position, anchored := 0, false
	if m.char(0) == '^' {
		position, anchored = 1, true
	}
	var out strings.Builder
	add := func(text string) {
		s.checkString(out.Len() + len(text))
		out.WriteString(text)
	}
	start, count := 0, 0
	for count < limit {
		s.tick()
		m.level = 0
		end := m.match(start, position)
		if end >= 0 {
			count++
			switch replacement.kind {
			case TypeNumber, TypeString:
				text, _ := replacement.toString()
				for index := 0; index < len(text); index++ {
					if text[index] != '%' {
						add(text[index : index+1])
						continue
					}
					index++
					if index == len(text) {
						add("\x00")
					} else if text[index] == '0' {
						add(source[start:end])
					} else if isDigit(text[index]) {
						add(m.capture(int(text[index]-'1'), start, end).String())
					} else {
						add(text[index : index+1])
					}
				}
			default:
				var value Value
				if replacement.kind == TypeFunction {
					value = first(s.invoke(replacement, m.values(start, end, true)))
				} else {
					value = s.get(replacement, m.capture(0, start, end), nameInfo{})
				}
				if !value.Truth() {
					add(source[start:end])
				} else if text, ok := value.toString(); ok {
					add(text)
				} else {
					s.raise("invalid replacement value (a "+value.kind.String()+")", 1)
				}
			}
		}
		if end > start {
			start = end
		} else if start < len(source) {
			add(source[start : start+1])
			start++
		} else {
			break
		}
		if anchored {
			break
		}
	}
	add(source[start:])
	return []Value{String(out.String()), Number(float64(count))}, nil
}
