package redis

import (
	"encoding/binary"
	"fmt"
	"math"
	"strings"

	"github.com/acneism/lua"
)

const (
	structMaxIntSize = 32
	structMaxAlign   = 8
	luaMaxCStack     = 8000
)

type structHeader struct {
	big   bool
	align int
}

type structFormat struct {
	state *lua.State
	text  string
	at    int
}

func (f *structFormat) next() (byte, bool) {
	if f.at == len(f.text) {
		return 0, false
	}
	f.at++
	return f.text[f.at-1], true
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func (f *structFormat) number(fallback int) int {
	if f.at == len(f.text) || !isDigit(f.text[f.at]) {
		return fallback
	}
	value := int32(0)
	for f.at < len(f.text) && isDigit(f.text[f.at]) {
		digit := int32(f.text[f.at] - '0')
		if value > math.MaxInt32/10 || value*10 > math.MaxInt32-digit {
			f.state.Raise("integral size overflow")
		}
		value = value*10 + digit
		f.at++
	}
	return int(value)
}

func (f *structFormat) size(option byte) uint64 {
	switch option {
	case 'B', 'b':
		return 1
	case 'H', 'h':
		return 2
	case 'L', 'l', 'T', 'd':
		return 8
	case 'f':
		return 4
	case 'x':
		return 1
	case 'c':
		return uint64(f.number(1))
	case 'i', 'I':
		size := f.number(4)
		if size > structMaxIntSize {
			f.state.Raise(fmt.Sprintf("integral size %d is larger than limit of %d", size, structMaxIntSize))
		}
		return uint64(size)
	}
	return 0
}

func structPadding(position uint64, header *structHeader, option byte, size uint64) uint64 {
	if size == 0 || option == 'c' {
		return 0
	}
	if size > uint64(header.align) {
		size = uint64(header.align)
	}
	return (size - position&(size-1)) & (size - 1)
}

func (f *structFormat) control(option byte, header *structHeader) {
	switch option {
	case ' ':
	case '>':
		header.big = true
	case '<':
		header.big = false
	case '!':
		align := f.number(structMaxAlign)
		if align <= 0 || align&(align-1) != 0 {
			f.state.Raise(fmt.Sprintf("alignment %d is not a power of 2", align))
		}
		header.align = align
	default:
		f.state.ArgError(1, "invalid format option '"+string([]byte{option})+"'")
	}
}

func cInt64(value float64) int64 {
	if value != value || value >= 0x1p63 || value < -0x1p63 {
		return math.MinInt64
	}
	return int64(value)
}

func cUint64(value float64) uint64 {
	if value >= 0x1p63 {
		return uint64(cInt64(value-0x1p63)) ^ 1<<63
	}
	return uint64(cInt64(value))
}

func structInteger(value uint64, big bool, size uint64) []byte {
	buffer := make([]byte, size)
	for index := range buffer {
		slot := index
		if big {
			slot = int(size) - 1 - index
		}
		buffer[slot] = byte(value)
		value >>= 8
	}
	return buffer
}

type luaBuffer struct {
	stack   []lua.Value
	base    int
	levels  []string
	pending []byte
}

func newLuaBuffer(args []lua.Value) *luaBuffer {
	stack := append(append([]lua.Value(nil), args...), lua.Nil())
	return &luaBuffer{stack: stack, base: len(stack)}
}

func (b *luaBuffer) flush() {
	b.levels = append(b.levels, string(b.pending))
	b.pending = b.pending[:0]
	if count := len(b.levels); count > 1 {
		taken := 1
		top := len(b.levels[count-1])
		for {
			size := len(b.levels[count-taken-1])
			if count-taken+1 < 10 && top <= size {
				break
			}
			top += size
			taken++
			if taken >= count {
				break
			}
		}
		b.levels = append(b.levels[:count-taken], strings.Join(b.levels[count-taken:], ""))
	}
	b.stack = b.stack[:b.base]
	for _, level := range b.levels {
		b.stack = append(b.stack, lua.String(level))
	}
}

func (b *luaBuffer) add(text string) {
	for len(text) > 0 {
		if len(b.pending) == 8192 {
			b.flush()
		}
		size := min(8192-len(b.pending), len(text))
		b.pending = append(b.pending, text[:size]...)
		text = text[size:]
	}
}

func (b *luaBuffer) result() string {
	return strings.Join(b.levels, "") + string(b.pending)
}

func structPack(state *lua.State, args []lua.Value) ([]lua.Value, error) {
	format := structFormat{state: state, text: cstring(state.CheckText(args, 0))}
	header := structHeader{align: 1}
	argument := 2
	total := uint64(0)
	out := newLuaBuffer(args)
	for {
		option, ok := format.next()
		if !ok {
			break
		}
		size := format.size(option)
		padding := structPadding(total, &header, option, size)
		total += padding
		for range padding {
			out.add("\x00")
		}
		switch option {
		case 'b', 'B', 'h', 'H', 'l', 'L', 'T', 'i', 'I':
			number := state.CheckNumber(out.stack, argument-1)
			argument++
			value := cUint64(number)
			if number < 0 {
				value = uint64(cInt64(number))
			}
			out.add(string(structInteger(value, header.big, size)))
		case 'x':
			out.add("\x00")
		case 'f':
			value := uint64(math.Float32bits(float32(state.CheckNumber(out.stack, argument-1))))
			argument++
			out.add(string(structInteger(value, header.big, 4)))
		case 'd':
			value := math.Float64bits(state.CheckNumber(out.stack, argument-1))
			argument++
			out.add(string(structInteger(value, header.big, 8)))
		case 'c', 's':
			text := state.CheckText(out.stack, argument-1)
			argument++
			if size == 0 {
				size = uint64(len(text))
			}
			if uint64(len(text)) < size {
				state.ArgError(argument, "string too short")
			}
			out.add(text[:size])
			if option == 's' {
				out.add("\x00")
				size++
			}
		default:
			format.control(option, &header)
		}
		total += size
	}
	return state.Result(lua.String(out.result())), nil
}

func structFits(state *lua.State, size, position, length uint64) {
	if size > length || position > length-size {
		state.ArgError(2, "data string too short")
	}
}

func structUnpack(state *lua.State, args []lua.Value) ([]lua.Value, error) {
	format := structFormat{state: state, text: cstring(state.CheckText(args, 0))}
	data := state.CheckText(args, 1)
	length := uint64(len(data))
	position := uint64(state.OptionalInteger(args, 2, 1))
	if position == 0 {
		state.ArgError(3, "offset must be 1 or greater")
	}
	position--
	header := structHeader{align: 1}
	var results []lua.Value
	for {
		option, ok := format.next()
		if !ok {
			break
		}
		size := format.size(option)
		position += structPadding(position, &header, option, size)
		structFits(state, size, position, length)
		if len(args)+len(results)+2 > luaMaxCStack {
			state.Raise("stack overflow (too many results)")
		}
		switch option {
		case 'b', 'B', 'h', 'H', 'l', 'L', 'T', 'i', 'I':
			value := uint64(0)
			for index := uint64(0); index < size; index++ {
				slot := position + index
				if !header.big {
					slot = position + size - 1 - index
				}
				value = value<<8 | uint64(data[slot])
			}
			if option >= 'a' {
				mask := ^uint64(0) << ((size*8 - 1) & 63)
				if value&mask != 0 {
					value |= mask
				}
				results = append(results, lua.Number(float64(int64(value))))
			} else {
				results = append(results, lua.Number(float64(value)))
			}
		case 'x':
		case 'f':
			raw := []byte(data[position : position+size])
			value := binary.LittleEndian.Uint32(raw)
			if header.big {
				value = binary.BigEndian.Uint32(raw)
			}
			results = append(results, lua.Number(float64(math.Float32frombits(value))))
		case 'd':
			raw := []byte(data[position : position+size])
			value := binary.LittleEndian.Uint64(raw)
			if header.big {
				value = binary.BigEndian.Uint64(raw)
			}
			results = append(results, lua.Number(math.Float64frombits(value)))
		case 'c':
			if size == 0 {
				var previous float64
				found := false
				if len(results) > 0 {
					previous, found = numeric(results[len(results)-1])
				}
				if !found {
					state.Raise("format 'c0' needs a previous size")
				}
				size = cUint64(previous)
				results = results[:len(results)-1]
				structFits(state, size, position, length)
			}
			results = append(results, lua.String(data[position:position+size]))
		case 's':
			end := strings.IndexByte(data[position:], 0)
			if end < 0 {
				state.Raise("unfinished string in data")
			}
			size = uint64(end) + 1
			results = append(results, lua.String(data[position:position+size-1]))
		default:
			format.control(option, &header)
		}
		position += size
	}
	return append(results, lua.Number(float64(int64(position+1)))), nil
}

func isAlphanumeric(c byte) bool {
	return isDigit(c) || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func structSize(state *lua.State, args []lua.Value) ([]lua.Value, error) {
	format := structFormat{state: state, text: cstring(state.CheckText(args, 0))}
	header := structHeader{align: 1}
	position := uint64(0)
	for {
		option, ok := format.next()
		if !ok {
			break
		}
		size := format.size(option)
		position += structPadding(position, &header, option, size)
		if option == 's' {
			state.ArgError(1, "option 's' has no fixed size")
		} else if option == 'c' && size == 0 {
			state.ArgError(1, "option 'c0' has no fixed size")
		}
		if !isAlphanumeric(option) {
			format.control(option, &header)
		}
		position += size
	}
	return state.Result(lua.Number(float64(int64(position)))), nil
}

func (s *session) openStruct() lua.Value {
	table := s.state.NewTable(0, 3)
	_ = table.RawSet(lua.Intern("pack"), s.state.Function("pack", structPack))
	_ = table.RawSet(lua.Intern("unpack"), s.state.Function("unpack", structUnpack))
	_ = table.RawSet(lua.Intern("size"), s.state.Function("size", structSize))
	return table.Value()
}
