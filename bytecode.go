package lua

const vmPollStride = 32
const vmCallWeight = 4

const (
	operationAdd = iota
	operationSubtract
	operationMultiply
	operationDivide
	operationModulo
	operationPower
	operationLess
	operationLessEqual
	operationGreater
	operationGreaterEqual
	operationEqual
	operationNotEqual
	operationCount
)

const (
	compareEqual = iota
	compareLess
	compareLessEqual
	compareCount
)

const (
	leftConstant  = 1
	rightConstant = 2
	expectTrue    = 4
	operandModes  = 4
	compareModes  = 8
)

const numericBase opcode = (opLength + operandModes) &^ (operandModes - 1)
const numericCompareBase = numericBase + operationLess*operandModes
const numericEnd = numericBase + operationCount*operandModes
const numericJumpBase = numericEnd
const numericJumpEnd = numericJumpBase + (operationCount-operationLess)*operandModes
const comparisonBase = numericJumpEnd

const (
	opAdd opcode = numericBase + operandModes*iota
	opSubtract
	opMultiply
	opDivide
	opModulo
	opPower
)

func numericOpcode(operation, mode int) opcode {
	return numericBase + opcode(operation*operandModes+mode)
}

func (op opcode) numericOperation() int { return int(op-numericBase) / operandModes }
func (op opcode) arithmetic() bool      { return op >= numericBase && op < numericCompareBase }
func (op opcode) numericCompare() bool  { return op >= numericCompareBase && op < numericEnd }
func (op opcode) withJump() opcode      { return op - numericCompareBase + numericJumpBase }
func (op opcode) withoutJump() opcode   { return op - numericJumpBase + numericCompareBase }

func comparisonOpcode(comparison, mode int) opcode {
	return comparisonBase + opcode(comparison*compareModes+mode)
}

func comparisonKind(mode opcode) int { return int(mode) / compareModes }

func numericOperands(op opcode, word uint64, registers *[256]Value, constants []Value) (*Value, *Value) {
	left, right := &registers[uint8(word>>16)], &registers[uint8(word>>32)]
	if op&leftConstant != 0 {
		left = &constants[uint16(word>>16)]
	}
	if op&rightConstant != 0 {
		right = &constants[uint16(word>>32)]
	}
	return left, right
}

func rkOperand(index int, registers *[256]Value, constants []Value) Value {
	if index < 0 {
		return constants[-1-index]
	}
	return registers[uint8(index)]
}

func numericOperation(operator string) int {
	switch operator {
	case "+":
		return operationAdd
	case "-":
		return operationSubtract
	case "*":
		return operationMultiply
	case "/":
		return operationDivide
	case "%":
		return operationModulo
	case "^":
		return operationPower
	case "<":
		return operationLess
	case "<=":
		return operationLessEqual
	case ">":
		return operationGreater
	case ">=":
		return operationGreaterEqual
	case "==":
		return operationEqual
	case "~=":
		return operationNotEqual
	}
	return -1
}

func (c *compiler) freeze() {
	p := c.proto
	for _, constant := range p.constants {
		if constant.text != nil {
			p.longest = max(p.longest, len(constant.text.text))
		}
	}
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
		switch op {
		case opCompare:
			operation := compareEqual
			switch source.text {
			case "<":
				operation = compareLess
			case "<=":
				operation = compareLessEqual
			}
			left, right, mode := source.b, source.c, source.a*expectTrue
			if left < 0 {
				left = -1 - left
				mode |= leftConstant
			}
			if right < 0 {
				right = -1 - right
				mode |= rightConstant
			}
			word = uint64(comparisonOpcode(operation, mode)) | uint64(uint8(left))<<8 | uint64(uint8(right))<<16
		case opBinary:
			operation := numericOperation(source.text)
			left, right, mode := source.b, source.c, 0
			if left < 0 {
				left = -1 - left
				mode |= leftConstant
			}
			if right < 0 {
				right = -1 - right
				mode |= rightConstant
			}
			if operation >= 0 && left <= 65535 && right <= 65535 {
				op = numericOpcode(operation, mode)
				word = uint64(op) | uint64(uint8(source.a))<<8 | uint64(uint16(left))<<16 | uint64(uint16(right))<<32
			}
		case opUnary:
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
		if op.numericCompare() && (nextOp == opJumpFalse || nextOp == opJumpTrue) && uint8(word>>8) == uint8(next>>8) {
			p.code[pc] = word&^255 | uint64(op.withJump())
		}
	}
	for pc := 1; pc < len(p.code); pc++ {
		loop, add := p.code[pc], p.code[pc-1]
		index, sum := int(uint8(loop>>8)), int(uint8(add>>8))
		if opcode(loop) == opForLoop && int(uint32(loop>>16)) == pc-1 && opcode(add) == opAdd && uint8(add>>16) == uint8(sum) && int(uint8(add>>32)) == index+3 && (sum < index || sum > index+3) {
			p.code[pc] = loop&^255 | uint64(opForAccumulate) | uint64(uint8(sum))<<48
		}
	}
	for pc := 0; pc+2 < len(p.code); pc++ {
		load, argument, call := p.code[pc], p.code[pc+1], p.code[pc+2]
		loadOp, argumentOp := opcode(load), opcode(argument)
		arithmetic := argumentOp.arithmetic()
		if (loadOp != opMove && loadOp != opGetUpvalue) || (argumentOp != opMove && argumentOp != opConstant && !arithmetic) || opcode(call) != opCall || uint8(load>>8) != uint8(call>>8) || uint8(argument>>8) != uint8(call>>8)+1 || int16(call>>16) != 1 {
			continue
		}
		arg := uint32(argument >> 16)
		if arithmetic {
			arg = 0
		}
		if arg > 65535 {
			continue
		}
		op := opCallDirect
		switch {
		case loadOp == opGetUpvalue && argumentOp == opConstant:
			op = opCallDirectUpvalueConstant
		case loadOp == opGetUpvalue:
			op = opCallDirectUpvalue
		case argumentOp == opConstant:
			op = opCallDirectConstant
		}
		if arithmetic {
			op = opCallArithmetic
			if loadOp == opGetUpvalue {
				op = opCallArithmeticUpvalue
			}
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
