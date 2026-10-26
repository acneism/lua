package lua

func redisOperand(reg int) int {
	if reg >= redisRK {
		return -1 - (reg - redisRK)
	}
	return reg
}

func translateRedis(raw *redisProto) *Proto {
	p := &Proto{canonical: raw, chunk: raw.source, constants: append([]Value(nil), raw.constants...), parameters: raw.parameters, maxRegisters: raw.maxStack, vararg: raw.vararg&2 != 0, usesVararg: raw.vararg&4 == 0, argRegister: -1, line: raw.line, needsArg: raw.vararg&4 != 0}
	if raw.vararg&1 != 0 {
		p.argRegister = raw.parameters
	}
	for _, child := range raw.children {
		p.children = append(p.children, translateRedis(child))
	}
	c := &compiler{proto: p}
	mapping := make([]int, len(raw.code)+1)
	targets := make(map[int]int)
	emit := func(i instruction, pc int) int {
		if len(c.code) > 0 && len(c.code)%vmPollStride == 0 {
			c.code = append(c.code, instruction{op: opPoll, line: int(raw.lines[pc])})
			p.rawPC = append(p.rawPC, pc)
		}
		i.line = int(raw.lines[pc])
		p.rawPC = append(p.rawPC, pc)
		index := len(c.code)
		c.code = append(c.code, i)
		return index
	}
	constant := func(v Value) int {
		for n, value := range p.constants {
			if value.Equal(v) {
				return n
			}
		}
		p.constants = append(p.constants, v)
		return len(p.constants) - 1
	}
	name := func(reg, pc int) nameInfo {
		if reg >= redisRK {
			return nameInfo{}
		}
		return nameInfo{kind: "register", register: int16(reg)}
	}
	for pc := 0; pc < len(raw.code); pc++ {
		mapping[pc] = len(c.code)
		i := raw.code[pc]
		a, b, d := i.a(), i.b(), i.c()
		ins := instruction{a: a, b: b, c: d}
		switch i.op() {
		case rMove:
			ins.op = opMove
		case rLoadK:
			ins.op = opConstant
			ins.b = i.bx()
		case rLoadBool:
			ins.op = opConstant
			ins.b = constant(Bool(b != 0))
			emit(ins, pc)
			if d != 0 {
				jump := emit(instruction{op: opJump}, pc)
				targets[jump] = pc + 2
			}
			continue
		case rLoadNil:
			ins.op = opLoadNil
		case rGetUpval:
			ins.op = opGetUpvalue
		case rGetGlobal:
			ins.op = opGetGlobal
			ins.b = i.bx()
		case rSetGlobal:
			ins.op = opSetGlobal
			ins.a = i.bx()
			ins.b = a
		case rSetUpval:
			ins.op = opSetUpvalue
			ins.a = b
			ins.b = a
		case rGetTable:
			ins.op = opGetTable
			if d >= redisRK {
				ins.op = opGetTableK
				ins.c = d - redisRK
			}
			ins.left = name(b, pc)
		case rSetTable:
			ins.op = opSetTable
			if b >= redisRK || d >= redisRK {
				ins.op = opSetTableRK
				ins.b = redisOperand(b)
				ins.c = redisOperand(d)
			}
			ins.left = name(a, pc)
		case rNewTable:
			ins.op = opNewTable
			ins.b = redisFb2int(b)
			ins.c = redisFb2int(d)
		case rSelf:
			ins.op = opSelf
			ins.c = redisOperand(d)
			ins.left = name(b, pc)
		case rAdd, rSub, rMul, rDiv, rMod, rPow:
			ins.op = opBinary
			ins.b = redisOperand(b)
			ins.c = redisOperand(d)
			ins.text = []string{"+", "-", "*", "/", "%", "^"}[i.op()-rAdd]
			ins.left = name(b, pc)
			ins.right = name(d, pc)
		case rUnm, rNot, rLen:
			ins.op = opUnary
			ins.text = []string{"-", "not", "#"}[i.op()-rUnm]
			ins.left = name(b, pc)
		case rConcat:
			ins.op = opConcat
		case rJmp:
			ins.op = opJump
			ins.c = 0
		case rEq, rLt, rLe:
			scratch := raw.maxStack
			p.maxRegisters = max(p.maxRegisters, scratch+1)
			ins = instruction{op: opBinary, a: scratch, b: redisOperand(b), c: redisOperand(d), text: []string{"==", "<", "<="}[i.op()-rEq], left: name(b, pc), right: name(d, pc)}
			emit(ins, pc)
			branch := opJumpTrue
			if a != 0 {
				branch = opJumpFalse
			}
			index := emit(instruction{op: branch, a: scratch}, pc)
			targets[index] = pc + 2
			continue
		case rTest:
			ins.op = opJumpTrue
			if d != 0 {
				ins.op = opJumpFalse
			}
			index := emit(ins, pc)
			targets[index] = pc + 2
			continue
		case rTestSet:
			ins.op = opTestSet
		case rCall, rTailCall:
			ins.op = opCall
			if i.op() == rTailCall {
				ins.op = opTailCall
			}
			ins.b = b - 1
			ins.c = d - 1
			ins.d = a
			ins.left = name(a, pc)
			ins.method = ins.left.kind == "method"
		case rReturn:
			ins.op = opReturn
			ins.b = b - 1
		case rForLoop, rForPrep:
			ins.op = opForLoop
			ins.c = 0
			if i.op() == rForPrep {
				ins.op = opForPrep
			}
		case rTForLoop:
			ins.op = opForIterator
			ins.left = name(a, pc)
		case rSetList:
			ins.op = opSetList
			ins.b = a + 1
			ins.c = b
			if b == 0 {
				ins.c = -1
			}
			if d == 0 {
				pc++
				d = int(raw.code[pc])
				mapping[pc] = len(c.code)
			}
			ins.d = (d - 1) * 50
		case rClose:
			ins.op = opClose
		case rClosure:
			ins.op = opClosure
			ins.b = i.bx()
			child := p.children[ins.b]
			for n, upname := range child.canonical.upvalues {
				binding := raw.code[pc+1+n]
				child.upvalues = append(child.upvalues, upvalueDescriptor{local: binding.op() == rMove, index: binding.b(), name: upname})
			}
			index := emit(ins, pc)
			_ = index
			for range child.upvalues {
				pc++
				mapping[pc] = len(c.code)
			}
			continue
		case rVararg:
			ins.op = opVararg
			ins.b = b - 1
		}
		index := emit(ins, pc)
		switch i.op() {
		case rJmp, rForLoop, rForPrep:
			targets[index] = pc + 1 + i.sbx()
		case rTestSet, rTForLoop:
			targets[index] = pc + 2
		}
	}
	mapping[len(raw.code)] = len(c.code)
	for index, rawTarget := range targets {
		if rawTarget >= 0 {
			if c.code[index].op == opTestSet || c.code[index].op == opForIterator {
				c.code[index].d = mapping[rawTarget]
			} else {
				c.code[index].b = mapping[rawTarget]
			}
		}
	}
	c.freeze()
	return p
}
