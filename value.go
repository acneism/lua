package lua

import (
	"math"
	"strconv"
	"strings"
)

type Type uint8

const (
	TypeNil Type = iota
	TypeBoolean
	TypeNumber
	TypeString
	TypeTable
	TypeFunction
	TypeThread
	TypeUserdata
)

func (t Type) String() string {
	switch t {
	case TypeNil:
		return "nil"
	case TypeBoolean:
		return "boolean"
	case TypeNumber:
		return "number"
	case TypeString:
		return "string"
	case TypeTable:
		return "table"
	case TypeFunction:
		return "function"
	case TypeThread:
		return "thread"
	default:
		return "userdata"
	}
}

type Value struct {
	number float64
	object *object
	text   *string
}

type object struct {
	kind     Type
	id       uint64
	table    *Table
	function *function
	thread   *thread
	data     any
}

var numberMarker = object{kind: TypeNumber}
var booleanMarker = object{kind: TypeBoolean}

func Nil() Value { return Value{} }
func Bool(b bool) Value {
	if b {
		return Value{object: &booleanMarker, number: 1}
	}
	return Value{object: &booleanMarker}
}
func Number(n float64) Value { return Value{object: &numberMarker, number: n} }
func String(s string) Value  { return Value{text: &s} }
func (v Value) Type() Type {
	if v.object != nil {
		return v.object.kind
	}
	if v.text != nil {
		return TypeString
	}
	return TypeNil
}
func (v Value) Float() float64 { return v.number }
func (v Value) Text() string {
	if v.text != nil {
		return *v.text
	}
	return ""
}
func (v Value) IsNil() bool { return v.object == nil && v.text == nil }
func (v Value) Truth() bool {
	if v.object == nil {
		return v.text != nil
	}
	return v.object != &booleanMarker || v.number != 0
}
func (v Value) Table() *Table {
	if v.Type() == TypeTable {
		return v.object.table
	}
	return nil
}
func (v Value) Equal(other Value) bool {
	if v.text != nil || other.text != nil {
		return v.text != nil && other.text != nil && *v.text == *other.text
	}
	if v.object != other.object {
		return false
	}
	if v.object == &numberMarker || v.object == &booleanMarker {
		return v.number == other.number
	}
	return true
}
func (v Value) String() string {
	switch v.Type() {
	case TypeNil:
		return "nil"
	case TypeBoolean:
		if v.number != 0 {
			return "true"
		}
		return "false"
	case TypeNumber:
		return formatNumber(v.number)
	case TypeString:
		return *v.text
	default:
		return v.Type().String() + ": 0x" + strconv.FormatUint(v.object.id, 16)
	}
}

func formatNumber(n float64) string {
	if math.IsNaN(n) {
		if math.Signbit(n) {
			return "-nan"
		}
		return "nan"
	}
	if math.IsInf(n, 1) {
		return "inf"
	}
	if math.IsInf(n, -1) {
		return "-inf"
	}
	return strconv.FormatFloat(n, 'g', 14, 64)
}

func parseNumber(text string) (float64, bool) {
	text = strings.Trim(text, " \t\n\r\v\f")
	if text == "" || strings.ContainsRune(text, '\x00') {
		return 0, false
	}
	lower := strings.ToLower(text)
	sign := 1.0
	unsigned := lower
	if unsigned[0] == '-' || unsigned[0] == '+' {
		if unsigned[0] == '-' {
			sign = -1
		}
		unsigned = unsigned[1:]
	}
	if unsigned == "inf" || unsigned == "infinity" {
		return math.Inf(int(sign)), true
	}
	if unsigned == "nan" || (strings.HasPrefix(unsigned, "nan(") && strings.HasSuffix(unsigned, ")") && validNaNPayload(unsigned[4:len(unsigned)-1])) {
		return math.Copysign(math.NaN(), sign), true
	}
	if strings.ContainsRune(text, '_') {
		return 0, false
	}
	if strings.HasPrefix(unsigned, "0x") && !strings.ContainsAny(unsigned, "p") {
		text += "p0"
	}
	n, err := strconv.ParseFloat(text, 64)
	if err == nil {
		return n, true
	}
	if num, ok := err.(*strconv.NumError); ok && num.Err == strconv.ErrRange {
		return n, true
	}
	return 0, false
}

func validNaNPayload(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isAlpha(s[i]) && !isDigit(s[i]) {
			return false
		}
	}
	return true
}

func (v Value) toNumber() (float64, bool) {
	if v.Type() == TypeNumber {
		return v.number, true
	}
	if v.Type() == TypeString {
		return parseNumber(v.Text())
	}
	return 0, false
}

func (v Value) toString() (string, bool) {
	if v.Type() == TypeString {
		return v.Text(), true
	}
	if v.Type() == TypeNumber {
		return formatNumber(v.number), true
	}
	return "", false
}
