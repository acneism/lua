package redis

import (
	"math"
	"strconv"
	"strings"
)

type ReplyType uint8

const (
	BulkReply ReplyType = iota
	StatusReply
	ErrorReply
	IntegerReply
	NullReply
	NullArrayReply
	ArrayReply
	MapReply
	SetReply
	DoubleReply
	BooleanReply
	BigNumberReply
	VerbatimReply
	AbortReply
)

type Pair struct{ Key, Value Reply }

type Reply struct {
	Type     ReplyType
	Str      string
	Int      int64
	Float    float64
	Bool     bool
	Elements []Reply
	Pairs    []Pair
	Format   string
	Err      error
}

func Bulk(text string) Reply      { return Reply{Type: BulkReply, Str: text} }
func Status(text string) Reply    { return Reply{Type: StatusReply, Str: text} }
func Error(text string) Reply     { return Reply{Type: ErrorReply, Str: text} }
func Integer(value int64) Reply   { return Reply{Type: IntegerReply, Int: value} }
func Null() Reply                 { return Reply{Type: NullReply} }
func NullArray() Reply            { return Reply{Type: NullArrayReply} }
func Array(values ...Reply) Reply { return Reply{Type: ArrayReply, Elements: values} }
func Map(pairs ...Pair) Reply     { return Reply{Type: MapReply, Pairs: pairs} }
func Set(values ...Reply) Reply   { return Reply{Type: SetReply, Elements: values} }
func Double(value float64) Reply  { return Reply{Type: DoubleReply, Float: value} }
func Boolean(value bool) Reply    { return Reply{Type: BooleanReply, Bool: value} }
func BigNumber(text string) Reply { return Reply{Type: BigNumberReply, Str: text} }
func Verbatim(format, text string) Reply {
	return Reply{Type: VerbatimReply, Format: format, Str: text}
}
func Abort(err error) Reply { return Reply{Type: AbortReply, Err: err} }

func doubleText(value float64) string {
	if math.IsInf(value, 1) {
		return "inf"
	}
	if math.IsInf(value, -1) {
		return "-inf"
	}
	if math.IsNaN(value) {
		return "nan"
	}
	return strconv.FormatFloat(value, 'g', -1, 64)
}

func appendLine(dst []byte, prefix byte, text string) []byte {
	dst = append(dst, prefix)
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r", " "), "\n", " ")
	dst = append(dst, text...)
	return append(dst, '\r', '\n')
}

func appendCount(dst []byte, prefix byte, count int64) []byte {
	dst = append(dst, prefix)
	dst = strconv.AppendInt(dst, count, 10)
	return append(dst, '\r', '\n')
}

func appendBulk(dst []byte, text string) []byte {
	dst = appendCount(dst, '$', int64(len(text)))
	dst = append(dst, text...)
	return append(dst, '\r', '\n')
}

func (r Reply) AppendRESP(dst []byte, proto int) []byte {
	return r.appendRESP(dst, proto, 0)
}

func (r Reply) appendRESP(dst []byte, proto, depth int) []byte {
	if depth > 128 {
		return appendLine(dst, '-', "ERR reply nesting exceeds limit")
	}
	switch r.Type {
	case StatusReply:
		return appendLine(dst, '+', r.Str)
	case ErrorReply:
		return appendLine(dst, '-', r.Str)
	case IntegerReply:
		return appendCount(dst, ':', r.Int)
	case NullReply, NullArrayReply:
		if proto == 3 {
			return append(dst, '_', '\r', '\n')
		}
		if r.Type == NullArrayReply {
			return appendCount(dst, '*', -1)
		}
		return appendCount(dst, '$', -1)
	case ArrayReply, SetReply:
		prefix := byte('*')
		if proto == 3 && r.Type == SetReply {
			prefix = '~'
		}
		dst = appendCount(dst, prefix, int64(len(r.Elements)))
		for _, value := range r.Elements {
			dst = value.appendRESP(dst, proto, depth+1)
		}
		return dst
	case MapReply:
		prefix, count := byte('*'), len(r.Pairs)*2
		if proto == 3 {
			prefix, count = '%', len(r.Pairs)
		}
		dst = appendCount(dst, prefix, int64(count))
		for _, pair := range r.Pairs {
			dst = pair.Key.appendRESP(dst, proto, depth+1)
			dst = pair.Value.appendRESP(dst, proto, depth+1)
		}
		return dst
	case DoubleReply:
		if proto == 3 {
			return appendLine(dst, ',', doubleText(r.Float))
		}
		return appendBulk(dst, doubleText(r.Float))
	case BooleanReply:
		if proto == 3 {
			if r.Bool {
				return append(dst, '#', 't', '\r', '\n')
			}
			return append(dst, '#', 'f', '\r', '\n')
		}
		if r.Bool {
			return appendCount(dst, ':', 1)
		}
		return appendCount(dst, ':', 0)
	case BigNumberReply:
		if proto == 3 {
			return appendLine(dst, '(', r.Str)
		}
		return appendBulk(dst, r.Str)
	case VerbatimReply:
		if proto == 3 {
			format := r.Format
			if len(format) != 3 {
				format = "txt"
			}
			dst = appendCount(dst, '=', int64(len(r.Str)+4))
			dst = append(dst, format...)
			dst = append(dst, ':')
			dst = append(dst, r.Str...)
			return append(dst, '\r', '\n')
		}
		return appendBulk(dst, r.Str)
	case AbortReply:
		return appendLine(dst, '-', "ERR execution aborted")
	default:
		return appendBulk(dst, r.Str)
	}
}
