package lua

func (p *redisProto) localname(reg, pc int) string {
	for _, v := range p.locals {
		if v.start > pc {
			break
		}
		if pc < v.end {
			if reg == 0 {
				return v.name
			}
			reg--
		}
	}
	return ""
}
func redisWritesA(op redisOpcode) bool {
	switch op {
	case rSetGlobal, rSetUpval, rSetTable, rJmp, rEq, rLt, rLe, rTForLoop, rReturn, rSetList, rClose:
		return false
	}
	return true
}

func (s *State) resolveName(info nameInfo) nameInfo {
	if info.kind != "register" {
		return info
	}
	for index := len(s.current.frames) - 1; index >= 0; index-- {
		f := &s.current.frames[index]
		p := f.function.proto
		if p == nil {
			continue
		}
		pc := int(f.pc) - 1
		if p.canonical == nil || pc < 0 || pc >= len(p.rawPC) {
			return nameInfo{}
		}
		return p.canonical.objname(int(info.register), p.rawPC[pc])
	}
	return nameInfo{}
}
func (p *redisProto) symbexec(lastpc, reg int) redisInstruction {
	last := len(p.code) - 1
	for pc := 0; pc < lastpc; pc++ {
		i := p.code[pc]
		op, a, b, c := i.op(), i.a(), i.b(), i.c()
		if redisWritesA(op) && a == reg {
			last = pc
		}
		switch op {
		case rLoadNil:
			if a <= reg && reg <= b {
				last = pc
			}
		case rSelf:
			if reg == a+1 {
				last = pc
			}
		case rTForLoop:
			if reg >= a+2 {
				last = pc
			}
		case rForLoop, rForPrep, rJmp:
			dest := pc + 1 + i.sbx()
			if pc < dest && dest <= lastpc {
				pc += i.sbx()
			}
		case rCall, rTailCall:
			if reg >= a {
				last = pc
			}
		case rSetList:
			if c == 0 {
				pc++
			}
		case rClosure:
			pc += len(p.children[i.bx()].upvalues)
		}
	}
	return p.code[last]
}
func (p *redisProto) keyname(k int) string {
	if k >= redisRK && p.constants[k-redisRK].Type() == TypeString {
		return p.constants[k-redisRK].Text()
	}
	return "?"
}
func (p *redisProto) objname(reg, pc int) nameInfo {
	for attempts := 0; attempts < 250; attempts++ {
		if name := p.localname(reg, pc); name != "" {
			return nameInfo{kind: "local", name: name}
		}
		i := p.symbexec(pc, reg)
		switch i.op() {
		case rGetGlobal:
			return nameInfo{kind: "global", name: p.constants[i.bx()].Text()}
		case rMove:
			if i.b() < i.a() {
				reg = i.b()
				continue
			}
		case rGetTable:
			return nameInfo{kind: "field", name: p.keyname(i.c())}
		case rGetUpval:
			return nameInfo{kind: "upvalue", name: p.upvalues[i.b()]}
		case rSelf:
			return nameInfo{kind: "method", name: p.keyname(i.c())}
		}
		break
	}
	return nameInfo{}
}
