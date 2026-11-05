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
const comparisonBase = numericBase + operationCount*operandModes

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

func rkOperand(index int, registers *[256]Value, constants []Value) *Value {
	if index < 0 {
		return &constants[-1-index]
	}
	return &registers[uint8(index)]
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
	}
	return -1
}

const codeWindowWords = 256

type codeBlocks struct{ free []uint64 }

func (b *codeBlocks) words(count int) []uint64 {
	if count > codeWindowWords {
		return make([]uint64, count)
	}
	if count > len(b.free) {
		b.free = make([]uint64, 2*codeWindowWords)[codeWindowWords:]
	}
	words := b.free[:count:count]
	b.free = b.free[count:]
	return words
}

func (c *compiler) freeze() {
	p := c.proto
	for _, constant := range p.constants {
		if constant.text != nil {
			p.longest = max(p.longest, len(constant.text.text))
		}
	}
	for _, child := range p.children {
		p.longest = max(p.longest, child.longest)
	}
	p.code = c.blocks.words(len(c.code))
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
		if op == opForIterator {
			word = uint64(op) | uint64(uint8(source.a))<<8 | uint64(uint32(source.d))<<16 | uint64(uint16(source.c))<<48
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

	for pc := range p.code {
		if count := selfArguments(p.code, pc); count > 0 {
			p.code[pc] |= uint64(count) << 24
		}
		if count, arguments, ok := fusedArguments(p.code, pc); ok {
			op := opCallFused
			if opcode(p.code[pc]) == opGetUpvalue {
				op = opCallFusedUpvalue
			}
			p.code[pc] = uint64(op) | p.code[pc]&(255<<8|255<<16) | uint64(count)<<24 | arguments<<32
		}
	}

	c.code = nil
}

func selfArguments(code []uint64, pc int) int {
	self := code[pc]
	if opcode(self) != opSelf {
		return 0
	}
	method := uint8(self >> 8)
	for count := 1; count <= 4 && pc+count < len(code); count++ {
		word := code[pc+count]
		if opcode(word) == opCall {
			if uint8(word>>8) != method || int16(word>>16) != int16(count) {
				return 0
			}
			return count
		}
		if uint8(word>>8) != method+uint8(count)+1 || opcode(word) == opMove && uint8(word>>16) >= method || opcode(word) != opMove && opcode(word) != opConstant {
			return 0
		}
	}
	return 0
}

const (
	fusedConstant   = 128
	fusedArithmetic = 255
)

func fusedArguments(code []uint64, pc int) (int, uint64, bool) {
	load := code[pc]
	if opcode(load) != opMove && opcode(load) != opGetUpvalue {
		return 0, 0, false
	}
	function := uint8(load >> 8)
	if opcode(load) == opMove && uint8(load>>16) >= function {
		return 0, 0, false
	}
	var arguments uint64
	for count := 0; count <= 4 && pc+count+1 < len(code); count++ {
		word := code[pc+count+1]
		if opcode(word) == opCall {
			return count, arguments, uint8(word>>8) == function && int16(word>>16) == int16(count)
		}
		source := uint64(uint32(word >> 16))
		switch {
		case uint8(word>>8) != function+uint8(count)+1:
			return 0, 0, false
		case opcode(word) == opMove && source < uint64(function) && source < fusedConstant:
		case opcode(word) == opConstant && source < fusedArithmetic-fusedConstant:
			source += fusedConstant
		case fusedOperation(word, function):
			source = fusedArithmetic
		default:
			return 0, 0, false
		}
		arguments |= source << (8 * count)
	}
	return 0, 0, false
}

func fusedOperation(word uint64, function uint8) bool {
	op := opcode(word)
	if op < opAdd || op >= opDivide {
		return false
	}
	return (op&leftConstant != 0 || uint16(word>>16) < uint16(function)) && (op&rightConstant != 0 || uint16(word>>32) < uint16(function))
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
