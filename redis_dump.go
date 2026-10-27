package lua

import (
	"encoding/binary"
	"math"
)

type redisDumper struct{ data []byte }

func (d *redisDumper) int(n int) { d.data = binary.LittleEndian.AppendUint32(d.data, uint32(n)) }
func (d *redisDumper) text(s string) {
	d.data = binary.LittleEndian.AppendUint64(d.data, uint64(len(s)+1))
	d.data = append(d.data, s...)
	d.data = append(d.data, 0)
}
func (d *redisDumper) function(p *redisProto, parent string) {
	if p.source == parent {
		d.data = binary.LittleEndian.AppendUint64(d.data, 0)
	} else {
		d.text(p.source)
	}
	d.int(p.line)
	d.int(p.lastLine)
	d.data = append(d.data, byte(len(p.upvalues)), byte(p.parameters), byte(p.vararg), byte(p.maxStack))
	d.int(len(p.code))
	for _, i := range p.code {
		d.data = binary.LittleEndian.AppendUint32(d.data, uint32(i))
	}
	d.int(len(p.constants))
	for _, v := range p.constants {
		switch v.Type() {
		case TypeNil:
			d.data = append(d.data, 0)
		case TypeBoolean:
			d.data = append(d.data, 1, byte(v.number))
		case TypeNumber:
			d.data = append(d.data, 3)
			d.data = binary.LittleEndian.AppendUint64(d.data, math.Float64bits(v.number))
		case TypeString:
			d.data = append(d.data, 4)
			d.text(v.Text())
		}
	}
	d.int(len(p.children))
	for _, c := range p.children {
		d.function(c, p.source)
	}
	d.int(len(p.lines))
	for _, line := range p.lines {
		d.int(int(line))
	}
	d.int(len(p.locals))
	for _, v := range p.locals {
		d.text(v.name)
		d.int(v.start)
		d.int(v.end)
	}
	d.int(len(p.upvalues))
	for _, v := range p.upvalues {
		d.text(v)
	}
}
func dumpRedis(p *redisProto) []byte {
	d := redisDumper{data: []byte{27, 'L', 'u', 'a', 0x51, 0, 1, 4, 8, 4, 8, 0}}
	d.function(p, "\x00")
	return d.data
}
