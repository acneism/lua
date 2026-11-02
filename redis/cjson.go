package redis

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/acneism/lua"
)

type cjsonConfig struct {
	sparseConvert  int
	sparseRatio    int32
	sparseSafe     int32
	encodeMaxDepth int32
	decodeMaxDepth int32
	encodeInvalid  int
	decodeInvalid  int
	keepBuffer     int
	precision      int32
}

func newCJSONConfig() *cjsonConfig {
	return &cjsonConfig{sparseRatio: 2, sparseSafe: 10, encodeMaxDepth: 1000, decodeMaxDepth: 1000, decodeInvalid: 1, keepBuffer: 1, precision: 14}
}

func cjsonArguments(state *lua.State, args []lua.Value, count int) []lua.Value {
	if len(args) > count {
		state.ArgError(count+1, "found too many arguments")
	}
	padded := make([]lua.Value, count)
	copy(padded, args)
	return padded
}

func cInt32(value float64) int32 {
	if value != value || value >= 0x1p31 || value <= -0x1p31-1 {
		return math.MinInt32
	}
	return int32(value)
}

func cjsonInteger(state *lua.State, args []lua.Value, index int, setting *int32, low, high int32) lua.Value {
	if !args[index].IsNil() {
		value := int32(cInt64(state.CheckNumber(args, index)))
		if value < low || value > high {
			state.ArgError(1, fmt.Sprintf("expected integer between %d and %d", low, high))
		}
		*setting = value
	}
	return lua.Number(float64(*setting))
}

func cjsonEnum(state *lua.State, args []lua.Value, index int, setting *int, options []string) lua.Value {
	if !args[index].IsNil() {
		if args[index].Type() == lua.TypeBoolean {
			*setting = 0
			if args[index].Truth() {
				*setting = 1
			}
		} else {
			name := state.CheckText(args, index)
			choice := -1
			for option, text := range options {
				if text == cstring(name) {
					choice = option
					break
				}
			}
			if choice < 0 {
				state.ArgError(index+1, "invalid option '"+cstring(name)+"'")
			}
			*setting = choice
		}
	}
	if *setting == 0 || *setting == 1 {
		return lua.Bool(*setting == 1)
	}
	return lua.String(options[*setting])
}

var cjsonBoolOptions = []string{"off", "on"}

var cjsonEscapes = func() [256]string {
	var table [256]string
	for index := range 32 {
		table[index] = fmt.Sprintf("\\u%04x", index)
	}
	table['\b'], table['\t'], table['\n'], table['\f'], table['\r'] = "\\b", "\\t", "\\n", "\\f", "\\r"
	table['"'], table['/'], table['\\'], table[127] = "\\\"", "\\/", "\\\\", "\\u007f"
	return table
}()

type cjsonEncoder struct {
	state *lua.State
	cfg   *cjsonConfig
	out   []byte
}

func (e *cjsonEncoder) fail(value lua.Value, reason string) {
	e.state.Raise("Cannot serialise " + value.Type().String() + ": " + reason)
}

func (e *cjsonEncoder) appendString(text string) {
	e.out = append(e.out, '"')
	start := 0
	for index := 0; index < len(text); index++ {
		if escape := cjsonEscapes[text[index]]; escape != "" {
			e.out = append(append(e.out, text[start:index]...), escape...)
			start = index + 1
		}
	}
	e.out = append(append(e.out, text[start:]...), '"')
}

func (e *cjsonEncoder) appendNumber(value lua.Value) {
	number := value.Float()
	switch e.cfg.encodeInvalid {
	case 0:
		if math.IsInf(number, 0) || number != number {
			e.fail(value, "must not be NaN or Inf")
		}
	case 1:
		if number != number {
			e.out = append(e.out, "nan"...)
			return
		}
	default:
		if math.IsInf(number, 0) || number != number {
			e.out = append(e.out, "null"...)
			return
		}
	}
	switch {
	case math.IsInf(number, 1):
		e.out = append(e.out, "inf"...)
	case math.IsInf(number, -1):
		e.out = append(e.out, "-inf"...)
	default:
		e.out = strconv.AppendFloat(e.out, number, 'g', int(e.cfg.precision), 64)
	}
}

func (e *cjsonEncoder) arrayLength(table *lua.Table, value lua.Value) int32 {
	highest, items := int32(0), int32(0)
	for key, _, err := table.Next(lua.Nil()); !key.IsNil() && err == nil; key, _, err = table.Next(key) {
		e.state.Tick()
		if key.Type() != lua.TypeNumber {
			return -1
		}
		index := key.Float()
		if index == 0 || index != index || math.Floor(index) != index || index < 1 {
			return -1
		}
		if index > float64(highest) {
			highest = cInt32(index)
		}
		items++
	}
	if e.cfg.sparseRatio > 0 && highest > items*e.cfg.sparseRatio && highest > e.cfg.sparseSafe {
		if e.cfg.sparseConvert == 0 {
			e.fail(value, "excessively sparse array")
		}
		return -1
	}
	return highest
}

func (e *cjsonEncoder) appendValue(value lua.Value, depth int32, slots int) {
	e.state.Tick()
	switch value.Type() {
	case lua.TypeString:
		e.appendString(value.Text())
	case lua.TypeNumber:
		e.appendNumber(value)
	case lua.TypeBoolean:
		e.out = strconv.AppendBool(e.out, value.Truth())
	case lua.TypeNil:
		e.out = append(e.out, "null"...)
	case lua.TypeTable:
		depth++
		if depth > e.cfg.encodeMaxDepth || slots+3 > luaMaxCStack {
			e.state.Raise(fmt.Sprintf("Cannot serialise, excessive nesting (%d)", depth))
		}
		table := value.Table()
		if length := e.arrayLength(table, value); length > 0 {
			e.out = append(e.out, '[')
			for index := int32(1); index <= length; index++ {
				if index > 1 {
					e.out = append(e.out, ',')
				}
				e.appendValue(table.RawGet(lua.Number(float64(index))), depth, slots+1)
			}
			e.out = append(e.out, ']')
			return
		}
		e.out = append(e.out, '{')
		first := true
		for key, item, err := table.Next(lua.Nil()); !key.IsNil() && err == nil; key, item, err = table.Next(key) {
			if !first {
				e.out = append(e.out, ',')
			}
			first = false
			switch key.Type() {
			case lua.TypeNumber:
				e.out = append(e.out, '"')
				e.appendNumber(key)
				e.out = append(e.out, '"', ':')
			case lua.TypeString:
				e.appendString(key.Text())
				e.out = append(e.out, ':')
			default:
				e.fail(key, "table key must be a number or string")
			}
			e.appendValue(item, depth, slots+2)
		}
		e.out = append(e.out, '}')
	default:
		if value == lua.NullUserdata() {
			e.out = append(e.out, "null"...)
			return
		}
		e.fail(value, "type not supported")
	}
}

type cjsonToken uint8

const (
	cjsonObjectBegin cjsonToken = iota
	cjsonObjectEnd
	cjsonArrayBegin
	cjsonArrayEnd
	cjsonString
	cjsonNumber
	cjsonBoolean
	cjsonNull
	cjsonColon
	cjsonComma
	cjsonEnd
	cjsonWhitespace
	cjsonError
	cjsonUnknown
)

var cjsonTokenNames = [...]string{"T_OBJ_BEGIN", "T_OBJ_END", "T_ARR_BEGIN", "T_ARR_END", "T_STRING", "T_NUMBER", "T_BOOLEAN", "T_NULL", "T_COLON", "T_COMMA", "T_END", "T_WHITESPACE", "T_ERROR", "T_UNKNOWN"}

var cjsonTokens = func() [256]cjsonToken {
	var table [256]cjsonToken
	for index := range table {
		table[index] = cjsonError
	}
	table['{'], table['}'], table['['], table[']'] = cjsonObjectBegin, cjsonObjectEnd, cjsonArrayBegin, cjsonArrayEnd
	table[','], table[':'], table[0] = cjsonComma, cjsonColon, cjsonEnd
	table[' '], table['\t'], table['\n'], table['\r'] = cjsonWhitespace, cjsonWhitespace, cjsonWhitespace, cjsonWhitespace
	for _, c := range "fiInNt\"+-0123456789" {
		table[c] = cjsonUnknown
	}
	return table
}()

var cjsonUnescape = func() [256]byte {
	var table [256]byte
	table['"'], table['\\'], table['/'] = '"', '\\', '/'
	table['b'], table['t'], table['n'], table['f'], table['r'], table['u'] = '\b', '\t', '\n', '\f', '\r', 'u'
	return table
}()

type cjsonParsed struct {
	kind    cjsonToken
	index   int
	text    string
	number  float64
	boolean bool
}

type cjsonDecoder struct {
	state *lua.State
	cfg   *cjsonConfig
	data  string
	at    int
	depth int32
	tmp   []byte
}

func (d *cjsonDecoder) peek(index int) byte {
	if index < len(d.data) {
		return d.data[index]
	}
	return 0
}

func (d *cjsonDecoder) errorToken(token *cjsonParsed, message string) {
	token.kind, token.index, token.text = cjsonError, d.at, message
}

func hexDigit(c byte) int {
	if c >= '0' && c <= '9' {
		return int(c - '0')
	}
	c |= 0x20
	if c >= 'a' && c <= 'f' {
		return 10 + int(c-'a')
	}
	return -1
}

func (d *cjsonDecoder) hex4(index int) int {
	value := 0
	for offset := range 4 {
		digit := hexDigit(d.peek(index + offset))
		if digit < 0 {
			return -1
		}
		value = value<<4 + digit
	}
	return value
}

func appendCodepoint(dst []byte, codepoint int) []byte {
	switch {
	case codepoint <= 0x7f:
		return append(dst, byte(codepoint))
	case codepoint <= 0x7ff:
		return append(dst, byte(codepoint>>6)|0xc0, byte(codepoint&0x3f)|0x80)
	case codepoint <= 0xffff:
		return append(dst, byte(codepoint>>12)|0xe0, byte(codepoint>>6&0x3f)|0x80, byte(codepoint&0x3f)|0x80)
	}
	return append(dst, byte(codepoint>>18)|0xf0, byte(codepoint>>12&0x3f)|0x80, byte(codepoint>>6&0x3f)|0x80, byte(codepoint&0x3f)|0x80)
}

func (d *cjsonDecoder) unicodeEscape() bool {
	codepoint := d.hex4(d.at + 2)
	if codepoint < 0 {
		return false
	}
	length := 6
	if codepoint&0xf800 == 0xd800 {
		if codepoint&0x400 != 0 || d.peek(d.at+6) != '\\' || d.peek(d.at+7) != 'u' {
			return false
		}
		low := d.hex4(d.at + 8)
		if low < 0 || low&0xfc00 != 0xdc00 {
			return false
		}
		codepoint = ((codepoint&0x3ff)<<10 | low&0x3ff) + 0x10000
		length = 12
	}
	d.tmp = appendCodepoint(d.tmp, codepoint)
	d.at += length
	return true
}

func (d *cjsonDecoder) stringToken(token *cjsonParsed) {
	d.at++
	d.tmp = d.tmp[:0]
	for {
		c := d.peek(d.at)
		if c == '"' {
			break
		}
		if c == 0 {
			d.errorToken(token, "unexpected end of string")
			return
		}
		if c == '\\' {
			c = cjsonUnescape[d.peek(d.at+1)]
			if c == 'u' {
				if d.unicodeEscape() {
					continue
				}
				d.errorToken(token, "invalid unicode escape code")
				return
			}
			if c == 0 {
				d.errorToken(token, "invalid escape code")
				return
			}
			d.at++
		}
		d.tmp = append(d.tmp, c)
		d.at++
	}
	d.at++
	d.state.Reserve(int64(len(d.tmp)))
	token.kind, token.text = cjsonString, string(d.tmp)
}

func hasFoldedPrefix(text, prefix string) bool {
	return len(text) >= len(prefix) && strings.EqualFold(text[:len(prefix)], prefix)
}

func (d *cjsonDecoder) invalidNumber() bool {
	at := d.at
	if d.peek(at) == '+' {
		return true
	}
	if d.peek(at) == '-' {
		at++
	}
	c := d.peek(at)
	if c == '0' {
		next := d.peek(at + 1)
		return next|0x20 == 'x' || next >= '0' && next <= '9'
	}
	if int8(c) <= '9' {
		return false
	}
	return hasFoldedPrefix(d.data[at:], "inf") || hasFoldedPrefix(d.data[at:], "nan")
}

func (d *cjsonDecoder) numberToken(token *cjsonParsed) {
	token.kind = cjsonNumber
	number, end := cStrtod(d.data[d.at:])
	if end == 0 {
		d.errorToken(token, "invalid number")
		return
	}
	token.number = number
	d.at += end
}

func cStrtod(text string) (float64, int) {
	at := 0
	for at < len(text) && (text[at] == ' ' || text[at] >= '\t' && text[at] <= '\r') {
		at++
	}
	negative := false
	if at < len(text) && (text[at] == '-' || text[at] == '+') {
		negative = text[at] == '-'
		at++
	}
	rest := text[at:]
	signed := func(value float64) float64 {
		if negative {
			return math.Copysign(value, -1)
		}
		return value
	}
	switch {
	case hasFoldedPrefix(rest, "infinity"):
		return signed(math.Inf(1)), at + 8
	case hasFoldedPrefix(rest, "inf"):
		return signed(math.Inf(1)), at + 3
	case hasFoldedPrefix(rest, "nan"):
		bits := uint64(0x7ff8000000000000)
		end := at + 3
		if len(rest) > 3 && rest[3] == '(' {
			close := 4
			for close < len(rest) && (isAlphanumeric(rest[close]) || rest[close] == '_') {
				close++
			}
			if close < len(rest) && rest[close] == ')' {
				end = at + close + 1
				if !strings.Contains(rest[4:close], "_") {
					if payload, err := strconv.ParseUint(rest[4:close], 0, 64); err == nil && payload&(1<<51-1) != 0 {
						bits |= payload & (1<<51 - 1)
					} else if err != nil && strings.Contains(err.Error(), "out of range") {
						bits |= 1<<51 - 1
					}
				}
			}
		}
		return signed(math.Float64frombits(bits)), end
	}
	hexadecimal := len(rest) > 2 && rest[0] == '0' && rest[1]|0x20 == 'x'
	digits := func(index int) (int, int) {
		count := 0
		for index < len(rest) {
			c := rest[index]
			if c >= '0' && c <= '9' || hexadecimal && hexDigit(c) >= 0 {
				index++
				count++
				continue
			}
			break
		}
		return index, count
	}
	start := 0
	if hexadecimal {
		start = 2
	}
	index, whole := digits(start)
	fraction := 0
	if index < len(rest) && rest[index] == '.' {
		var next int
		next, fraction = digits(index + 1)
		if whole > 0 || fraction > 0 {
			index = next
		}
	}
	if whole == 0 && fraction == 0 {
		if hexadecimal {
			return signed(0), at + 1
		}
		return 0, 0
	}
	exponentMark := byte('e')
	if hexadecimal {
		exponentMark = 'p'
	}
	literal := rest[:index]
	if index < len(rest) && rest[index]|0x20 == exponentMark {
		next := index + 1
		if next < len(rest) && (rest[next] == '+' || rest[next] == '-') {
			next++
		}
		end := next
		for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
			end++
		}
		if end > next {
			index = end
			literal = rest[:index]
		}
	}
	if hexadecimal && !strings.ContainsAny(literal, "pP") {
		literal += "p0"
	}
	value, err := strconv.ParseFloat(literal, 64)
	if err != nil && !strings.Contains(err.Error(), "out of range") {
		return 0, 0
	}
	return signed(value), at + index
}

func (d *cjsonDecoder) next(token *cjsonParsed) {
	d.state.Tick()
	var c byte
	for {
		c = d.peek(d.at)
		token.kind = cjsonTokens[c]
		if token.kind != cjsonWhitespace {
			break
		}
		d.at++
	}
	token.index = d.at
	switch token.kind {
	case cjsonError:
		d.errorToken(token, "invalid token")
		return
	case cjsonEnd:
		return
	case cjsonUnknown:
	default:
		d.at++
		return
	}
	rest := d.data[d.at:]
	switch {
	case c == '"':
		d.stringToken(token)
	case c == '-' || c >= '0' && c <= '9':
		if d.cfg.decodeInvalid == 0 && d.invalidNumber() {
			d.errorToken(token, "invalid number")
			return
		}
		d.numberToken(token)
	case strings.HasPrefix(rest, "true"):
		token.kind, token.boolean = cjsonBoolean, true
		d.at += 4
	case strings.HasPrefix(rest, "false"):
		token.kind, token.boolean = cjsonBoolean, false
		d.at += 5
	case strings.HasPrefix(rest, "null"):
		token.kind = cjsonNull
		d.at += 4
	case d.cfg.decodeInvalid != 0 && d.invalidNumber():
		d.numberToken(token)
	default:
		d.errorToken(token, "invalid token")
	}
}

func (d *cjsonDecoder) fail(expected string, token *cjsonParsed) {
	found := cjsonTokenNames[token.kind]
	if token.kind == cjsonError {
		found = token.text
	}
	d.state.Raise(fmt.Sprintf("Expected %s but found %s at character %d", expected, found, token.index+1))
}

func (d *cjsonDecoder) descend(slots, need int) {
	d.depth++
	if d.depth <= d.cfg.decodeMaxDepth && slots+need <= luaMaxCStack {
		return
	}
	d.state.Raise(fmt.Sprintf("Found too many nested data structures (%d) at character %d", d.depth, d.at))
}

func (d *cjsonDecoder) object(slots int) lua.Value {
	d.descend(slots, 3)
	table := d.state.NewTable(0, 0)
	var token cjsonParsed
	d.next(&token)
	if token.kind == cjsonObjectEnd {
		d.depth--
		return table.Value()
	}
	for {
		if token.kind != cjsonString {
			d.fail("object key string", &token)
		}
		key := lua.String(token.text)
		d.next(&token)
		if token.kind != cjsonColon {
			d.fail("colon", &token)
		}
		d.next(&token)
		value := d.value(&token, slots+2)
		_ = table.RawSet(key, value)
		d.next(&token)
		if token.kind == cjsonObjectEnd {
			d.depth--
			return table.Value()
		}
		if token.kind != cjsonComma {
			d.fail("comma or object end", &token)
		}
		d.next(&token)
	}
}

func (d *cjsonDecoder) array(slots int) lua.Value {
	d.descend(slots, 2)
	table := d.state.NewTable(0, 0)
	var token cjsonParsed
	d.next(&token)
	if token.kind == cjsonArrayEnd {
		d.depth--
		return table.Value()
	}
	for index := 1; ; index++ {
		value := d.value(&token, slots+1)
		_ = table.RawSet(lua.Number(float64(index)), value)
		d.next(&token)
		if token.kind == cjsonArrayEnd {
			d.depth--
			return table.Value()
		}
		if token.kind != cjsonComma {
			d.fail("comma or array end", &token)
		}
		d.next(&token)
	}
}

func (d *cjsonDecoder) value(token *cjsonParsed, slots int) lua.Value {
	switch token.kind {
	case cjsonString:
		return lua.String(token.text)
	case cjsonNumber:
		return lua.Number(token.number)
	case cjsonBoolean:
		return lua.Bool(token.boolean)
	case cjsonObjectBegin:
		return d.object(slots)
	case cjsonArrayBegin:
		return d.array(slots)
	case cjsonNull:
		return lua.NullUserdata()
	}
	d.fail("value", token)
	return lua.Nil()
}

func (s *session) openCJSON() lua.Value {
	cfg := newCJSONConfig()
	functions := []struct {
		name     string
		function lua.GoFunction
	}{
		{"encode", func(state *lua.State, args []lua.Value) ([]lua.Value, error) {
			if len(args) != 1 {
				state.ArgError(1, "expected 1 argument")
			}
			encoder := cjsonEncoder{state: state, cfg: cfg}
			encoder.appendValue(args[0], 0, 1)
			state.Reserve(int64(len(encoder.out)))
			return state.Result(lua.String(string(encoder.out))), nil
		}},
		{"decode", func(state *lua.State, args []lua.Value) ([]lua.Value, error) {
			if len(args) != 1 {
				state.ArgError(1, "expected 1 argument")
			}
			text := state.CheckText(args, 0)
			if len(text) >= 2 && (text[0] == 0 || text[1] == 0) {
				state.Raise("JSON parser does not support UTF-16 or UTF-32")
			}
			decoder := cjsonDecoder{state: state, cfg: cfg, data: text}
			var token cjsonParsed
			decoder.next(&token)
			value := decoder.value(&token, 1)
			decoder.next(&token)
			if token.kind != cjsonEnd {
				decoder.fail("the end", &token)
			}
			return state.Result(value), nil
		}},
		{"encode_sparse_array", func(state *lua.State, args []lua.Value) ([]lua.Value, error) {
			args = cjsonArguments(state, args, 3)
			convert := cjsonEnum(state, args, 0, &cfg.sparseConvert, cjsonBoolOptions)
			ratio := cjsonInteger(state, args, 1, &cfg.sparseRatio, 0, math.MaxInt32)
			safe := cjsonInteger(state, args, 2, &cfg.sparseSafe, 0, math.MaxInt32)
			return []lua.Value{convert, ratio, safe}, nil
		}},
		{"encode_max_depth", func(state *lua.State, args []lua.Value) ([]lua.Value, error) {
			return state.Result(cjsonInteger(state, cjsonArguments(state, args, 1), 0, &cfg.encodeMaxDepth, 1, math.MaxInt32)), nil
		}},
		{"decode_max_depth", func(state *lua.State, args []lua.Value) ([]lua.Value, error) {
			return state.Result(cjsonInteger(state, cjsonArguments(state, args, 1), 0, &cfg.decodeMaxDepth, 1, math.MaxInt32)), nil
		}},
		{"encode_number_precision", func(state *lua.State, args []lua.Value) ([]lua.Value, error) {
			return state.Result(cjsonInteger(state, cjsonArguments(state, args, 1), 0, &cfg.precision, 1, 14)), nil
		}},
		{"encode_keep_buffer", func(state *lua.State, args []lua.Value) ([]lua.Value, error) {
			return state.Result(cjsonEnum(state, cjsonArguments(state, args, 1), 0, &cfg.keepBuffer, cjsonBoolOptions)), nil
		}},
		{"encode_invalid_numbers", func(state *lua.State, args []lua.Value) ([]lua.Value, error) {
			return state.Result(cjsonEnum(state, cjsonArguments(state, args, 1), 0, &cfg.encodeInvalid, []string{"off", "on", "null"})), nil
		}},
		{"decode_invalid_numbers", func(state *lua.State, args []lua.Value) ([]lua.Value, error) {
			return state.Result(cjsonEnum(state, cjsonArguments(state, args, 1), 0, &cfg.decodeInvalid, cjsonBoolOptions)), nil
		}},
		{"new", func(state *lua.State, _ []lua.Value) ([]lua.Value, error) {
			return state.Result(s.openCJSON()), nil
		}},
	}
	table := s.state.NewTable(0, 0)
	for _, entry := range functions {
		_ = table.RawSet(lua.Intern(entry.name), s.state.Function(entry.name, entry.function))
	}
	_ = table.RawSet(lua.Intern("null"), lua.NullUserdata())
	_ = table.RawSet(lua.Intern("_NAME"), lua.String("cjson"))
	_ = table.RawSet(lua.Intern("_VERSION"), lua.String("2.1.0"))
	return table.Value()
}
