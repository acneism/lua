package redis

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/acneism/lua"
)

const msgpackMaxNesting = 16

func msgpackBytes(dst []byte, text string) []byte {
	length := len(text)
	switch {
	case length < 32:
		dst = append(dst, 0xa0|byte(length))
	case length <= 0xff:
		dst = append(dst, 0xd9, byte(length))
	case length <= 0xffff:
		dst = binary.BigEndian.AppendUint16(append(dst, 0xda), uint16(length))
	default:
		dst = binary.BigEndian.AppendUint32(append(dst, 0xdb), uint32(length))
	}
	return append(dst, text...)
}

func msgpackDouble(dst []byte, value float64) []byte {
	single := float32(value)
	if value == float64(single) {
		return binary.BigEndian.AppendUint32(append(dst, 0xca), math.Float32bits(single))
	}
	return binary.BigEndian.AppendUint64(append(dst, 0xcb), math.Float64bits(value))
}

func msgpackInteger(dst []byte, value int64) []byte {
	if value >= 0 {
		switch {
		case value <= 127:
			return append(dst, byte(value))
		case value <= 0xff:
			return append(dst, 0xcc, byte(value))
		case value <= 0xffff:
			return binary.BigEndian.AppendUint16(append(dst, 0xcd), uint16(value))
		case value <= 0xffffffff:
			return binary.BigEndian.AppendUint32(append(dst, 0xce), uint32(value))
		}
		return binary.BigEndian.AppendUint64(append(dst, 0xcf), uint64(value))
	}
	switch {
	case value >= -32:
		return append(dst, byte(value))
	case value >= -128:
		return append(dst, 0xd0, byte(value))
	case value >= -32768:
		return binary.BigEndian.AppendUint16(append(dst, 0xd1), uint16(value))
	case value >= -2147483648:
		return binary.BigEndian.AppendUint32(append(dst, 0xd2), uint32(value))
	}
	return binary.BigEndian.AppendUint64(append(dst, 0xd3), uint64(value))
}

func msgpackHeader(dst []byte, count int, fix, wide16, wide32 byte) []byte {
	switch {
	case count <= 15:
		return append(dst, fix|byte(count))
	case count <= 65535:
		return binary.BigEndian.AppendUint16(append(dst, wide16), uint16(count))
	}
	return binary.BigEndian.AppendUint32(append(dst, wide32), uint32(count))
}

func msgpackIsArray(table *lua.Table) bool {
	highest, count := int32(0), int32(0)
	for key, _, err := table.Next(lua.Nil()); !key.IsNil() && err == nil; key, _, err = table.Next(key) {
		if key.Type() != lua.TypeNumber {
			return false
		}
		index := key.Float()
		if index <= 0 || math.IsInf(index, 0) || float64(cInt32(index)) != index {
			return false
		}
		if index > float64(highest) {
			highest = cInt32(index)
		}
		count++
	}
	return highest == count
}

func msgpackValue(dst []byte, value lua.Value, level int) []byte {
	switch value.Type() {
	case lua.TypeString:
		return msgpackBytes(dst, value.Text())
	case lua.TypeBoolean:
		if value.Truth() {
			return append(dst, 0xc3)
		}
		return append(dst, 0xc2)
	case lua.TypeNumber:
		number := value.Float()
		if integer := cInt64(number); !math.IsInf(number, 0) && float64(integer) == number {
			return msgpackInteger(dst, integer)
		}
		return msgpackDouble(dst, number)
	case lua.TypeTable:
		if level == msgpackMaxNesting {
			return append(dst, 0xc0)
		}
		table := value.Table()
		if msgpackIsArray(table) {
			length := table.Len()
			dst = msgpackHeader(dst, length, 0x90, 0xdc, 0xdd)
			for index := 1; index <= length; index++ {
				dst = msgpackValue(dst, table.RawGet(lua.Number(float64(index))), level+1)
			}
			return dst
		}
		count := 0
		for key, _, err := table.Next(lua.Nil()); !key.IsNil() && err == nil; key, _, err = table.Next(key) {
			count++
		}
		dst = msgpackHeader(dst, count, 0x80, 0xde, 0xdf)
		for key, item, err := table.Next(lua.Nil()); !key.IsNil() && err == nil; key, item, err = table.Next(key) {
			dst = msgpackValue(dst, key, level+1)
			dst = msgpackValue(dst, item, level+1)
		}
		return dst
	}
	return append(dst, 0xc0)
}

func msgpackPack(state *lua.State, args []lua.Value) ([]lua.Value, error) {
	if len(args) == 0 {
		state.ArgError(0, "MessagePack pack needs input.")
	}
	if len(args) > luaMaxCStack || len(args)+len(args) > luaMaxCStack {
		state.ArgError(0, "Too many arguments for MessagePack pack.")
	}
	var out []byte
	for _, value := range args {
		out = msgpackValue(out, value, 0)
	}
	return state.Result(lua.String(string(out))), nil
}

const (
	msgpackOK = iota
	msgpackEOF
	msgpackBadFormat
)

type msgpackCursor struct {
	state   *lua.State
	data    string
	failure int
	abort   error
}

func (c *msgpackCursor) need(count uint64) bool {
	if uint64(len(c.data)) < count {
		c.failure = msgpackEOF
		return false
	}
	return true
}

func (c *msgpackCursor) failed() bool { return c.failure != msgpackOK || c.abort != nil }

func (c *msgpackCursor) checkStack(slots int, message string) {
	if slots+1 > luaMaxCStack {
		c.state.Raise("stack overflow (" + message + ")")
	}
}

func (c *msgpackCursor) uint16At(index int) uint64 {
	return uint64(c.data[index])<<8 | uint64(c.data[index+1])
}

func (c *msgpackCursor) uint32At(index int) uint64 {
	return c.uint16At(index)<<16 | c.uint16At(index+2)
}

func (c *msgpackCursor) uint64At(index int) uint64 {
	return c.uint32At(index)<<32 | c.uint32At(index+4)
}

func (c *msgpackCursor) array(count uint64, slots int) lua.Value {
	table := c.state.NewTable(0, 0)
	c.checkStack(slots+1, "in function mp_decode_to_lua_array")
	for index := uint64(1); count > 0; index, count = index+1, count-1 {
		value := c.decode(slots + 2)
		if c.failed() {
			return lua.Nil()
		}
		if err := table.RawSet(lua.Number(float64(int32(index))), value); err != nil {
			c.abort = err
			return lua.Nil()
		}
	}
	return table.Value()
}

func (c *msgpackCursor) hash(count uint64, slots int) lua.Value {
	table := c.state.NewTable(0, 0)
	for ; count > 0; count-- {
		key := c.decode(slots + 1)
		if c.failed() {
			return lua.Nil()
		}
		value := c.decode(slots + 2)
		if c.failed() {
			return lua.Nil()
		}
		if err := table.RawSet(key, value); err != nil {
			c.abort = err
			return lua.Nil()
		}
	}
	return table.Value()
}

func (c *msgpackCursor) text(header int, length uint64) lua.Value {
	if !c.need(uint64(header) + length) {
		return lua.Nil()
	}
	end := header + int(length)
	value := lua.String(c.data[header:end])
	c.data = c.data[end:]
	return value
}

func (c *msgpackCursor) number(size int, value float64) lua.Value {
	c.data = c.data[size:]
	return lua.Number(value)
}

func (c *msgpackCursor) decode(slots int) lua.Value {
	if !c.need(1) {
		return lua.Nil()
	}
	c.checkStack(slots, "too many return values at once; use unpack_one or unpack_limit instead.")
	first := c.data[0]
	switch first {
	case 0xcc:
		if c.need(2) {
			return c.number(2, float64(c.data[1]))
		}
	case 0xd0:
		if c.need(2) {
			return c.number(2, float64(int8(c.data[1])))
		}
	case 0xcd:
		if c.need(3) {
			return c.number(3, float64(c.uint16At(1)))
		}
	case 0xd1:
		if c.need(3) {
			return c.number(3, float64(int16(c.uint16At(1))))
		}
	case 0xce:
		if c.need(5) {
			return c.number(5, float64(c.uint32At(1)))
		}
	case 0xd2:
		if c.need(5) {
			return c.number(5, float64(int32(c.uint32At(1))))
		}
	case 0xcf, 0xd3:
		if c.need(9) {
			return c.number(9, float64(int64(c.uint64At(1))))
		}
	case 0xc0:
		c.data = c.data[1:]
		return lua.Nil()
	case 0xc3:
		c.data = c.data[1:]
		return lua.Bool(true)
	case 0xc2:
		c.data = c.data[1:]
		return lua.Bool(false)
	case 0xca:
		if c.need(5) {
			return c.number(5, float64(math.Float32frombits(uint32(c.uint32At(1)))))
		}
	case 0xcb:
		if c.need(9) {
			return c.number(9, math.Float64frombits(c.uint64At(1)))
		}
	case 0xd9:
		if c.need(2) {
			return c.text(2, uint64(c.data[1]))
		}
	case 0xda:
		if c.need(3) {
			return c.text(3, c.uint16At(1))
		}
	case 0xdb:
		if c.need(5) {
			return c.text(5, c.uint32At(1))
		}
	case 0xdc:
		if c.need(3) {
			count := c.uint16At(1)
			c.data = c.data[3:]
			return c.array(count, slots)
		}
	case 0xdd:
		if c.need(5) {
			count := c.uint32At(1)
			c.data = c.data[5:]
			return c.array(count, slots)
		}
	case 0xde:
		if c.need(3) {
			count := c.uint16At(1)
			c.data = c.data[3:]
			return c.hash(count, slots)
		}
	case 0xdf:
		if c.need(5) {
			count := c.uint32At(1)
			c.data = c.data[5:]
			return c.hash(count, slots)
		}
	default:
		switch {
		case first&0x80 == 0:
			return c.number(1, float64(first))
		case first&0xe0 == 0xe0:
			return c.number(1, float64(int8(first)))
		case first&0xe0 == 0xa0:
			return c.text(1, uint64(first&0x1f))
		case first&0xf0 == 0x90:
			c.data = c.data[1:]
			return c.array(uint64(first&0xf), slots)
		case first&0xf0 == 0x80:
			c.data = c.data[1:]
			return c.hash(uint64(first&0xf), slots)
		}
		c.failure = msgpackBadFormat
	}
	return lua.Nil()
}

func msgpackUnpack(state *lua.State, args []lua.Value, limit, offset int64, stack int) ([]lua.Value, error) {
	text := state.CheckText(args, 0)
	length := int64(len(text))
	if offset < 0 || limit < 0 {
		state.Raise(fmt.Sprintf("Invalid request to unpack with offset of %d and limit of %d.", int32(offset), int32(length)))
	} else if offset > length {
		state.Raise(fmt.Sprintf("Start offset %d greater than input length %d.", int32(offset), int32(length)))
	}
	all := limit == 0 && offset == 0
	if all {
		limit = math.MaxInt32
	}
	cursor := msgpackCursor{state: state, data: text[offset:]}
	results := []lua.Value{}
	for count := int64(0); len(cursor.data) > 0 && count < limit; count++ {
		value := cursor.decode(stack + len(results))
		if cursor.abort != nil {
			return nil, cursor.abort
		}
		switch cursor.failure {
		case msgpackEOF:
			state.Raise("Missing bytes in input.")
		case msgpackBadFormat:
			state.Raise("Bad data format in input.")
		}
		results = append(results, value)
	}
	if !all {
		if stack+len(results)+1 > luaMaxCStack {
			state.Raise("stack overflow (in function mp_unpack_full)")
		}
		next := length - int64(len(cursor.data))
		if len(cursor.data) == 0 {
			next = -1
		}
		results = append([]lua.Value{lua.Number(float64(next))}, results...)
	}
	return results, nil
}

func (s *session) openCMsgpack() lua.Value {
	table := s.state.NewTable(0, 0)
	functions := []struct {
		name     string
		function lua.GoFunction
	}{
		{"pack", msgpackPack},
		{"unpack", func(state *lua.State, args []lua.Value) ([]lua.Value, error) {
			return msgpackUnpack(state, args, 0, 0, len(args))
		}},
		{"unpack_one", func(state *lua.State, args []lua.Value) ([]lua.Value, error) {
			offset := state.OptionalInteger(args, 1, 0)
			return msgpackUnpack(state, args[:min(len(args), 1)], 1, int64(offset), 1)
		}},
		{"unpack_limit", func(state *lua.State, args []lua.Value) ([]lua.Value, error) {
			limit := cInt64(state.CheckNumber(args, 1))
			offset := state.OptionalInteger(args, 2, 0)
			return msgpackUnpack(state, args[:min(len(args), 1)], limit, int64(offset), 1)
		}},
	}
	for _, entry := range functions {
		_ = table.RawSet(lua.Intern(entry.name), s.state.Function(entry.name, entry.function))
	}
	for _, field := range [][2]string{{"_NAME", "cmsgpack"}, {"_VERSION", "lua-cmsgpack 0.4.0"}, {"_COPYRIGHT", "Copyright (C) 2012, Salvatore Sanfilippo"}, {"_DESCRIPTION", "MessagePack C implementation for Lua"}} {
		_ = table.RawSet(lua.Intern(field[0]), lua.String(field[1]))
	}
	return table.Value()
}
