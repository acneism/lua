package lua

import "math"

func (f *redisFunc) nils(from, n int) {
	if f.pc() > f.lastTarget {
		if f.pc() == 0 {
			if from >= f.active {
				return
			}
		} else {
			previous := &f.p.code[f.pc()-1]
			if previous.op() == rLoadNil && previous.a() <= from && from <= previous.b()+1 {
				if from+n-1 > previous.b() {
					previous.setB(from + n - 1)
				}
				return
			}
		}
	}
	f.abc(rLoadNil, from, from+n-1, 0)
}
func (f *redisFunc) jump() int {
	pending := f.pending
	f.pending = -1
	j := f.abx(rJmp, 0, redisMaxJump-1)
	f.concat(&j, pending)
	return j
}
func (f *redisFunc) ret(first, n int)                         { f.abc(rReturn, first, n+1, 0) }
func (f *redisFunc) condjump(op redisOpcode, a, b, c int) int { f.abc(op, a, b, c); return f.jump() }
func (f *redisFunc) fixjump(pc, dest int) {
	offset := dest - pc - 1
	if offset < -redisMaxJump || offset > redisMaxJump {
		f.ls.fail("control structure too long")
	}
	f.p.code[pc].setSBx(offset)
}
func (f *redisFunc) label() int { f.lastTarget = f.pc(); return f.pc() }
func (f *redisFunc) getjump(pc int) int {
	offset := f.p.code[pc].sbx()
	if offset == -1 {
		return -1
	}
	return pc + 1 + offset
}
func (f *redisFunc) jumpcontrol(pc int) *redisInstruction {
	if pc >= 1 && redisTestMode(f.p.code[pc-1].op()) {
		return &f.p.code[pc-1]
	}
	return &f.p.code[pc]
}
func (f *redisFunc) needvalue(list int) bool {
	for ; list != -1; list = f.getjump(list) {
		if f.jumpcontrol(list).op() != rTestSet {
			return true
		}
	}
	return false
}
func (f *redisFunc) patchtestreg(node, reg int) bool {
	i := f.jumpcontrol(node)
	if i.op() != rTestSet {
		return false
	}
	if reg != redisNoRegister && reg != i.b() {
		i.setA(reg)
	} else {
		*i = redisABC(rTest, i.b(), 0, i.c())
	}
	return true
}
func (f *redisFunc) removevalues(list int) {
	for ; list != -1; list = f.getjump(list) {
		f.patchtestreg(list, redisNoRegister)
	}
}
func (f *redisFunc) patchaux(list, vtarget, reg, dtarget int) {
	for list != -1 {
		next := f.getjump(list)
		if f.patchtestreg(list, reg) {
			f.fixjump(list, vtarget)
		} else {
			f.fixjump(list, dtarget)
		}
		list = next
	}
}
func (f *redisFunc) patchlist(list, target int) {
	if target == f.pc() {
		f.patchhere(list)
	} else {
		f.patchaux(list, target, redisNoRegister, target)
	}
}
func (f *redisFunc) patchhere(list int) { f.label(); f.concat(&f.pending, list) }
func (f *redisFunc) concat(first *int, second int) {
	if second == -1 {
		return
	}
	if *first == -1 {
		*first = second
		return
	}
	list := f.jumptail(*first)
	tail := f.jumptail(second)
	f.fixjump(list, second)
	f.jumpTails[*first] = tail
}

func (f *redisFunc) jumptail(list int) int {
	if f.jumpTails == nil {
		f.jumpTails = make(map[int]int)
	}
	head := list
	if cached, ok := f.jumpTails[list]; ok {
		list = cached
	}
	for {
		next := f.getjump(list)
		if next == -1 {
			break
		}
		list = next
	}
	f.jumpTails[head] = list
	return list
}
func (f *redisFunc) checkstack(n int) {
	next := f.free + n
	if next > f.p.maxStack {
		if next >= 250 {
			f.ls.fail("function or expression too complex")
		}
		f.p.maxStack = next
	}
}
func (f *redisFunc) reserve(n int) { f.checkstack(n); f.free += n }
func (f *redisFunc) freereg(reg int) {
	if reg < redisRK && reg >= f.active {
		f.free--
	}
}
func (f *redisFunc) freeexp(e *redisExp) {
	if e.k == rRegister {
		f.freereg(e.info)
	}
}
func (f *redisFunc) addconstant(v Value) int {
	switch v.Type() {
	case TypeString:
		if n, ok := f.constants["s"+v.Text()]; ok {
			return n
		}
	case TypeNumber:
		if n, ok := f.numbers[v.Float()]; ok {
			return n
		}
	case TypeNil:
		if f.nilConstant >= 0 {
			return f.nilConstant
		}
	case TypeBoolean:
		key := "false"
		if v.Truth() {
			key = "true"
		}
		if n, ok := f.constants[key]; ok {
			return n
		}
	}
	n := len(f.p.constants)
	f.p.constants = append(f.p.constants, v)
	switch v.Type() {
	case TypeString:
		f.constants["s"+v.Text()] = n
	case TypeNumber:
		f.numbers[v.Float()] = n
	case TypeNil:
		f.nilConstant = n
	case TypeBoolean:
		key := "false"
		if v.Truth() {
			key = "true"
		}
		f.constants[key] = n
	}
	return n
}
func (f *redisFunc) setreturns(e *redisExp, n int) {
	switch e.k {
	case rCallExp:
		f.p.code[e.info].setC(n + 1)
	case rVarargExp:
		f.p.code[e.info].setB(n + 1)
		f.p.code[e.info].setA(f.free)
		f.reserve(1)
	}
}
func (f *redisFunc) oneret(e *redisExp) {
	switch e.k {
	case rCallExp:
		e.k = rRegister
		e.info = f.p.code[e.info].a()
	case rVarargExp:
		f.p.code[e.info].setB(2)
		e.k = rReloc
	}
}
func (f *redisFunc) discharge(e *redisExp) {
	switch e.k {
	case rLocal:
		e.k = rRegister
	case rUpvalue:
		e.info = f.abc(rGetUpval, 0, e.info, 0)
		e.k = rReloc
	case rGlobal:
		e.info = f.abx(rGetGlobal, 0, e.info)
		e.k = rReloc
	case rIndexed:
		f.freereg(e.aux)
		f.freereg(e.info)
		e.info = f.abc(rGetTable, 0, e.info, e.aux)
		e.k = rReloc
	case rVarargExp, rCallExp:
		f.oneret(e)
	}
}
func (f *redisFunc) codelabel(a, b, jump int) int { f.label(); return f.abc(rLoadBool, a, b, jump) }
func (f *redisFunc) discharge2reg(e *redisExp, reg int) {
	f.discharge(e)
	switch e.k {
	case rNil:
		f.nils(reg, 1)
	case rFalse, rTrue:
		b := 0
		if e.k == rTrue {
			b = 1
		}
		f.abc(rLoadBool, reg, b, 0)
	case rK:
		f.abx(rLoadK, reg, e.info)
	case rNumber:
		f.abx(rLoadK, reg, f.addconstant(Number(e.number)))
	case rReloc:
		f.p.code[e.info].setA(reg)
	case rRegister:
		if reg != e.info {
			f.abc(rMove, reg, e.info, 0)
		}
	default:
		return
	}
	e.info = reg
	e.k = rRegister
}
func (f *redisFunc) discharge2any(e *redisExp) {
	if e.k != rRegister {
		f.reserve(1)
		f.discharge2reg(e, f.free-1)
	}
}
func (f *redisFunc) exp2reg(e *redisExp, reg int) {
	f.discharge2reg(e, reg)
	if e.k == rJump {
		f.concat(&e.t, e.info)
	}
	if e.jumps() {
		pf, pt := -1, -1
		if f.needvalue(e.t) || f.needvalue(e.f) {
			fj := -1
			if e.k != rJump {
				fj = f.jump()
			}
			pf = f.codelabel(reg, 0, 1)
			pt = f.codelabel(reg, 1, 0)
			f.patchhere(fj)
		}
		final := f.label()
		f.patchaux(e.f, final, reg, pf)
		f.patchaux(e.t, final, reg, pt)
	}
	e.f = -1
	e.t = -1
	e.info = reg
	e.k = rRegister
}
func (f *redisFunc) nextreg(e *redisExp) {
	f.discharge(e)
	f.freeexp(e)
	f.reserve(1)
	f.exp2reg(e, f.free-1)
}
func (f *redisFunc) anyreg(e *redisExp) int {
	f.discharge(e)
	if e.k == rRegister {
		if !e.jumps() {
			return e.info
		}
		if e.info >= f.active {
			f.exp2reg(e, e.info)
			return e.info
		}
	}
	f.nextreg(e)
	return e.info
}
func (f *redisFunc) exp2val(e *redisExp) {
	if e.jumps() {
		f.anyreg(e)
	} else {
		f.discharge(e)
	}
}
func (f *redisFunc) rk(e *redisExp) int {
	f.exp2val(e)
	switch e.k {
	case rNumber, rTrue, rFalse, rNil:
		if len(f.p.constants) <= 255 {
			v := Nil()
			if e.k == rNumber {
				v = Number(e.number)
			} else if e.k != rNil {
				v = Bool(e.k == rTrue)
			}
			e.info = f.addconstant(v)
			e.k = rK
			return e.info | redisRK
		}
	case rK:
		if e.info <= 255 {
			return e.info | redisRK
		}
	}
	return f.anyreg(e)
}
func (f *redisFunc) store(v, e *redisExp) {
	switch v.k {
	case rLocal:
		f.freeexp(e)
		f.exp2reg(e, v.info)
		return
	case rUpvalue:
		reg := f.anyreg(e)
		f.abc(rSetUpval, reg, v.info, 0)
	case rGlobal:
		reg := f.anyreg(e)
		f.abx(rSetGlobal, reg, v.info)
	case rIndexed:
		reg := f.rk(e)
		f.abc(rSetTable, v.info, v.aux, reg)
	}
	f.freeexp(e)
}
func (f *redisFunc) self(e, key *redisExp) {
	f.anyreg(e)
	f.freeexp(e)
	base := f.free
	f.reserve(2)
	f.abc(rSelf, base, e.info, f.rk(key))
	f.freeexp(key)
	e.info = base
	e.k = rRegister
}
func (f *redisFunc) invert(e *redisExp) { i := f.jumpcontrol(e.info); i.setA(1 - i.a()) }
func (f *redisFunc) jumponcond(e *redisExp, cond int) int {
	if e.k == rReloc {
		i := f.p.code[e.info]
		if i.op() == rNot {
			f.p.code = f.p.code[:f.pc()-1]
			f.p.lines = f.p.lines[:len(f.p.lines)-1]
			return f.condjump(rTest, i.b(), 0, 1-cond)
		}
	}
	f.discharge2any(e)
	f.freeexp(e)
	return f.condjump(rTestSet, redisNoRegister, e.info, cond)
}
func (f *redisFunc) goiftrue(e *redisExp) {
	f.discharge(e)
	pc := -1
	switch e.k {
	case rK, rNumber, rTrue:
	case rJump:
		f.invert(e)
		pc = e.info
	default:
		pc = f.jumponcond(e, 0)
	}
	f.concat(&e.f, pc)
	f.patchhere(e.t)
	e.t = -1
}
func (f *redisFunc) goiffalse(e *redisExp) {
	f.discharge(e)
	pc := -1
	switch e.k {
	case rNil, rFalse:
	case rJump:
		pc = e.info
	default:
		pc = f.jumponcond(e, 1)
	}
	f.concat(&e.t, pc)
	f.patchhere(e.f)
	e.f = -1
}
func (f *redisFunc) codenot(e *redisExp) {
	f.discharge(e)
	switch e.k {
	case rNil, rFalse:
		e.k = rTrue
	case rK, rNumber, rTrue:
		e.k = rFalse
	case rJump:
		f.invert(e)
	case rReloc, rRegister:
		f.discharge2any(e)
		f.freeexp(e)
		e.info = f.abc(rNot, 0, e.info, 0)
		e.k = rReloc
	}
	e.t, e.f = e.f, e.t
	f.removevalues(e.f)
	f.removevalues(e.t)
}
func (f *redisFunc) indexed(t, k *redisExp) { t.aux = f.rk(k); t.k = rIndexed }

func redisFold(op redisOpcode, e1, e2 *redisExp) bool {
	if !e1.numeral() || !e2.numeral() {
		return false
	}
	a, b := e1.number, e2.number
	var r float64
	switch op {
	case rAdd:
		r = a + b
	case rSub:
		r = a - b
	case rMul:
		r = a * b
	case rDiv:
		if b == 0 {
			return false
		}
		r = a / b
	case rMod:
		if b == 0 {
			return false
		}
		r = luaModulo(a, b)
	case rPow:
		r = luaPower(a, b)
	case rUnm:
		r = -a
	default:
		return false
	}
	if math.IsNaN(r) {
		return false
	}
	e1.number = r
	return true
}
func (f *redisFunc) arith(op redisOpcode, e1, e2 *redisExp) {
	if redisFold(op, e1, e2) {
		return
	}
	o2 := 0
	if op != rUnm && op != rLen {
		o2 = f.rk(e2)
	}
	o1 := f.rk(e1)
	if o1 > o2 {
		f.freeexp(e1)
		f.freeexp(e2)
	} else {
		f.freeexp(e2)
		f.freeexp(e1)
	}
	e1.info = f.abc(op, 0, o1, o2)
	e1.k = rReloc
}
func (f *redisFunc) comparison(op redisOpcode, cond int, e1, e2 *redisExp) {
	o1 := f.rk(e1)
	o2 := f.rk(e2)
	f.freeexp(e2)
	f.freeexp(e1)
	if cond == 0 && op != rEq {
		o1, o2 = o2, o1
		cond = 1
	}
	e1.info = f.condjump(op, cond, o1, o2)
	e1.k = rJump
}
func (f *redisFunc) prefix(op string, e *redisExp) {
	e2 := redisExpression(rNumber, 0)
	switch op {
	case "-":
		if !e.numeral() {
			f.anyreg(e)
		}
		f.arith(rUnm, e, &e2)
	case "not":
		f.codenot(e)
	case "#":
		f.anyreg(e)
		f.arith(rLen, e, &e2)
	}
}
func (f *redisFunc) infix(op string, e *redisExp) {
	switch op {
	case "and":
		f.goiftrue(e)
	case "or":
		f.goiffalse(e)
	case "..":
		f.nextreg(e)
	case "+", "-", "*", "/", "%", "^":
		if !e.numeral() {
			f.rk(e)
		}
	default:
		f.rk(e)
	}
}
func (f *redisFunc) postfix(op string, e1, e2 *redisExp) {
	switch op {
	case "and":
		f.discharge(e2)
		f.concat(&e2.f, e1.f)
		*e1 = *e2
	case "or":
		f.discharge(e2)
		f.concat(&e2.t, e1.t)
		*e1 = *e2
	case "..":
		f.exp2val(e2)
		if e2.k == rReloc && f.p.code[e2.info].op() == rConcat {
			f.freeexp(e1)
			f.p.code[e2.info].setB(e1.info)
			e1.k = rReloc
			e1.info = e2.info
		} else {
			f.nextreg(e2)
			f.arith(rConcat, e1, e2)
		}
	case "+":
		f.arith(rAdd, e1, e2)
	case "-":
		f.arith(rSub, e1, e2)
	case "*":
		f.arith(rMul, e1, e2)
	case "/":
		f.arith(rDiv, e1, e2)
	case "%":
		f.arith(rMod, e1, e2)
	case "^":
		f.arith(rPow, e1, e2)
	case "==":
		f.comparison(rEq, 1, e1, e2)
	case "~=":
		f.comparison(rEq, 0, e1, e2)
	case "<":
		f.comparison(rLt, 1, e1, e2)
	case "<=":
		f.comparison(rLe, 1, e1, e2)
	case ">":
		f.comparison(rLt, 0, e1, e2)
	case ">=":
		f.comparison(rLe, 0, e1, e2)
	}
}
func (f *redisFunc) fixline(line int) { f.p.lines[f.pc()-1] = int32(line) }
func (f *redisFunc) emit(i redisInstruction, line int) int {
	f.patchaux(f.pending, f.pc(), redisNoRegister, f.pc())
	f.pending = -1
	pc := f.pc()
	f.p.code = append(f.p.code, i)
	f.p.lines = append(f.p.lines, int32(line))
	return pc
}
func (f *redisFunc) abc(op redisOpcode, a, b, c int) int {
	return f.emit(redisABC(op, a, b, c), f.ls.lastLine)
}
func (f *redisFunc) abx(op redisOpcode, a, b int) int {
	return f.emit(redisABx(op, a, b), f.ls.lastLine)
}
func (f *redisFunc) setlist(base, nelems, tostore int) {
	c := (nelems-1)/50 + 1
	b := tostore
	if b == -1 {
		b = 0
	}
	if c <= 511 {
		f.abc(rSetList, base, b, c)
	} else {
		f.abc(rSetList, base, b, 0)
		f.emit(redisInstruction(c), f.ls.lastLine)
	}
	f.free = base + 1
}
