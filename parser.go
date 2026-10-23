package lua

import "fmt"

type expression struct {
	kind     string
	token    token
	value    Value
	left     *expression
	right    *expression
	args     []*expression
	fields   []tableField
	function *syntaxFunction
	name     string
	method   bool
	endLine  int
}

type tableField struct {
	key   *expression
	value *expression
}

type statement struct {
	kind     string
	line     int
	endLine  int
	prepLine int
	names    []token
	values   []*expression
	targets  []*expression
	body     []*statement
	branches []branch
}

type branch struct {
	condition *expression
	body      []*statement
}

type syntaxFunction struct {
	params []token
	vararg bool
	main   bool
	line   int
	body   []*statement
}

type parser struct {
	lexer     lexer
	current   token
	lookahead *token
	previous  token
	depth     int
}

func (p *parser) advance() token {
	old := p.current
	p.previous = old
	if p.lookahead != nil {
		p.current = *p.lookahead
		p.lookahead = nil
	} else {
		p.current = p.lexer.next()
	}
	return old
}

func (p *parser) lastLine() int {
	if p.previous.endLine > 0 {
		return p.previous.endLine
	}
	return p.previous.line
}

func (p *parser) peek() token {
	if p.lookahead == nil {
		next := p.lexer.next()
		p.lookahead = &next
	}
	return *p.lookahead
}

func (p *parser) fail(message string) {
	panic(locatedError(p.lexer.chunk, p.current.line, message+" near '"+p.current.text+"'"))
}

func (p *parser) expect(kind string) token {
	if p.current.kind != kind {
		p.fail("'" + kind + "' expected")
	}
	return p.advance()
}

func (p *parser) accept(kind string) bool {
	if p.current.kind != kind {
		return false
	}
	p.advance()
	return true
}

func (p *parser) enter() {
	p.depth++
	if p.depth >= 200 {
		panic(locatedError(p.lexer.chunk, p.current.line, "chunk has too many syntax levels"))
	}
}

func (p *parser) close(kind, opener string, line int) {
	if p.current.kind != kind {
		if p.current.line == line {
			p.fail("'" + kind + "' expected")
		}
		p.fail(fmt.Sprintf("'%s' expected (to close '%s' at line %d)", kind, opener, line))
	}
	p.advance()
}

func (p *parser) block() []*statement {
	p.enter()
	defer func() { p.depth-- }()
	var out []*statement
	for {
		switch p.current.kind {
		case "end", "else", "elseif", "until", "<eof>":
			return out
		}
		s := p.statement()
		out = append(out, s)
		p.accept(";")
		if s.kind == "return" || s.kind == "break" {
			return out
		}
	}
}

func (p *parser) names() []token {
	names := []token{p.expect("<name>")}
	for p.accept(",") {
		names = append(names, p.expect("<name>"))
	}
	return names
}

func (p *parser) statement() *statement {
	start := p.current
	s := &statement{kind: start.kind, line: start.line}
	switch start.kind {
	case "local":
		p.advance()
		if p.accept("function") {
			s.kind = "localfunction"
			s.names = []token{p.expect("<name>")}
			s.values = []*expression{{kind: "function", token: start, function: p.function(start.line, false)}}
		} else {
			s.names = p.names()
			if p.accept("=") {
				s.values = p.expressionList()
			}
		}
	case "function":
		p.advance()
		name := p.expect("<name>")
		target := &expression{kind: "name", name: name.text, token: name}
		for p.accept(".") {
			key := p.expect("<name>")
			target = &expression{kind: "index", left: target, right: &expression{kind: "literal", value: String(key.text), token: key}, token: key}
		}
		method := p.accept(":")
		if method {
			key := p.expect("<name>")
			target = &expression{kind: "index", left: target, right: &expression{kind: "literal", value: String(key.text), token: key}, token: key}
		}
		s.kind = "assign"
		s.targets = []*expression{target}
		s.values = []*expression{{kind: "function", token: start, function: p.function(start.line, method)}}
	case "do":
		p.advance()
		s.body = p.block()
		p.close("end", "do", start.line)
	case "while":
		p.advance()
		s.values = []*expression{p.expr(0)}
		p.expect("do")
		s.body = p.block()
		p.close("end", "while", start.line)
	case "repeat":
		p.advance()
		s.body = p.block()
		p.close("until", "repeat", start.line)
		s.values = []*expression{p.expr(0)}
	case "if":
		p.advance()
		for {
			condition := p.expr(0)
			p.expect("then")
			body := p.block()
			s.branches = append(s.branches, branch{condition: condition, body: body})
			if !p.accept("elseif") {
				break
			}
		}
		if p.accept("else") {
			s.body = p.block()
		}
		p.close("end", "if", start.line)
	case "for":
		p.advance()
		s.names = p.names()
		if len(s.names) == 1 && p.accept("=") {
			s.kind = "fornum"
			s.values = []*expression{p.expr(0)}
			p.expect(",")
			s.values = append(s.values, p.expr(0))
			if p.accept(",") {
				s.values = append(s.values, p.expr(0))
			} else {
				s.values = append(s.values, &expression{kind: "literal", value: Number(1), token: start})
			}
		} else {
			s.kind = "forin"
			p.expect("in")
			s.values = p.expressionList()
		}
		p.expect("do")
		s.prepLine = p.lastLine()
		s.body = p.block()
		p.close("end", "for", start.line)
	case "return":
		p.advance()
		switch p.current.kind {
		case "end", "else", "elseif", "until", "<eof>", ";":
		default:
			s.values = p.expressionList()
		}
	case "break":
		p.advance()
	default:
		target := p.suffix()
		if target.kind == "call" && p.current.kind != "=" && p.current.kind != "," {
			s.kind = "call"
			s.values = []*expression{target}
			s.endLine = p.lastLine()
			return s
		}
		s.kind = "assign"
		s.targets = []*expression{target}
		for p.accept(",") {
			s.targets = append(s.targets, p.suffix())
		}
		for _, target := range s.targets {
			if target.kind != "name" && target.kind != "index" {
				p.fail("syntax error")
			}
		}
		p.expect("=")
		s.values = p.expressionList()
	}
	s.endLine = p.lastLine()
	return s
}

func (p *parser) function(line int, method bool) *syntaxFunction {
	p.enter()
	defer func() { p.depth-- }()
	f := &syntaxFunction{line: line}
	p.expect("(")
	if method {
		f.params = append(f.params, token{kind: "<name>", text: "self", line: line})
	}
	if p.current.kind != ")" {
		for {
			if p.accept("...") {
				f.vararg = true
				break
			}
			f.params = append(f.params, p.expect("<name>"))
			if !p.accept(",") {
				break
			}
		}
	}
	p.expect(")")
	f.body = p.block()
	p.close("end", "function", line)
	return f
}

func (p *parser) expressionList() []*expression {
	out := []*expression{p.expr(0)}
	for p.accept(",") {
		out = append(out, p.expr(0))
	}
	return out
}

func priority(op string) (int, int) {
	switch op {
	case "or":
		return 1, 1
	case "and":
		return 2, 2
	case "<", ">", "<=", ">=", "~=", "==":
		return 3, 3
	case "..":
		return 5, 4
	case "+", "-":
		return 6, 6
	case "*", "/", "%":
		return 7, 7
	case "^":
		return 10, 9
	default:
		return 0, 0
	}
}

func (p *parser) expr(limit int) *expression {
	p.enter()
	defer func() { p.depth-- }()
	var value *expression
	if p.current.kind == "not" || p.current.kind == "-" || p.current.kind == "#" {
		op := p.advance()
		value = &expression{kind: "unary", token: op, name: op.kind, left: p.expr(8)}
	} else {
		value = p.simple()
	}
	for {
		left, right := priority(p.current.kind)
		if left <= limit {
			value.endLine = p.lastLine()
			return value
		}
		op := p.advance()
		value = &expression{kind: "binary", token: op, name: op.kind, left: value, right: p.expr(right)}
	}
}

func (p *parser) simple() *expression {
	start := p.current
	switch start.kind {
	case "nil", "false", "true", "<number>", "<string>":
		p.advance()
		value := start.value
		if start.kind == "true" {
			value = Bool(true)
		}
		if start.kind == "false" {
			value = Bool(false)
		}
		return &expression{kind: "literal", token: start, value: value}
	case "...":
		p.advance()
		return &expression{kind: "vararg", token: start}
	case "function":
		p.advance()
		return &expression{kind: "function", token: start, function: p.function(start.line, false)}
	case "{":
		return p.constructor()
	default:
		return p.suffix()
	}
}

func (p *parser) suffix() *expression {
	start := p.current
	var value *expression
	if p.accept("(") {
		value = &expression{kind: "paren", token: start, left: p.expr(0)}
		p.close(")", "(", start.line)
	} else {
		if p.current.kind != "<name>" {
			p.fail("unexpected symbol")
		}
		name := p.advance()
		value = &expression{kind: "name", name: name.text, token: name}
	}
	value.endLine = p.lastLine()
	for {
		switch p.current.kind {
		case ".":
			p.advance()
			key := p.expect("<name>")
			value = &expression{kind: "index", token: key, left: value, right: &expression{kind: "literal", token: key, value: String(key.text)}}
		case "[":
			opening := p.advance()
			key := p.expr(0)
			p.close("]", "[", opening.line)
			value = &expression{kind: "index", token: opening, left: value, right: key}
		case ":":
			p.advance()
			key := p.expect("<name>")
			indexed := &expression{kind: "index", token: key, left: value, right: &expression{kind: "literal", token: key, value: String(key.text)}}
			value = &expression{kind: "call", token: key, left: indexed, args: p.arguments(), method: true}
		case "(", "{", "<string>":
			call := p.current
			value = &expression{kind: "call", token: call, left: value, args: p.arguments()}
		default:
			return value
		}
		value.endLine = p.lastLine()
	}
}

func (p *parser) arguments() []*expression {
	start := p.current
	previousLine := p.previous.line
	if p.previous.endLine > 0 {
		previousLine = p.previous.endLine
	}
	if start.kind == "(" && start.line != previousLine {
		p.fail("ambiguous syntax (function call x new statement)")
	}
	if p.accept("(") {
		var args []*expression
		if p.current.kind != ")" {
			args = p.expressionList()
		}
		p.close(")", "(", start.line)
		return args
	}
	if p.current.kind == "{" {
		return []*expression{p.constructor()}
	}
	if p.current.kind == "<string>" {
		item := p.advance()
		return []*expression{{kind: "literal", value: item.value, token: item}}
	}
	p.fail("function arguments expected")
	return nil
}

func (p *parser) constructor() *expression {
	start := p.expect("{")
	value := &expression{kind: "table", token: start}
	for p.current.kind != "}" {
		var field tableField
		if p.accept("[") {
			field.key = p.expr(0)
			p.expect("]")
			p.expect("=")
			field.value = p.expr(0)
		} else if p.current.kind == "<name>" && p.peek().kind == "=" {
			name := p.advance()
			p.expect("=")
			field.key = &expression{kind: "literal", token: name, value: String(name.text)}
			field.value = p.expr(0)
		} else {
			field.value = p.expr(0)
		}
		value.fields = append(value.fields, field)
		if !p.accept(",") && !p.accept(";") {
			break
		}
		if p.current.kind == "}" {
			break
		}
	}
	p.close("}", "{", start.line)
	return value
}
