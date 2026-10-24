package lua

import "fmt"

type opcode uint8

const (
	opConstant opcode = iota
	opMove
	opGetGlobal
	opSetGlobal
	opGetUpvalue
	opSetUpvalue
	opGetTable
	opSetTable
	opNewTable
	opSetList
	opClosure
	opVararg
	opUnary
	opBinary
	opJump
	opJumpFalse
	opJumpTrue
	opCall
	opTailCall
	opReturn
	opClose
	opForPrep
	opForLoop
	opForCall
	opPoll
)

type nameInfo struct {
	kind string
	name string
}

type instruction struct {
	op          opcode
	a, b, c, d  int
	line        int
	text        string
	left, right nameInfo
	method      bool
}

type upvalueDescriptor struct {
	local bool
	index int
	name  string
}

type Proto struct {
	code         []uint64
	sites        []uint32
	metadata     []instruction
	lines        []int32
	constants    []Value
	children     []*Proto
	upvalues     []upvalueDescriptor
	chunk        string
	parameters   int
	maxRegisters int
	vararg       bool
	usesVararg   bool
	argRegister  int
	line         int
	needsArg     bool
}

type localVariable struct {
	name     string
	register int
}

type compiler struct {
	code            []instruction
	proto           *Proto
	parent          *compiler
	constants       map[Value]int
	stringConstants map[string]int
	captured        map[int]bool
	locals          []localVariable
	next            int
	breaks          [][]int
	position        token
}

func Compile(source []byte, chunk string) (*Proto, error) {
	return compile(source, chunk, 0)
}

func compile(source []byte, chunk string, depth int) (proto *Proto, err error) {
	defer func() {
		if raised := recover(); raised != nil {
			if failure, ok := raised.(*Error); ok {
				err = failure
			} else {
				err = &Error{Message: fmt.Sprint(raised), Internal: true}
			}
			proto = nil
		}
	}()
	if chunk == "" {
		chunk = string(source)
	}
	p := &parser{depth: depth, lexer: lexer{source: string(source), chunk: chunk, line: 1}}
	p.advance()
	f := &syntaxFunction{main: true, vararg: true, line: 1, body: p.block()}
	p.expect("<eof>")
	c := &compiler{proto: &Proto{chunk: chunk, argRegister: -1}}
	c.compileFunction(f)
	return c.proto, nil
}

func (c *compiler) reserve(next int) {
	if next > c.next {
		c.next = next
	}
	if c.next > c.proto.maxRegisters {
		c.proto.maxRegisters = c.next
	}
	if c.next >= 250 {
		panic(syntaxError(c.proto.chunk, max(1, c.position.line), "function or expression too complex near '"+c.position.text+"'"))
	}
}

func (c *compiler) temp() int { index := c.next; c.reserve(index + 1); return index }

func (c *compiler) emit(i instruction) int {
	if len(c.code) > 0 && len(c.code)%vmPollStride == 0 {
		c.code = append(c.code, instruction{op: opPoll, line: i.line})
	}
	c.code = append(c.code, i)
	return len(c.code) - 1
}

func (c *compiler) constant(value Value) int {
	if value.Type() == TypeString {
		if c.stringConstants == nil {
			c.stringConstants = make(map[string]int)
		}
		if index, exists := c.stringConstants[value.Text()]; exists {
			return index
		}
		index := len(c.proto.constants)
		c.proto.constants = append(c.proto.constants, value)
		c.stringConstants[value.Text()] = index
		return index
	}
	if c.constants == nil {
		c.constants = make(map[Value]int)
	}
	if index, exists := c.constants[value]; exists {
		return index
	}
	c.proto.constants = append(c.proto.constants, value)
	index := len(c.proto.constants) - 1
	c.constants[value] = index
	return index
}

func (c *compiler) addLocal(name token) int {
	if len(c.locals) >= 200 {
		where := "main function"
		if c.proto.line > 0 {
			where = fmt.Sprintf("function at line %d", c.proto.line)
		}
		panic(syntaxError(c.proto.chunk, name.line, where+" has more than 200 local variables"))
	}
	index := c.temp()
	c.locals = append(c.locals, localVariable{name: name.text, register: index})
	return index
}

func (c *compiler) findLocal(name string) (int, bool) {
	for i := len(c.locals) - 1; i >= 0; i-- {
		if c.locals[i].name == name {
			return c.locals[i].register, true
		}
	}
	return 0, false
}

func (c *compiler) capture(name string) (int, bool) {
	for i, descriptor := range c.proto.upvalues {
		if descriptor.name == name {
			return i, true
		}
	}
	if c.parent == nil {
		return 0, false
	}
	index, local := c.parent.findLocal(name)
	if local && index == c.parent.proto.argRegister {
		c.parent.proto.needsArg = true
	}
	if local {
		if c.parent.captured == nil {
			c.parent.captured = make(map[int]bool)
		}
		c.parent.captured[index] = true
	}
	if !local {
		var ok bool
		index, ok = c.parent.capture(name)
		if !ok {
			return 0, false
		}
	}
	if len(c.proto.upvalues) >= 60 {
		panic(syntaxError(c.proto.chunk, max(1, c.proto.line), fmt.Sprintf("function at line %d has more than 60 upvalues", max(1, c.proto.line))))
	}
	c.proto.upvalues = append(c.proto.upvalues, upvalueDescriptor{local: local, index: index, name: name})
	return len(c.proto.upvalues) - 1, true
}

func (c *compiler) resolve(name string) (opcode, int, nameInfo) {
	if index, ok := c.findLocal(name); ok {
		if index == c.proto.argRegister {
			c.proto.needsArg = true
		}
		return opMove, index, nameInfo{kind: "local", name: name}
	}
	if index, ok := c.capture(name); ok {
		return opGetUpvalue, index, nameInfo{kind: "upvalue", name: name}
	}
	return opGetGlobal, c.constant(String(name)), nameInfo{kind: "global", name: name}
}

func (c *compiler) name(e *expression) nameInfo {
	for {
		if e.kind == "binary" && e.left.kind == "literal" {
			if e.name == "and" && e.left.value.Truth() || e.name == "or" && !e.left.value.Truth() {
				e = e.right
				continue
			}
		}
		if e.kind == "name" {
			_, _, info := c.resolve(e.name)
			return info
		}
		if e.kind == "index" {
			name := "?"
			if e.right.kind == "literal" && e.right.value.Type() == TypeString {
				name = e.right.value.Text()
			}
			return nameInfo{kind: "field", name: name}
		}
		if e.kind == "paren" {
			e = e.left
			continue
		}
		return nameInfo{}
	}
}

func (c *compiler) pure(e *expression) bool {
	if e.kind == "literal" {
		return true
	}
	if e.kind == "name" {
		_, ok := c.findLocal(e.name)
		return ok
	}
	return false
}

func (c *compiler) operand(e *expression, direct bool) int {
	if direct {
		if e.kind == "literal" {
			return -1 - c.constant(e.value)
		}
		if e.kind == "name" {
			if index, ok := c.findLocal(e.name); ok {
				return index
			}
		}
	}
	index := c.temp()
	c.expression(e, index, 1)
	return index
}

func (c *compiler) compileFunction(f *syntaxFunction) {
	if !f.main {
		c.proto.line = f.line
	}
	c.proto.parameters = len(f.params)
	c.proto.vararg = f.vararg
	for _, param := range f.params {
		c.addLocal(param)
	}
	if f.vararg && !f.main {
		c.proto.argRegister = c.addLocal(token{text: "arg", line: f.line})
	}
	c.block(f.body, false)
	c.emit(instruction{op: opReturn, a: 0, b: 0, line: f.line})
	if c.proto.maxRegisters < 2 {
		c.proto.maxRegisters = 2
	}
	c.freeze()
}

func (c *compiler) block(body []*statement, scoped bool) {
	locals, registers := len(c.locals), c.next
	for _, s := range body {
		c.statement(s)
	}
	if scoped {
		c.close(registers, 0)
		c.locals = c.locals[:locals]
		c.next = registers
	}
}

func (c *compiler) close(base, line int) {
	for index := range c.captured {
		if index >= base {
			c.emit(instruction{op: opClose, a: base, line: line})
			return
		}
	}
}

func (c *compiler) values(expressions []*expression, base, count int) {
	if count >= 0 {
		c.reserve(base + count)
	}
	for i, e := range expressions {
		want := 1
		if i == len(expressions)-1 {
			if count < 0 {
				want = -1
			} else {
				want = max(0, count-i)
			}
		}
		c.expression(e, base+i, want)
	}
	for i := 0; len(expressions) == 0 && i < count; i++ {
		c.emit(instruction{op: opConstant, a: base + i, b: c.constant(Nil())})
	}
}

func (c *compiler) expression(e *expression, dest, want int) {
	c.position = e.token
	before := c.next
	c.reserve(dest + max(1, want))
	defer func() { c.next = max(before, dest+max(1, want)) }()
	i := instruction{a: dest, line: e.token.line}
	switch e.kind {
	case "literal":
		i.op = opConstant
		i.b = c.constant(e.value)
		c.emit(i)
	case "name":
		i.op, i.b, i.left = c.resolve(e.name)
		c.emit(i)
	case "paren":
		c.expression(e.left, dest, 1)
	case "index":
		c.chain(e, dest, 1, false)
	case "function":
		child := &compiler{parent: c, proto: &Proto{chunk: c.proto.chunk, argRegister: -1}}
		child.compileFunction(e.function)
		i.op = opClosure
		i.b = len(c.proto.children)
		c.proto.children = append(c.proto.children, child.proto)
		c.emit(i)
	case "vararg":
		if !c.proto.vararg {
			panic(syntaxError(c.proto.chunk, e.token.line, "cannot use '...' outside a vararg function near '...'"))
		}
		c.proto.usesVararg = true
		i.op = opVararg
		i.b = want
		c.emit(i)
		return
	case "unary":
		if e.endLine > 0 {
			i.line = e.endLine
		}
		if e.name == "-" && e.left.kind == "literal" && e.left.value.Type() == TypeNumber {
			i.op = opConstant
			i.b = c.constant(Number(-e.left.value.number))
			c.emit(i)
			break
		}
		i.op = opUnary
		i.text = e.name
		i.b = c.temp()
		i.left = c.name(e.left)
		c.expression(e.left, i.b, 1)
		c.emit(i)
	case "binary":
		c.chain(e, dest, 1, false)
	case "call":
		c.call(e, dest, want, false)
		return
	case "table":
		arraySize, hashSize := 0, 0
		for _, field := range e.fields {
			if field.key == nil {
				arraySize++
			} else {
				hashSize++
			}
		}
		i.op = opNewTable
		i.b = floatingSize(arraySize)
		i.c = floatingSize(hashSize)
		c.emit(i)
		listBase := c.next
		c.reserve(listBase + min(arraySize, 50))
		listIndex := 0
		listOffset := 0
		for index, field := range e.fields {
			if field.key == nil {
				count := 1
				if index == len(e.fields)-1 {
					count = -1
				}
				c.expression(field.value, listBase+listIndex, count)
				listIndex++
				if count == -1 && (field.value.kind == "call" || field.value.kind == "vararg") {
					c.emit(instruction{op: opSetList, a: dest, b: listBase, c: -1, d: listOffset, line: e.token.line})
					return
				}
				if listIndex == 50 {
					c.emit(instruction{op: opSetList, a: dest, b: listBase, c: listIndex, d: listOffset, line: e.token.line})
					listOffset += listIndex
					listIndex = 0
				}
			} else {
				before := c.next
				key, value := c.temp(), c.temp()
				c.expression(field.key, key, 1)
				c.expression(field.value, value, 1)
				c.emit(instruction{op: opSetTable, a: dest, b: key, c: value, line: e.token.line})
				c.next = before
			}
		}
		c.emit(instruction{op: opSetList, a: dest, b: listBase, c: listIndex, d: listOffset, line: e.token.line})
	}
	if want > 1 {
		for offset := 1; offset < want; offset++ {
			c.emit(instruction{op: opConstant, a: dest + offset, b: c.constant(Nil()), line: e.token.line})
		}
	}
}

func floatingSize(size int) int {
	exponent := 0
	for size >= 16 {
		size = (size + 1) >> 1
		exponent++
	}
	return size << exponent
}

func (c *compiler) call(e *expression, dest, want int, tail bool) {
	c.chain(e, dest, want, tail)
}

func (c *compiler) chain(e *expression, dest, want int, tail bool) {
	var links []*expression
	root := e
	for root.kind == "binary" || root.kind == "index" || root.kind == "call" {
		links = append(links, root)
		if root.kind == "call" && root.method {
			root = root.left.left
		} else {
			root = root.left
		}
	}
	first := links[len(links)-1]
	if len(links) == 1 && first.kind == "call" {
		c.callLink(first, dest, want, tail, false)
		return
	}
	before := c.next
	c.reserve(dest + max(1, want))
	base := c.next
	direct := first.kind == "binary" && first.name != "and" && first.name != "or" && c.pure(first.left) && c.pure(first.right)
	if first.kind != "call" && !direct {
		c.expression(root, dest, 1)
	}
	for index := len(links) - 1; index >= 0; index-- {
		link := links[index]
		c.next = base
		c.position = link.token
		i := instruction{a: dest, b: dest, line: link.token.line}
		if link.endLine > 0 {
			i.line = link.endLine
		}
		switch link.kind {
		case "binary":
			if link.name == "and" || link.name == "or" {
				i.op = opJumpFalse
				if link.name == "or" {
					i.op = opJumpTrue
				}
				jump := c.emit(i)
				c.expression(link.right, dest, 1)
				c.code[jump].b = len(c.code)
			} else {
				i.op, i.text = opBinary, link.name
				i.left, i.right = c.name(link.left), c.name(link.right)
				if index == len(links)-1 && direct {
					i.b = c.operand(link.left, true)
				}
				i.c = c.operand(link.right, true)
				c.emit(i)
			}
		case "index":
			i.op, i.left = opGetTable, c.name(link.left)
			i.c = c.operand(link.right, false)
			c.emit(i)
		case "call":
			count := 1
			if index == 0 {
				count = want
			}
			c.callLink(link, dest, count, tail && index == 0, index != len(links)-1)
		}
	}
	c.next = max(before, dest+max(1, want))
}

func (c *compiler) callLink(e *expression, dest, want int, tail, prepared bool) {
	base := c.temp()
	info := c.name(e.left)
	argCount := len(e.args)
	if e.method {
		c.reserve(base + 2)
		if prepared {
			c.emit(instruction{op: opMove, a: base + 1, b: dest, line: e.token.line})
		} else {
			c.expression(e.left.left, base+1, 1)
		}
		key := c.temp()
		c.expression(e.left.right, key, 1)
		c.emit(instruction{op: opGetTable, a: base, b: base + 1, c: key, line: e.token.line, left: c.name(e.left.left)})
		info.kind = "method"
		argCount++
	} else {
		c.reserve(base + 1)
		if prepared {
			c.emit(instruction{op: opMove, a: base, b: dest, line: e.token.line})
		} else {
			c.expression(e.left, base, 1)
		}
	}
	argBase := base + 1
	if e.method {
		argBase++
	}
	for index, arg := range e.args {
		c.position = arg.token
		if index == len(e.args)-1 {
			c.position = e.after
		}
		c.reserve(argBase + index + 1)
		count := 1
		if index == len(e.args)-1 && (arg.kind == "call" || arg.kind == "vararg") {
			count = -1
			argCount = -1
		}
		c.expression(arg, argBase+index, count)
	}
	op := opCall
	if tail {
		op = opTailCall
	}
	c.emit(instruction{op: op, a: base, b: argCount, c: want, d: dest, line: e.token.line, left: info, method: e.method})
}

func (c *compiler) statement(s *statement) {
	base := c.next
	switch s.kind {
	case "local":
		c.values(s.values, base, len(s.names))
		c.next = base
		for _, name := range s.names {
			c.addLocal(name)
		}
		return
	case "localfunction":
		reg := c.addLocal(s.names[0])
		c.expression(s.values[0], reg, 1)
		return
	case "assign":
		if len(s.targets) == 1 && len(s.values) == 1 && s.targets[0].kind == "name" {
			value := s.values[0]
			if index, ok := c.findLocal(s.targets[0].name); ok && value.kind == "binary" && value.name != "and" && value.name != "or" && c.pure(value.left) && c.pure(value.right) {
				c.expression(value, index, 1)
				c.next = base
				return
			}
		}
		targets := make([]instruction, len(s.targets))
		for index, target := range s.targets {
			i := instruction{line: s.endLine}
			if target.kind == "name" {
				i.op, i.a, i.left = c.resolve(target.name)
				switch i.op {
				case opMove:
				case opGetGlobal:
					i.op = opSetGlobal
				case opGetUpvalue:
					i.op = opSetUpvalue
				}
			} else {
				i.op = opSetTable
				i.a = c.temp()
				i.b = c.temp()
				i.left = c.name(target.left)
				c.expression(target.left, i.a, 1)
				c.expression(target.right, i.b, 1)
			}
			targets[index] = i
		}
		values := c.next
		c.values(s.values, values, len(targets))
		for index := len(targets) - 1; index >= 0; index-- {
			i := targets[index]
			if i.op == opSetTable {
				i.c = values + index
			} else {
				i.b = values + index
			}
			c.emit(i)
		}
	case "call":
		c.call(s.values[0], base, 0, false)
	case "return":
		if len(s.values) == 1 && s.values[0].kind == "call" {
			c.call(s.values[0], base, -1, true)
		} else {
			c.values(s.values, base, -1)
			count := len(s.values)
			if count > 0 && (s.values[count-1].kind == "call" || s.values[count-1].kind == "vararg") {
				count = -1
			}
			c.emit(instruction{op: opReturn, a: base, b: count, line: s.line})
		}
	case "do":
		c.block(s.body, true)
	case "if":
		var ends []int
		for _, branch := range s.branches {
			c.expression(branch.condition, base, 1)
			jump := c.emit(instruction{op: opJumpFalse, a: base, line: s.line})
			c.next = base
			c.block(branch.body, true)
			ends = append(ends, c.emit(instruction{op: opJump}))
			c.code[jump].b = len(c.code)
		}
		c.block(s.body, true)
		for _, end := range ends {
			c.code[end].b = len(c.code)
		}
	case "while", "repeat", "fornum", "forin":
		c.loop(s, base)
	case "break":
		index := len(c.breaks) - 1
		c.breaks[index] = append(c.breaks[index], c.emit(instruction{op: opJump, line: s.line}))
	}
	c.next = base
}

func (c *compiler) loop(s *statement, base int) {
	c.breaks = append(c.breaks, nil)
	localCount := len(c.locals)
	start := len(c.code)
	var exit int
	switch s.kind {
	case "while":
		c.expression(s.values[0], base, 1)
		exit = c.emit(instruction{op: opJumpFalse, a: base, line: s.line})
		c.next = base
		c.block(s.body, true)
		c.emit(instruction{op: opJump, b: start, line: s.line})
	case "repeat":
		c.block(s.body, false)
		condition := c.temp()
		c.expression(s.values[0], condition, 1)
		c.close(base, s.line)
		c.emit(instruction{op: opJumpFalse, a: condition, b: start, line: s.line})
		exit = -1
	case "fornum":
		c.values(s.values, base, 3)
		c.next = base
		for _, name := range []string{"(for index)", "(for limit)", "(for step)"} {
			c.addLocal(token{text: name, line: s.line})
		}
		variable := c.addLocal(s.names[0])
		prep := c.emit(instruction{op: opForPrep, a: base, line: s.prepLine})
		start = len(c.code)
		c.block(s.body, true)
		c.close(variable, s.line)
		c.code[prep].b = len(c.code)
		c.emit(instruction{op: opForLoop, a: base, b: start, line: s.line})
		exit = -1
	case "forin":
		c.values(s.values, base, 3)
		c.next = base
		for _, name := range []string{"(for generator)", "(for state)", "(for control)"} {
			c.addLocal(token{text: name, line: s.line})
		}
		for _, name := range s.names {
			c.addLocal(name)
		}
		jump := c.emit(instruction{op: opJump, line: s.line})
		start = len(c.code)
		c.block(s.body, true)
		c.close(base+3, s.line)
		c.code[jump].b = len(c.code)
		c.emit(instruction{op: opForCall, a: base, b: start, c: len(s.names), line: s.line})
		exit = -1
	}
	if exit >= 0 {
		c.code[exit].b = len(c.code)
	}
	end := len(c.code)
	c.close(base, s.line)
	for _, jump := range c.breaks[len(c.breaks)-1] {
		c.code[jump].b = end
		c.code[jump].a = base + 1
	}
	c.breaks = c.breaks[:len(c.breaks)-1]
	c.locals = c.locals[:localCount]
	c.next = base
}
