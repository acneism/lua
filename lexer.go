package lua

import (
	"fmt"
	"strings"
)

type token struct {
	kind    string
	text    string
	value   Value
	line    int
	endLine int
}

type lexer struct {
	source     string
	chunk      string
	pos        int
	line       int
	errorToken *token
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isAlpha(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' }

func (l *lexer) current() int {
	if l.pos >= len(l.source) {
		return -1
	}
	return int(l.source[l.pos])
}

func (l *lexer) checkNext(set string) bool {
	c := l.current()
	if c < 0 || c != 0 && !strings.ContainsRune(set, rune(c)) {
		return false
	}
	l.pos++
	return true
}

func (l *lexer) newline() {
	old := l.current()
	l.pos++
	if c := l.current(); c != old && (c == '\r' || c == '\n') {
		l.pos++
	}
	l.line++
	if l.line >= redisMaxInt {
		l.syntaxError("chunk has too many lines", l.errorToken)
	}
}

func (l *lexer) syntaxError(message string, current *token) {
	if current != nil && current.kind != "" && current.kind != "\x00" {
		message += " near '" + lexerTokenText(current.text) + "'"
	}
	panic(syntaxError(l.chunk, l.line, message))
}

func (l *lexer) fail(message, near string) {
	panic(syntaxError(l.chunk, l.line, message+" near '"+lexerTokenText(near)+"'"))
}

func lexerTokenText(text string) string {
	if zero := strings.IndexByte(text, 0); zero >= 0 {
		return text[:zero]
	}
	return text
}

func (l *lexer) delimiter(pos int, bracket byte) (int, int) {
	start := pos
	if pos >= len(l.source) || l.source[pos] != bracket {
		return -1, pos
	}
	pos++
	for pos < len(l.source) && l.source[pos] == '=' {
		pos++
	}
	if pos < len(l.source) && l.source[pos] == bracket {
		return pos - start - 1, pos + 1
	}
	return -1, pos
}

func (l *lexer) longString(level, end int, comment bool) string {
	l.pos = end
	if l.current() == '\n' || l.current() == '\r' {
		l.newline()
	}
	var out strings.Builder
	for {
		switch c := l.current(); c {
		case -1:
			name := "string"
			if comment {
				name = "comment"
			}
			l.fail("unfinished long "+name, "<eof>")
		case ']':
			count, next := l.delimiter(l.pos, ']')
			if count == level {
				l.pos = next
				return out.String()
			}
			if !comment {
				out.WriteByte(']')
			}
			l.pos++
		case '\n', '\r':
			if !comment {
				out.WriteByte('\n')
			}
			l.newline()
		default:
			if !comment {
				out.WriteByte(byte(c))
			}
			l.pos++
		}
	}
}

func (l *lexer) quoted() token {
	line, quote := l.line, l.current()
	l.pos++
	var out strings.Builder
	for {
		c := l.current()
		if c == quote {
			l.pos++
			return token{kind: "<string>", text: string(byte(quote)) + out.String() + string(byte(quote)), value: String(out.String()), line: line, endLine: l.line}
		}
		if c < 0 {
			l.fail("unfinished string", "<eof>")
		}
		if c == '\n' || c == '\r' {
			l.fail("unfinished string", string(byte(quote))+out.String())
		}
		l.pos++
		if c != '\\' {
			out.WriteByte(byte(c))
			continue
		}
		c = l.current()
		switch c {
		case -1:
			l.fail("unfinished string", "<eof>")
		case '\n', '\r':
			out.WriteByte('\n')
			l.newline()
		case 'a':
			out.WriteByte('\a')
			l.pos++
		case 'b':
			out.WriteByte('\b')
			l.pos++
		case 'f':
			out.WriteByte('\f')
			l.pos++
		case 'n':
			out.WriteByte('\n')
			l.pos++
		case 'r':
			out.WriteByte('\r')
			l.pos++
		case 't':
			out.WriteByte('\t')
			l.pos++
		case 'v':
			out.WriteByte('\v')
			l.pos++
		default:
			if isDigit(byte(c)) {
				n := 0
				for i := 0; i < 3 && l.current() >= '0' && l.current() <= '9'; i++ {
					n = n*10 + l.current() - '0'
					l.pos++
				}
				if n > 255 {
					l.fail("escape sequence too large", string(byte(quote))+out.String())
				}
				out.WriteByte(byte(n))
			} else {
				out.WriteByte(byte(c))
				l.pos++
			}
		}
	}
}

func (l *lexer) numeral() token {
	start, line := l.pos, l.line
	for l.current() == '.' || l.current() >= '0' && l.current() <= '9' {
		l.pos++
	}
	if l.checkNext("Ee") {
		l.checkNext("+-")
	}
	for c := l.current(); c >= 0 && (isAlpha(byte(c)) || isDigit(byte(c))); c = l.current() {
		l.pos++
	}
	text := l.source[start:l.pos]
	n, ok := parseNumber(lexerTokenText(text))
	if !ok {
		l.fail("malformed number", text)
	}
	return token{kind: "<number>", text: text, value: Number(n), line: line}
}

func (l *lexer) next() token {
	for {
		c := l.current()
		switch c {
		case -1:
			return token{kind: "<eof>", text: "<eof>", line: l.line}
		case ' ', '\t', '\v', '\f':
			l.pos++
			continue
		case '\r', '\n':
			l.newline()
			continue
		case '-':
			if l.pos+1 < len(l.source) && l.source[l.pos+1] == '-' {
				l.pos += 2
				if level, end := l.delimiter(l.pos, '['); level >= 0 {
					l.longString(level, end, true)
				} else {
					for l.current() >= 0 && l.current() != '\r' && l.current() != '\n' {
						l.pos++
					}
				}
				continue
			}
		case '"', '\'':
			return l.quoted()
		case '[':
			if level, end := l.delimiter(l.pos, '['); level >= 0 {
				line := l.line
				value := l.longString(level, end, false)
				delimiter := strings.Repeat("=", level)
				return token{kind: "<string>", text: "[" + delimiter + "[" + value + "]" + delimiter + "]", value: String(value), line: line, endLine: l.line}
			} else if end > l.pos+1 {
				l.fail("invalid long string delimiter", l.source[l.pos:end])
			}
		}
		if isDigit(byte(c)) || c == '.' && l.pos+1 < len(l.source) && isDigit(l.source[l.pos+1]) {
			return l.numeral()
		}
		if isAlpha(byte(c)) {
			start := l.pos
			for l.current() >= 0 && (isAlpha(byte(l.current())) || isDigit(byte(l.current()))) {
				l.pos++
			}
			text := l.source[start:l.pos]
			kind := "<name>"
			switch text {
			case "and", "break", "do", "else", "elseif", "end", "false", "for", "function", "if", "in", "local", "nil", "not", "or", "repeat", "return", "then", "true", "until", "while":
				kind = text
			}
			return token{kind: kind, text: text, line: l.line}
		}
		if c == '.' {
			l.pos++
			text := "."
			if l.checkNext(".") {
				text = ".."
				if l.checkNext(".") {
					text = "..."
				}
			}
			return token{kind: text, text: text, line: l.line}
		}
		line := l.line
		l.pos++
		text := l.source[l.pos-1 : l.pos]
		if l.pos < len(l.source) {
			pair := text + string(l.source[l.pos])
			switch pair {
			case "==", "~=", "<=", ">=":
				text = pair
				l.pos++
			}
		}
		if c < 32 || c == 127 {
			return token{kind: text, text: fmt.Sprintf("char(%d)", c), line: line}
		}
		return token{kind: text, text: text, line: line}
	}
}
