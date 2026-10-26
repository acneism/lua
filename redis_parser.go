package lua

import "fmt"

type redisParser struct {
	lexer           lexer
	current         token
	lookahead       *token
	lastLine, depth int
	fs              *redisFunc
}

func (p *redisParser) next() {
	p.lastLine = p.lexer.line
	if p.lookahead != nil {
		p.current = *p.lookahead
		p.lookahead = nil
	} else {
		p.current = p.lexer.next()
	}
}
func (p *redisParser) peek() token {
	if p.lookahead == nil {
		t := p.lexer.next()
		p.lookahead = &t
	}
	return *p.lookahead
}
func (p *redisParser) fail(message string) {
	panic(syntaxError(p.lexer.chunk, p.lexer.line, message+" near '"+p.current.text+"'"))
}
func (p *redisParser) lexerror(message string) {
	panic(syntaxError(p.lexer.chunk, p.lexer.line, message))
}
func (p *redisParser) test(kind string) bool {
	if p.current.kind != kind {
		return false
	}
	p.next()
	return true
}
func (p *redisParser) check(kind string) {
	if p.current.kind != kind {
		p.fail("'" + kind + "' expected")
	}
}
func (p *redisParser) expect(kind string) { p.check(kind); p.next() }
func (p *redisParser) match(what, who string, line int) {
	if p.test(what) {
		return
	}
	if line == p.lexer.line {
		p.fail("'" + what + "' expected")
	}
	p.fail(fmt.Sprintf("'%s' expected (to close '%s' at line %d)", what, who, line))
}
func (p *redisParser) name() string { p.check("<name>"); name := p.current.text; p.next(); return name }
func (p *redisParser) stringexp(e *redisExp, s string) {
	*e = redisExpression(rK, p.fs.addconstant(String(s)))
}
func (p *redisParser) checkname(e *redisExp) { p.stringexp(e, p.name()) }
func (f *redisFunc) limit(value, limit int, what string) {
	if value <= limit {
		return
	}
	where := "main function"
	if f.p.line != 0 {
		where = fmt.Sprintf("function at line %d", f.p.line)
	}
	f.ls.lexerror(fmt.Sprintf("%s has more than %d %s", where, limit, what))
}
func (p *redisParser) newlocal(name string, n int) {
	f := p.fs
	f.limit(f.active+n+1, 200, "local variables")
	f.actvar[f.active+n] = len(f.p.locals)
	f.p.locals = append(f.p.locals, redisLocal{name: name})
}
func (p *redisParser) adjustlocals(n int) {
	f := p.fs
	f.active += n
	for ; n > 0; n-- {
		f.p.locals[f.actvar[f.active-n]].start = f.pc()
	}
}
func (p *redisParser) removelocals(level int) {
	f := p.fs
	for f.active > level {
		f.active--
		f.p.locals[f.actvar[f.active]].end = f.pc()
	}
}
func (f *redisFunc) indexupvalue(name string, v *redisExp) int {
	for i, u := range f.upvalues {
		if u.k == v.k && u.index == v.info {
			return i
		}
	}
	f.limit(len(f.upvalues)+1, 60, "upvalues")
	i := len(f.upvalues)
	f.upvalues = append(f.upvalues, redisUpvalue{k: v.k, index: v.info})
	f.p.upvalues = append(f.p.upvalues, name)
	return i
}
func (f *redisFunc) searchvar(name string) int {
	for i := f.active - 1; i >= 0; i-- {
		if f.p.locals[f.actvar[i]].name == name {
			return i
		}
	}
	return -1
}
func (f *redisFunc) markupvalue(level int) {
	b := f.block
	for b != nil && b.locals > level {
		b = b.previous
	}
	if b != nil {
		b.upvalue = true
	}
}
func redisSinglevar(f *redisFunc, name string, v *redisExp, base bool) redisExpKind {
	if f == nil {
		*v = redisExpression(rGlobal, redisNoRegister)
		return rGlobal
	}
	if reg := f.searchvar(name); reg >= 0 {
		*v = redisExpression(rLocal, reg)
		if !base {
			f.markupvalue(reg)
		}
		return rLocal
	}
	if redisSinglevar(f.parent, name, v, false) == rGlobal {
		return rGlobal
	}
	v.info = f.indexupvalue(name, v)
	v.k = rUpvalue
	return rUpvalue
}
func (p *redisParser) singlevar(v *redisExp) {
	name := p.name()
	if redisSinglevar(p.fs, name, v, true) == rGlobal {
		v.info = p.fs.addconstant(String(name))
	}
}
func (p *redisParser) adjustassign(nvars, nexps int, e *redisExp) {
	f := p.fs
	extra := nvars - nexps
	if redisMultret(e.k) {
		extra++
		if extra < 0 {
			extra = 0
		}
		f.setreturns(e, extra)
		if extra > 1 {
			f.reserve(extra - 1)
		}
	} else {
		if e.k != rVoid {
			f.nextreg(e)
		}
		if extra > 0 {
			reg := f.free
			f.reserve(extra)
			f.nils(reg, extra)
		}
	}
}
func (p *redisParser) enterlevel() {
	p.depth++
	if p.depth > 200 {
		p.lexerror("chunk has too many syntax levels")
	}
}
func (f *redisFunc) enterblock(loop bool) *redisBlock {
	b := &redisBlock{previous: f.block, breaks: -1, locals: f.active, loop: loop}
	f.block = b
	return b
}
func (f *redisFunc) leaveblock() {
	b := f.block
	f.block = b.previous
	f.ls.removelocals(b.locals)
	if b.upvalue {
		f.abc(rClose, b.locals, 0, 0)
	}
	f.free = f.active
	f.patchhere(b.breaks)
}
func (p *redisParser) pushclosure(child *redisFunc, e *redisExp) {
	f := p.fs
	index := len(f.p.children)
	f.p.children = append(f.p.children, child.p)
	*e = redisExpression(rReloc, f.abx(rClosure, 0, index))
	for _, u := range child.upvalues {
		op := rGetUpval
		if u.k == rLocal {
			op = rMove
		}
		f.abc(op, 0, u.index, 0)
	}
}
func (p *redisParser) openfunc() *redisFunc {
	f := &redisFunc{p: &redisProto{source: p.lexer.chunk, maxStack: 2}, parent: p.fs, ls: p, lastTarget: -1, pending: -1, constants: make(map[string]int), numbers: make(map[float64]int), nilConstant: -1}
	p.fs = f
	return f
}
func (p *redisParser) closefunc() { f := p.fs; p.removelocals(0); f.ret(0, 0); p.fs = f.parent }
func parseRedis(source []byte, chunk string, depth int) *redisProto {
	p := &redisParser{lexer: lexer{source: string(source), chunk: chunk, line: 1}, lastLine: 1, depth: depth}
	f := p.openfunc()
	f.p.vararg = 2
	p.next()
	p.chunk()
	p.check("<eof>")
	p.closefunc()
	return f.p
}
func (p *redisParser) field(v *redisExp) {
	f := p.fs
	f.anyreg(v)
	p.next()
	var key redisExp
	p.checkname(&key)
	f.indexed(v, &key)
}
func (p *redisParser) index(v *redisExp) { p.next(); p.expr(v); p.fs.exp2val(v); p.expect("]") }

type redisConstructor struct {
	value                redisExp
	table                *redisExp
	hash, array, pending int
}

func (p *redisParser) recordfield(cc *redisConstructor) {
	f := p.fs
	reg := f.free
	var key, value redisExp
	if p.current.kind == "<name>" {
		p.checkname(&key)
	} else {
		p.index(&key)
	}
	cc.hash++
	p.expect("=")
	rk := f.rk(&key)
	p.expr(&value)
	f.abc(rSetTable, cc.table.info, rk, f.rk(&value))
	f.free = reg
}
func (f *redisFunc) closelist(cc *redisConstructor) {
	if cc.value.k == rVoid {
		return
	}
	f.nextreg(&cc.value)
	cc.value.k = rVoid
	if cc.pending == 50 {
		f.setlist(cc.table.info, cc.array, cc.pending)
		cc.pending = 0
	}
}
func (f *redisFunc) lastlist(cc *redisConstructor) {
	if cc.pending == 0 {
		return
	}
	if redisMultret(cc.value.k) {
		f.setreturns(&cc.value, -1)
		f.setlist(cc.table.info, cc.array, -1)
		cc.array--
	} else {
		if cc.value.k != rVoid {
			f.nextreg(&cc.value)
		}
		f.setlist(cc.table.info, cc.array, cc.pending)
	}
}
func (p *redisParser) listfield(cc *redisConstructor) { p.expr(&cc.value); cc.array++; cc.pending++ }
func redisInt2fb(x int) int {
	e := 0
	for x >= 16 {
		x = (x + 1) >> 1
		e++
	}
	if x < 8 {
		return x
	}
	return (e+1)<<3 | x - 8
}
func redisFb2int(x int) int {
	e := x >> 3 & 31
	if e == 0 {
		return x
	}
	return (x&7 + 8) << (e - 1)
}
func (p *redisParser) constructor(t *redisExp) {
	f := p.fs
	line := p.lexer.line
	pc := f.abc(rNewTable, 0, 0, 0)
	*t = redisExpression(rReloc, pc)
	cc := redisConstructor{table: t, value: redisExpression(rVoid, 0)}
	f.nextreg(t)
	p.expect("{")
	for {
		if p.current.kind == "}" {
			break
		}
		f.closelist(&cc)
		switch p.current.kind {
		case "<name>":
			if p.peek().kind == "=" {
				p.recordfield(&cc)
			} else {
				p.listfield(&cc)
			}
		case "[":
			p.recordfield(&cc)
		default:
			p.listfield(&cc)
		}
		if !p.test(",") && !p.test(";") {
			break
		}
	}
	p.match("}", "{", line)
	f.lastlist(&cc)
	f.p.code[pc].setB(redisInt2fb(cc.array))
	f.p.code[pc].setC(redisInt2fb(cc.hash))
}
func (p *redisParser) parlist() {
	f := p.fs
	nparams := 0
	f.p.vararg = 0
	if p.current.kind != ")" {
		for {
			switch p.current.kind {
			case "<name>":
				p.newlocal(p.name(), nparams)
				nparams++
			case "...":
				p.next()
				p.newlocal("arg", nparams)
				nparams++
				f.p.vararg = 7
			default:
				p.fail("<name> or '...' expected")
			}
			if f.p.vararg != 0 || !p.test(",") {
				break
			}
		}
	}
	p.adjustlocals(nparams)
	f.p.parameters = f.active - (f.p.vararg & 1)
	f.reserve(f.active)
}
func (p *redisParser) body(e *redisExp, self bool, line int) {
	f := p.openfunc()
	f.p.line = line
	p.expect("(")
	if self {
		p.newlocal("self", 0)
		p.adjustlocals(1)
	}
	p.parlist()
	p.expect(")")
	p.chunk()
	f.p.lastLine = p.lexer.line
	p.match("end", "function", line)
	p.closefunc()
	p.pushclosure(f, e)
}
func (p *redisParser) explist(e *redisExp) int {
	n := 1
	p.expr(e)
	for p.test(",") {
		p.fs.nextreg(e)
		p.expr(e)
		n++
	}
	return n
}
func (p *redisParser) funcargs(fexp *redisExp) {
	f := p.fs
	args := redisExpression(rVoid, 0)
	line := p.lexer.line
	switch p.current.kind {
	case "(":
		if line != p.lastLine {
			p.fail("ambiguous syntax (function call x new statement)")
		}
		p.next()
		if p.current.kind != ")" {
			p.explist(&args)
			f.setreturns(&args, -1)
		}
		p.match(")", "(", line)
	case "{":
		p.constructor(&args)
	case "<string>":
		p.stringexp(&args, p.current.value.Text())
		p.next()
	default:
		p.fail("function arguments expected")
	}
	base := fexp.info
	nparams := -1
	if !redisMultret(args.k) {
		if args.k != rVoid {
			f.nextreg(&args)
		}
		nparams = f.free - (base + 1)
	}
	*fexp = redisExpression(rCallExp, f.abc(rCall, base, nparams+1, 2))
	f.fixline(line)
	f.free = base + 1
}
func (p *redisParser) prefixexp(v *redisExp) {
	switch p.current.kind {
	case "(":
		line := p.lexer.line
		p.next()
		p.expr(v)
		p.match(")", "(", line)
		p.fs.discharge(v)
	case "<name>":
		p.singlevar(v)
	default:
		p.fail("unexpected symbol")
	}
}
func (p *redisParser) primary(v *redisExp) {
	f := p.fs
	p.prefixexp(v)
	for {
		switch p.current.kind {
		case ".":
			p.field(v)
		case "[":
			var key redisExp
			f.anyreg(v)
			p.index(&key)
			f.indexed(v, &key)
		case ":":
			var key redisExp
			p.next()
			p.checkname(&key)
			f.self(v, &key)
			p.funcargs(v)
		case "(", "<string>", "{":
			f.nextreg(v)
			p.funcargs(v)
		default:
			return
		}
	}
}
func (p *redisParser) simple(v *redisExp) {
	switch p.current.kind {
	case "<number>":
		*v = redisExpression(rNumber, 0)
		v.number = p.current.value.Float()
	case "<string>":
		p.stringexp(v, p.current.value.Text())
	case "nil":
		*v = redisExpression(rNil, 0)
	case "true":
		*v = redisExpression(rTrue, 0)
	case "false":
		*v = redisExpression(rFalse, 0)
	case "...":
		if p.fs.p.vararg == 0 {
			p.fail("cannot use '...' outside a vararg function")
		}
		p.fs.p.vararg &^= 4
		*v = redisExpression(rVarargExp, p.fs.abc(rVararg, 0, 1, 0))
	case "{":
		p.constructor(v)
		return
	case "function":
		p.next()
		p.body(v, false, p.lexer.line)
		return
	default:
		p.primary(v)
		return
	}
	p.next()
}
func redisPriority(op string) (int, int) {
	switch op {
	case "+", "-":
		return 6, 6
	case "*", "/", "%":
		return 7, 7
	case "^":
		return 10, 9
	case "..":
		return 5, 4
	case "~=", "==", "<", "<=", ">", ">=":
		return 3, 3
	case "and":
		return 2, 2
	case "or":
		return 1, 1
	}
	return 0, 0
}
func (p *redisParser) subexpr(v *redisExp, limit int) string {
	p.enterlevel()
	op := p.current.kind
	if op == "-" || op == "not" || op == "#" {
		p.next()
		p.subexpr(v, 8)
		p.fs.prefix(op, v)
	} else {
		p.simple(v)
	}
	op = p.current.kind
	for {
		left, right := redisPriority(op)
		if left <= limit {
			break
		}
		p.next()
		p.fs.infix(op, v)
		var v2 redisExp
		next := p.subexpr(&v2, right)
		p.fs.postfix(op, v, &v2)
		op = next
	}
	p.depth--
	return op
}
func (p *redisParser) expr(v *redisExp) { p.subexpr(v, 0) }
func redisBlockFollow(kind string) bool {
	return kind == "else" || kind == "elseif" || kind == "end" || kind == "until" || kind == "<eof>"
}
func (p *redisParser) block() { f := p.fs; f.enterblock(false); p.chunk(); f.leaveblock() }

type redisAssignment struct {
	previous *redisAssignment
	value    redisExp
}

func (p *redisParser) conflict(lh *redisAssignment, v *redisExp) {
	f := p.fs
	extra := f.free
	conflict := false
	for ; lh != nil; lh = lh.previous {
		if lh.value.k == rIndexed {
			if lh.value.info == v.info {
				conflict = true
				lh.value.info = extra
			}
			if lh.value.aux == v.info {
				conflict = true
				lh.value.aux = extra
			}
		}
	}
	if conflict {
		f.abc(rMove, f.free, v.info, 0)
		f.reserve(1)
	}
}
func (p *redisParser) assignment(lh *redisAssignment, nvars int) {
	if lh.value.k < rLocal || lh.value.k > rIndexed {
		p.fail("syntax error")
	}
	var e redisExp
	if p.test(",") {
		next := redisAssignment{previous: lh}
		p.primary(&next.value)
		if next.value.k == rLocal {
			p.conflict(lh, &next.value)
		}
		p.fs.limit(nvars, 200-p.depth, "variables in assignment")
		p.assignment(&next, nvars+1)
	} else {
		p.expect("=")
		nexps := p.explist(&e)
		if nexps != nvars {
			p.adjustassign(nvars, nexps, &e)
			if nexps > nvars {
				p.fs.free -= nexps - nvars
			}
		} else {
			p.fs.oneret(&e)
			p.fs.store(&lh.value, &e)
			return
		}
	}
	e = redisExpression(rRegister, p.fs.free-1)
	p.fs.store(&lh.value, &e)
}
func (p *redisParser) condition() int {
	var v redisExp
	p.expr(&v)
	if v.k == rNil {
		v.k = rFalse
	}
	p.fs.goiftrue(&v)
	return v.f
}
func (p *redisParser) breakstat() {
	f := p.fs
	b := f.block
	upvalue := false
	for b != nil && !b.loop {
		upvalue = upvalue || b.upvalue
		b = b.previous
	}
	if b == nil {
		p.fail("no loop to break")
	}
	if upvalue {
		f.abc(rClose, b.locals, 0, 0)
	}
	f.concat(&b.breaks, f.jump())
}
func (p *redisParser) whilestat(line int) {
	f := p.fs
	p.next()
	start := f.label()
	exit := p.condition()
	f.enterblock(true)
	p.expect("do")
	p.block()
	f.patchlist(f.jump(), start)
	p.match("end", "while", line)
	f.leaveblock()
	f.patchhere(exit)
}
func (p *redisParser) repeatstat(line int) {
	f := p.fs
	start := f.label()
	f.enterblock(true)
	scope := f.enterblock(false)
	p.next()
	p.chunk()
	p.match("until", "repeat", line)
	exit := p.condition()
	if !scope.upvalue {
		f.leaveblock()
		f.patchlist(exit, start)
	} else {
		p.breakstat()
		f.patchhere(exit)
		f.leaveblock()
		f.patchlist(f.jump(), start)
	}
	f.leaveblock()
}
func (p *redisParser) exp1() redisExpKind {
	var e redisExp
	p.expr(&e)
	k := e.k
	p.fs.nextreg(&e)
	return k
}
func (p *redisParser) forbody(base, line, nvars int, numeric bool) {
	f := p.fs
	p.adjustlocals(3)
	p.expect("do")
	prep := 0
	if numeric {
		prep = f.abx(rForPrep, base, redisMaxJump-1)
	} else {
		prep = f.jump()
	}
	f.enterblock(false)
	p.adjustlocals(nvars)
	f.reserve(nvars)
	p.block()
	f.leaveblock()
	f.patchhere(prep)
	end := 0
	if numeric {
		end = f.abx(rForLoop, base, redisMaxJump-1)
	} else {
		end = f.abc(rTForLoop, base, 0, nvars)
	}
	f.fixline(line)
	if !numeric {
		end = f.jump()
	}
	f.patchlist(end, prep+1)
}
func (p *redisParser) fornum(name string, line int) {
	f := p.fs
	base := f.free
	p.newlocal("(for index)", 0)
	p.newlocal("(for limit)", 1)
	p.newlocal("(for step)", 2)
	p.newlocal(name, 3)
	p.expect("=")
	p.exp1()
	p.expect(",")
	p.exp1()
	if p.test(",") {
		p.exp1()
	} else {
		f.abx(rLoadK, f.free, f.addconstant(Number(1)))
		f.reserve(1)
	}
	p.forbody(base, line, 1, true)
}
func (p *redisParser) forlist(name string) {
	f := p.fs
	base := f.free
	p.newlocal("(for generator)", 0)
	p.newlocal("(for state)", 1)
	p.newlocal("(for control)", 2)
	p.newlocal(name, 3)
	nvars := 4
	for p.test(",") {
		p.newlocal(p.name(), nvars)
		nvars++
	}
	p.expect("in")
	line := p.lexer.line
	var e redisExp
	nexps := p.explist(&e)
	p.adjustassign(3, nexps, &e)
	f.checkstack(3)
	p.forbody(base, line, nvars-3, false)
}
func (p *redisParser) forstat(line int) {
	f := p.fs
	f.enterblock(true)
	p.next()
	name := p.name()
	switch p.current.kind {
	case "=":
		p.fornum(name, line)
	case ",", "in":
		p.forlist(name)
	default:
		p.fail("'=' or 'in' expected")
	}
	p.match("end", "for", line)
	f.leaveblock()
}
func (p *redisParser) thenblock() int {
	p.next()
	exit := p.condition()
	p.expect("then")
	p.block()
	return exit
}
func (p *redisParser) ifstat(line int) {
	f := p.fs
	escape := -1
	flist := p.thenblock()
	for p.current.kind == "elseif" {
		f.concat(&escape, f.jump())
		f.patchhere(flist)
		flist = p.thenblock()
	}
	if p.current.kind == "else" {
		f.concat(&escape, f.jump())
		f.patchhere(flist)
		p.next()
		p.block()
	} else {
		f.concat(&escape, flist)
	}
	f.patchhere(escape)
	p.match("end", "if", line)
}
func (p *redisParser) localfunc() {
	f := p.fs
	p.newlocal(p.name(), 0)
	v := redisExpression(rLocal, f.free)
	f.reserve(1)
	p.adjustlocals(1)
	var body redisExp
	p.body(&body, false, p.lexer.line)
	f.store(&v, &body)
	f.p.locals[f.actvar[f.active-1]].start = f.pc()
}
func (p *redisParser) localstat() {
	nvars := 0
	for {
		p.newlocal(p.name(), nvars)
		nvars++
		if !p.test(",") {
			break
		}
	}
	e := redisExpression(rVoid, 0)
	nexps := 0
	if p.test("=") {
		nexps = p.explist(&e)
	}
	p.adjustassign(nvars, nexps, &e)
	p.adjustlocals(nvars)
}
func (p *redisParser) funcstat(line int) {
	p.next()
	var v, b redisExp
	p.singlevar(&v)
	for p.current.kind == "." {
		p.field(&v)
	}
	self := false
	if p.current.kind == ":" {
		self = true
		p.field(&v)
	}
	p.body(&b, self, line)
	p.fs.store(&v, &b)
	p.fs.fixline(line)
}
func (p *redisParser) exprstat() {
	f := p.fs
	var v redisAssignment
	p.primary(&v.value)
	if v.value.k == rCallExp {
		f.p.code[v.value.info].setC(1)
	} else {
		p.assignment(&v, 1)
	}
}
func (p *redisParser) retstat() {
	f := p.fs
	p.next()
	first, nret := 0, 0
	if !redisBlockFollow(p.current.kind) && p.current.kind != ";" {
		var e redisExp
		nret = p.explist(&e)
		if redisMultret(e.k) {
			f.setreturns(&e, -1)
			if e.k == rCallExp && nret == 1 {
				i := &f.p.code[e.info]
				*i = *i&^63 | redisInstruction(rTailCall)
			}
			first = f.active
			nret = -1
		} else if nret == 1 {
			first = f.anyreg(&e)
		} else {
			f.nextreg(&e)
			first = f.active
		}
	}
	f.ret(first, nret)
}
func (p *redisParser) statement() bool {
	line := p.lexer.line
	switch p.current.kind {
	case "if":
		p.ifstat(line)
	case "while":
		p.whilestat(line)
	case "do":
		p.next()
		p.block()
		p.match("end", "do", line)
	case "for":
		p.forstat(line)
	case "repeat":
		p.repeatstat(line)
	case "function":
		p.funcstat(line)
	case "local":
		p.next()
		if p.test("function") {
			p.localfunc()
		} else {
			p.localstat()
		}
	case "return":
		p.retstat()
		return true
	case "break":
		p.next()
		p.breakstat()
		return true
	default:
		p.exprstat()
	}
	return false
}
func (p *redisParser) chunk() {
	p.enterlevel()
	last := false
	for !last && !redisBlockFollow(p.current.kind) {
		last = p.statement()
		p.test(";")
		p.fs.free = p.fs.active
	}
	p.depth--
}
