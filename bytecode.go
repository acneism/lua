package lua

const vmPollStride = 32
const vmCallWeight = 4

const numericBase opcode = 64
const numericEnd opcode = numericBase + 12*4
const numericJumpBase opcode = 160
const numericJumpEnd opcode = numericJumpBase + 6*4
const (
	opNegate opcode = 128 + iota
	opNot
	opLength
)

func numericOperation(operator string) int {
	switch operator {
	case "+":
		return 0
	case "-":
		return 1
	case "*":
		return 2
	case "/":
		return 3
	case "%":
		return 4
	case "^":
		return 5
	case "<":
		return 6
	case "<=":
		return 7
	case ">":
		return 8
	case ">=":
		return 9
	case "==":
		return 10
	case "~=":
		return 11
	}
	return -1
}

func (c *compiler) freeze() {
	p := c.proto
	p.code = make([]uint64, len(c.code))
	p.sites = make([]uint32, len(c.code))
	p.lines = make([]int32, len(c.code))
	sites := make(map[instruction]uint32)
	line := int32(max(1, p.line))
	for pc, source := range c.code {
		if source.line > 0 {
			line = int32(source.line)
		}
		p.lines[pc] = line
		source.line = 0
		index, exists := sites[source]
		if !exists {
			index = uint32(len(p.metadata))
			sites[source] = index
			p.metadata = append(p.metadata, source)
		}
		p.sites[pc] = index
		op := source.op
		word := uint64(op) | uint64(uint8(source.a))<<8 | uint64(uint32(source.b))<<16 | uint64(uint16(source.c))<<48
		if op == opCall || op == opReturn {
			word = uint64(op) | uint64(uint8(source.a))<<8 | uint64(uint16(source.b))<<16 | uint64(uint16(source.c))<<32 | uint64(uint8(source.d))<<48
		}
		if op == opBinary {
			operation := numericOperation(source.text)
			left, right, mode := source.b, source.c, 0
			if left < 0 {
				left = -1 - left
				mode |= 1
			}
			if right < 0 {
				right = -1 - right
				mode |= 2
			}
			if operation >= 0 && left <= 65535 && right <= 65535 {
				op = numericBase + opcode(operation*4+mode)
				word = uint64(op) | uint64(uint8(source.a))<<8 | uint64(uint16(left))<<16 | uint64(uint16(right))<<32
			}
		} else if op == opUnary {
			switch source.text {
			case "-":
				op = opNegate
			case "not":
				op = opNot
			case "#":
				op = opLength
			}
			word = word&^255 | uint64(op)
		}
		p.code[pc] = word
	}
	for pc := 0; pc+1 < len(p.code); pc++ {
		word, next := p.code[pc], p.code[pc+1]
		op, nextOp := opcode(word), opcode(next)
		if op >= numericBase+6*4 && op < numericEnd && (nextOp == opJumpFalse || nextOp == opJumpTrue) && uint8(word>>8) == uint8(next>>8) {
			p.code[pc] = word&^255 | uint64(op+numericJumpBase-(numericBase+6*4))
		}
	}
	for pc := 1; pc < len(p.code); pc++ {
		loop, add := p.code[pc], p.code[pc-1]
		index, sum := int(uint8(loop>>8)), int(uint8(add>>8))
		if opcode(loop) == opForLoop && int(uint32(loop>>16)) == pc-1 && opcode(add) == numericBase && uint8(add>>16) == uint8(sum) && int(uint8(add>>32)) == index+3 && (sum < index || sum > index+3) {
			p.code[pc] = loop&^255 | uint64(opForAccumulate) | uint64(uint8(sum))<<48
		}
	}
	for pc := 0; pc+2 < len(p.code); pc++ {
		load, argument, call := p.code[pc], p.code[pc+1], p.code[pc+2]
		loadOp, argumentOp := opcode(load), opcode(argument)
		if (loadOp != opMove && loadOp != opGetUpvalue) || (argumentOp != opMove && argumentOp != opConstant) || opcode(call) != opCall || uint8(load>>8) != uint8(call>>8) || uint8(argument>>8) != uint8(call>>8)+1 || int16(call>>16) != 1 {
			continue
		}
		arg := uint32(argument >> 16)
		if arg > 65535 {
			continue
		}
		op := opCallDirect
		if loadOp == opGetUpvalue {
			op++
		}
		if argumentOp == opConstant {
			op += 2
		}
		p.code[pc] = uint64(op) | uint64(uint8(load>>8))<<8 | uint64(uint8(load>>16))<<16 | uint64(uint16(arg))<<24 | uint64(uint8(call>>48))<<40 | uint64(uint16(call>>32))<<48
	}
	c.code = nil
}

func (p *Proto) instruction(pc int) *instruction { return &p.metadata[p.sites[pc]] }

func (f *frame) sourceLine() int {
	if f.function.proto == nil {
		return 0
	}
	p := f.function.proto
	if len(p.lines) == 0 {
		return 0
	}
	return int(p.lines[min(max(0, int(f.pc)-1), len(p.lines)-1)])
}
