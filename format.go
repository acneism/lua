package lua

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

func cString(text string) string {
	if index := strings.IndexByte(text, 0); index >= 0 {
		return text[:index]
	}
	return text
}

func quotedLua(text string) string {
	var out strings.Builder
	out.WriteByte('"')
	for index := 0; index < len(text); index++ {
		switch text[index] {
		case '"', '\\', '\n':
			out.WriteByte('\\')
			out.WriteByte(text[index])
		case '\r':
			out.WriteString("\\r")
		case 0:
			out.WriteString("\\000")
		default:
			out.WriteByte(text[index])
		}
	}
	out.WriteByte('"')
	return out.String()
}

func formatPadding(text string, width int, left bool, zero bool) string {
	count := width - len(text)
	if count <= 0 {
		return text
	}
	if left {
		return text + strings.Repeat(" ", count)
	}
	if zero {
		return strings.Repeat("0", count) + text
	}
	return strings.Repeat(" ", count) + text
}

func stringFormat(s *State, args []Value) ([]Value, error) {
	format := s.checkText(args, 0)
	var out strings.Builder
	add := func(text string) {
		s.checkString(out.Len() + len(text))
		out.WriteString(text)
	}
	argument := 0
	for index := 0; index < len(format); {
		s.tick()
		if format[index] != '%' {
			add(format[index : index+1])
			index++
			continue
		}
		index++
		if index < len(format) && format[index] == '%' {
			add("%")
			index++
			continue
		}
		argument++
		if argument >= len(args) {
			s.argError(argument+1, "no value")
		}
		flagsStart := index
		for index < len(format) && strings.ContainsRune("-+ #0", rune(format[index])) {
			index++
		}
		flags := format[flagsStart:index]
		if len(flags) >= 6 {
			s.raise("invalid format (repeated flags)", 1)
		}
		widthStart := index
		for count := 0; count < 2 && index < len(format) && isDigit(format[index]); count++ {
			index++
		}
		width := 0
		if index > widthStart {
			width, _ = strconv.Atoi(format[widthStart:index])
		}
		precision := -1
		if index < len(format) && format[index] == '.' {
			index++
			start := index
			for count := 0; count < 2 && index < len(format) && isDigit(format[index]); count++ {
				index++
			}
			precision = 0
			if index > start {
				precision, _ = strconv.Atoi(format[start:index])
			}
		}
		if index < len(format) && isDigit(format[index]) {
			s.raise("invalid format (width or precision too long)", 1)
		}
		option := byte(0)
		if index < len(format) {
			option = format[index]
			index++
		}
		left := strings.ContainsRune(flags, '-')
		switch option {
		case 'q':
			add(quotedLua(s.checkText(args, argument)))
		case 's':
			text := s.checkText(args, argument)
			if precision < 0 && len(text) >= 100 {
				add(text)
				continue
			}
			text = cString(text)
			if precision >= 0 && len(text) > precision {
				text = text[:precision]
			}
			add(formatPadding(text, width, left, false))
		case 'c':
			value := s.checkNumber(args, argument)
			converted := int32(math.MinInt32)
			if !math.IsNaN(value) && value < 0x1p31 && value >= -0x1p31 {
				converted = int32(value)
			}
			text := string([]byte{byte(converted)})
			add(cString(formatPadding(text, width, left, false)))
		case 'd', 'i', 'o', 'u', 'x', 'X':
			number := s.checkNumber(args, argument)
			var value any = luaInteger(number)
			verb := option
			if verb == 'i' {
				verb = 'd'
			}
			if option == 'u' || option == 'o' || option == 'x' || option == 'X' {
				unsigned := uint64(luaInteger(number))
				if number >= 0x1p63 {
					unsigned = uint64(luaInteger(number-0x1p63)) ^ (uint64(1) << 63)
				}
				value = unsigned
				flags = strings.ReplaceAll(strings.ReplaceAll(flags, "+", ""), " ", "")
				if option == 'u' {
					verb = 'd'
				}
			}
			zero := number == 0
			if unsigned, ok := value.(uint64); ok {
				zero = unsigned == 0
			}
			if (verb == 'x' || verb == 'X') && zero || verb == 'd' {
				flags = strings.ReplaceAll(flags, "#", "")
			}
			if verb == 'o' && zero && precision == 0 && strings.ContainsRune(flags, '#') {
				precision = 1
			}
			spec := "%" + flags
			if width > 0 {
				spec += strconv.Itoa(width)
			}
			if precision >= 0 {
				spec += "." + strconv.Itoa(precision)
			}
			spec += string(verb)
			add(fmt.Sprintf(spec, value))
		case 'e', 'E', 'f', 'g', 'G':
			number := s.checkNumber(args, argument)
			if math.IsInf(number, 0) || math.IsNaN(number) {
				text := "inf"
				if math.IsNaN(number) {
					text = "nan"
				}
				if option == 'E' || option == 'G' {
					text = strings.ToUpper(text)
				}
				if math.Signbit(number) {
					text = "-" + text
				} else if strings.ContainsRune(flags, '+') {
					text = "+" + text
				} else if strings.ContainsRune(flags, ' ') {
					text = " " + text
				}
				add(formatPadding(text, width, left, false))
				continue
			}
			if precision < 0 {
				precision = 6
			}
			if (option == 'g' || option == 'G') && precision == 0 {
				precision = 1
			}
			spec := "%" + flags
			if width > 0 {
				spec += strconv.Itoa(width)
			}
			spec += "." + strconv.Itoa(precision) + string(option)
			add(fmt.Sprintf(spec, number))
		default:
			s.raise("invalid option '%"+string([]byte{option})+"' to 'format'", 1)
		}
	}
	return []Value{String(out.String())}, nil
}
