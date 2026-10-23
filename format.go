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

func formatInteger(number float64, option byte, flags string, width, precision int) string {
	signed := option == 'd' || option == 'i'
	integer := luaInteger(number)
	value := uint64(integer)
	prefix := ""
	if signed {
		if integer < 0 {
			prefix = "-"
			value = 0 - uint64(integer)
		} else if strings.ContainsRune(flags, '+') {
			prefix = "+"
		} else if strings.ContainsRune(flags, ' ') {
			prefix = " "
		}
	} else if number >= 0x1p63 {
		value = uint64(luaInteger(number-0x1p63)) ^ (uint64(1) << 63)
	}
	base := 10
	if option == 'o' {
		base = 8
	} else if option == 'x' || option == 'X' {
		base = 16
	}
	digits := strconv.FormatUint(value, base)
	if precision == 0 && value == 0 {
		digits = ""
	}
	if precision > len(digits) {
		digits = strings.Repeat("0", precision-len(digits)) + digits
	}
	if strings.ContainsRune(flags, '#') {
		if option == 'o' && (len(digits) == 0 || digits[0] != '0') {
			prefix = "0"
		} else if base == 16 && value != 0 {
			prefix = "0x"
		}
	}
	if option == 'X' {
		prefix = strings.ToUpper(prefix)
		digits = strings.ToUpper(digits)
	}
	left := strings.ContainsRune(flags, '-')
	if strings.ContainsRune(flags, '0') && !left && precision < 0 && width > len(prefix)+len(digits) {
		digits = strings.Repeat("0", width-len(prefix)-len(digits)) + digits
	}
	return formatPadding(prefix+digits, width, left, false)
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
			add(formatInteger(number, option, flags, width, precision))
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
