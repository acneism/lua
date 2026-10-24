package lua

import (
	"fmt"
	"math"
)

type yieldSignal struct{ values []Value }

func (y *yieldSignal) Error() string { return "coroutine yielded" }

func (s *State) push(fn *function, args []Value, dest, want int, name nameInfo, method bool) {
	t := s.current
	if len(t.frames) >= 20000 {
		s.runtimeError("stack overflow")
	}
	p := fn.proto
	start := max(t.used, t.top)
	extra := 0
	if p.vararg {
		extra = max(0, len(args)-p.parameters)
	}
	base := start + extra
	limit := base + p.maxRegisters
	t.ensure(limit)
	clear(t.stack[start:limit])
	copy(t.stack[base:base+p.parameters], args)
	if extra > 0 {
		copy(t.stack[start:base], args[p.parameters:])
	}
	f := frame{function: fn, start: start, base: base, varargBase: start, varargCount: extra, limit: limit, returnDest: dest, want: want, callName: name, method: method}
	t.frames = append(t.frames, f)
	t.used, t.top = limit, base+p.parameters
	if p.argRegister >= 0 && p.needsArg && !p.usesVararg {
		arg := s.NewTable(extra, 1)
		copy(arg.array, t.stack[f.varargBase:f.varargBase+extra])
		arg.set(String("n"), Number(float64(extra)))
		t.stack[base+p.argRegister] = arg.Value()
	}
}

func (s *State) native(fn *function, args []Value, name nameInfo, method bool) []Value {
	t := s.current
	depth := len(t.frames)
	t.frames = append(t.frames, frame{function: fn, callName: name, method: method})
	t.nativeDepth++
	defer func() { t.nativeDepth--; t.frames = t.frames[:depth] }()
	s.tick()
	values, err := fn.native(s, args)
	if err != nil {
		if _, ok := err.(*yieldSignal); ok {
			panic(err)
		}
		if failure, ok := err.(*Error); ok {
			if failure.cDepth == 0 {
				failure.cDepth = t.cDepth
			}
			if failure.Line == 0 {
				failure.Line = s.currentLine()
			}
			panic(failure)
		}
		panic(&Error{Message: err.Error(), Value: String(err.Error()), Abort: err, Line: s.currentLine()})
	}
	return values
}

func (t *thread) writeResults(dest, want int, values []Value) {
	count := want
	if count < 0 {
		count = len(values)
	}
	t.ensure(dest + count)
	copy(t.stack[dest:dest+count], values)
	if count > len(values) {
		clear(t.stack[dest+len(values) : dest+count])
	}
	t.top = dest + count
}

func (s *State) finish(values []Value) (bool, []Value) {
	t := s.current
	f := t.frames[len(t.frames)-1]
	t.close(f.base)
	if f.returnDest < 0 {
		result := append([]Value(nil), values...)
		clear(t.stack[f.start:t.used])
		t.used = f.start
		t.frames = t.frames[:len(t.frames)-1]
		t.top = f.start
		return true, result
	}
	count := f.want
	if count < 0 {
		count = len(values)
	}
	t.ensure(f.returnDest + count)
	copy(t.stack[f.returnDest:f.returnDest+count], values)
	if count > len(values) {
		clear(t.stack[f.returnDest+len(values) : f.returnDest+count])
	}
	clearStart := max(f.start, f.returnDest+count)
	if clearStart < t.used {
		clear(t.stack[clearStart:t.used])
	}
	t.frames = t.frames[:len(t.frames)-1]
	t.used = f.start
	t.top = f.returnDest + count
	return false, nil
}

func (s *State) execute(stop int) (result []Value) {
	t := s.current
	defer func() {
		if raised := recover(); raised != nil {
			if signal, ok := raised.(*yieldSignal); ok {
				t.status = "suspended"
				result = signal.values
			} else {
				panic(raised)
			}
		}
	}()
	for len(t.frames) > stop {
		frameIndex := len(t.frames) - 1
		f := &t.frames[frameIndex]
		p := f.function.proto
		i := &p.code[f.pc]
		t.frames[frameIndex].pc++
		if i.line > 0 {
			t.frames[frameIndex].line = i.line
		}
		s.tick()
		a, b, c := f.base+i.a, f.base+i.b, f.base+i.c
		switch i.op {
		case opConstant:
			t.stack[a] = p.constants[i.b]
			if t.stack[a].kind == TypeString && len(t.stack[a].text) > s.options.MaxString {
				s.runtimeError("string length overflow")
			}
		case opMove:
			t.stack[a] = t.stack[b]
		case opGetGlobal:
			t.stack[a] = s.get(f.function.env.Value(), p.constants[i.b], nameInfo{})
		case opSetGlobal:
			s.set(f.function.env.Value(), p.constants[i.a], t.stack[b], nameInfo{})
		case opGetUpvalue:
			t.stack[a] = f.function.upvalues[i.b].get()
		case opSetUpvalue:
			f.function.upvalues[i.a].set(t.stack[b])
		case opGetTable:
			t.stack[a] = s.get(t.stack[b], t.stack[c], i.left)
		case opSetTable:
			s.set(t.stack[a], t.stack[b], t.stack[c], i.left)
		case opNewTable:
			t.stack[a] = s.NewTable(i.b, i.c).Value()
		case opSetList:
			count := i.c
			if count < 0 {
				count = t.top - b
			}
			table := t.stack[a].Table()
			if i.d+count > len(table.array) {
				table.resize(i.d+count, len(table.nodes))
			}
			copy(table.array[i.d:i.d+count], t.stack[b:b+count])
		case opClosure:
			child := p.children[i.b]
			upvalues := make([]*upvalue, len(child.upvalues))
			for index, descriptor := range child.upvalues {
				if descriptor.local {
					upvalues[index] = t.capture(f.base + descriptor.index)
				} else {
					upvalues[index] = f.function.upvalues[descriptor.index]
				}
			}
			t.stack[a] = s.closure(child, f.function.env, upvalues)
		case opVararg:
			t.writeResults(a, i.b, t.stack[f.varargBase:f.varargBase+f.varargCount])
		case opUnary:
			t.stack[a] = s.unary(i.text, t.stack[b], i.left)
		case opBinary:
			left, right := Nil(), Nil()
			if i.b < 0 {
				left = p.constants[-1-i.b]
			} else {
				left = t.stack[b]
			}
			if i.c < 0 {
				right = p.constants[-1-i.c]
			} else {
				right = t.stack[c]
			}
			if left.kind == TypeNumber && right.kind == TypeNumber {
				x, y := left.number, right.number
				switch i.text {
				case "+":
					t.stack[a] = Number(x + y)
					continue
				case "-":
					t.stack[a] = Number(x - y)
					continue
				case "*":
					t.stack[a] = Number(x * y)
					continue
				case "/":
					t.stack[a] = Number(x / y)
					continue
				case "%":
					t.stack[a] = Number(luaModulo(x, y))
					continue
				case "^":
					t.stack[a] = Number(luaPower(x, y))
					continue
				case "<":
					t.stack[a] = Bool(x < y)
					continue
				case "<=":
					t.stack[a] = Bool(x <= y)
					continue
				case ">":
					t.stack[a] = Bool(x > y)
					continue
				case ">=":
					t.stack[a] = Bool(x >= y)
					continue
				case "==":
					t.stack[a] = Bool(x == y)
					continue
				case "~=":
					t.stack[a] = Bool(x != y)
					continue
				}
			}
			t.stack[a] = s.binary(i.text, left, right, i.left, i.right)
		case opJump:
			if i.a > 0 {
				t.close(a - 1)
			}
			t.frames[frameIndex].pc = i.b
		case opJumpFalse:
			if !t.stack[a].Truth() {
				t.frames[frameIndex].pc = i.b
			}
		case opJumpTrue:
			if t.stack[a].Truth() {
				t.frames[frameIndex].pc = i.b
			}
		case opClose:
			t.close(a)
		case opCall, opTailCall:
			count := i.b
			if count < 0 {
				count = t.top - a - 1
			}
			fn := t.stack[a]
			args := t.stack[a+1 : a+1+count]
			if fn.kind != TypeFunction {
				fn, args = s.callable(fn, args, i.left)
			}
			if fn.object.function.native != nil {
				t.resumeDest, t.resumeWant, t.resumeReturn = f.base+i.d, i.c, i.op == opTailCall
				values := s.native(fn.object.function, args, i.left, i.method)
				if i.op == opTailCall {
					done, values := s.finish(values)
					if done {
						return values
					}
				} else {
					t.writeResults(f.base+i.d, i.c, values)
				}
			} else if i.op == opTailCall {
				t.tail = append(t.tail[:0], args...)
				t.close(f.base)
				clear(t.stack[f.start:t.used])
				t.frames = t.frames[:frameIndex]
				t.used, t.top = f.start, f.start
				s.push(fn.object.function, t.tail, f.returnDest, f.want, i.left, i.method)
			} else {
				s.push(fn.object.function, args, f.base+i.d, i.c, i.left, i.method)
			}
		case opReturn:
			count := i.b
			if count < 0 {
				count = t.top - a
			}
			done, values := s.finish(t.stack[a : a+count])
			if done {
				return values
			}
		case opForPrep:
			labels := [...]string{"initial value", "limit", "step"}
			for offset, label := range labels {
				number, ok := t.stack[a+offset].toNumber()
				if !ok {
					s.runtimeError("'for' " + label + " must be a number")
				}
				t.stack[a+offset] = Number(number)
			}
			t.stack[a].number -= t.stack[a+2].number
			t.frames[frameIndex].pc = i.b
		case opForLoop:
			step := t.stack[a+2].number
			next := t.stack[a].number + step
			limit := t.stack[a+1].number
			if step > 0 && next <= limit || step <= 0 && limit <= next {
				t.stack[a] = Number(next)
				t.stack[a+3] = Number(next)
				t.frames[frameIndex].pc = i.b
			}
		case opForCall:
			values := s.invoke(t.stack[a], []Value{t.stack[a+1], t.stack[a+2]})
			t.writeResults(a+3, i.c, values)
			if !t.stack[a+3].IsNil() {
				t.stack[a+2] = t.stack[a+3]
				t.frames[frameIndex].pc = i.b
			}
		}
	}
	return nil
}

func (s *State) meta(value Value, name string) Value {
	var table *Table
	if value.kind == TypeTable {
		table = value.Table().metatable
	}
	if value.kind == TypeString {
		table = s.stringMeta
	}
	if table != nil {
		return table.RawGet(String(name))
	}
	return Nil()
}

func (s *State) get(value, key Value, info nameInfo) Value {
	for depth := 0; depth < 100; depth++ {
		if value.kind == TypeTable {
			result := value.Table().RawGet(key)
			if !result.IsNil() {
				return result
			}
		}
		meta := s.meta(value, "__index")
		if meta.IsNil() {
			if value.kind == TypeTable {
				return Nil()
			}
			s.typeError("index", value, info)
		}
		if meta.kind == TypeFunction {
			return s.invokeOne(meta, []Value{value, key})
		}
		value, info = meta, nameInfo{}
	}
	s.runtimeError("loop in gettable")
	return Nil()
}

func (s *State) set(value, key, assigned Value, info nameInfo) {
	for depth := 0; depth < 100; depth++ {
		if value.kind == TypeTable {
			table := value.Table()
			if table.readonly {
				s.runtimeError("Attempt to modify a readonly table")
			}
			if !table.RawGet(key).IsNil() || s.meta(value, "__newindex").IsNil() {
				if err := table.RawSet(key, assigned); err != nil {
					s.runtimeError(err.Error())
				}
				return
			}
		}
		meta := s.meta(value, "__newindex")
		if meta.IsNil() {
			s.typeError("index", value, info)
		}
		if meta.kind == TypeFunction {
			s.invokeOne(meta, []Value{value, key, assigned})
			return
		}
		value, info = meta, nameInfo{}
	}
	s.runtimeError("loop in settable")
}

func (s *State) callable(value Value, args []Value, info nameInfo) (Value, []Value) {
	meta := s.meta(value, "__call")
	if meta.kind != TypeFunction {
		s.typeError("call", value, info)
	}
	arguments := make([]Value, len(args)+1)
	arguments[0] = value
	copy(arguments[1:], args)
	return meta, arguments
}

func (s *State) unary(operator string, value Value, info nameInfo) Value {
	switch operator {
	case "not":
		return Bool(!value.Truth())
	case "#":
		if value.kind == TypeString {
			return Number(float64(len(value.text)))
		}
		if value.kind == TypeTable {
			return Number(float64(value.Table().Len()))
		}
		s.typeError("get length of", value, info)
	case "-":
		if number, ok := value.toNumber(); ok {
			return Number(-number)
		}
		if method := s.meta(value, "__unm"); !method.IsNil() {
			return s.invokeOne(method, []Value{value, value})
		}
		s.typeError("perform arithmetic on", value, info)
	}
	return Nil()
}

func first(values []Value) Value {
	if len(values) > 0 {
		return values[0]
	}
	return Nil()
}

func (s *State) binary(operator string, left, right Value, leftName, rightName nameInfo) Value {
	switch operator {
	case "==", "~=":
		equal := left.Equal(right)
		if !equal && left.kind == right.kind && (left.kind == TypeTable || left.kind == TypeUserdata) {
			method := s.meta(left, "__eq")
			if !method.IsNil() && method.Equal(s.meta(right, "__eq")) {
				equal = s.invokeOne(method, []Value{left, right}).Truth()
			}
		}
		if operator == "~=" {
			equal = !equal
		}
		return Bool(equal)
	case "<", "<=", ">", ">=":
		if operator == ">" {
			return Bool(s.less(right, left, false))
		}
		if operator == ">=" {
			return Bool(s.less(right, left, true))
		}
		return Bool(s.less(left, right, operator == "<="))
	case "..":
		a, aok := left.toString()
		b, bok := right.toString()
		if aok && bok {
			s.checkString(len(a) + len(b))
			return String(a + b)
		}
		method := s.meta(left, "__concat")
		if method.IsNil() {
			method = s.meta(right, "__concat")
		}
		if !method.IsNil() {
			return s.invokeOne(method, []Value{left, right})
		}
		if !aok {
			s.typeError("concatenate", left, leftName)
		}
		s.typeError("concatenate", right, rightName)
	default:
		a, aok := left.toNumber()
		b, bok := right.toNumber()
		if aok && bok {
			switch operator {
			case "+":
				return Number(a + b)
			case "-":
				return Number(a - b)
			case "*":
				return Number(a * b)
			case "/":
				return Number(a / b)
			case "%":
				return Number(luaModulo(a, b))
			case "^":
				return Number(luaPower(a, b))
			}
		}
		name := ""
		switch operator {
		case "+":
			name = "__add"
		case "-":
			name = "__sub"
		case "*":
			name = "__mul"
		case "/":
			name = "__div"
		case "%":
			name = "__mod"
		case "^":
			name = "__pow"
		}
		method := s.meta(left, name)
		if method.IsNil() {
			method = s.meta(right, name)
		}
		if !method.IsNil() {
			return s.invokeOne(method, []Value{left, right})
		}
		if !aok {
			s.typeError("perform arithmetic on", left, leftName)
		}
		s.typeError("perform arithmetic on", right, rightName)
	}
	return Nil()
}

func luaModulo(left, right float64) float64 {
	product := float64(math.Floor(left/right) * right)
	return left - product
}

func luaPower(left, right float64) float64 {
	value := armPow(left, right)
	if math.IsNaN(value) && !math.IsNaN(left) && !math.IsNaN(right) {
		return math.Copysign(value, -1)
	}
	return value
}

func (s *State) less(left, right Value, equal bool) bool {
	if left.kind == right.kind {
		if left.kind == TypeNumber {
			if equal {
				return left.number <= right.number
			}
			return left.number < right.number
		}
		if left.kind == TypeString {
			if equal {
				return left.text <= right.text
			}
			return left.text < right.text
		}
	}
	name := "__lt"
	if equal {
		name = "__le"
	}
	method := s.meta(left, name)
	if !method.IsNil() && method.Equal(s.meta(right, name)) {
		return s.invokeOne(method, []Value{left, right}).Truth()
	}
	if equal {
		method = s.meta(left, "__lt")
		if !method.IsNil() && method.Equal(s.meta(right, "__lt")) {
			return !s.invokeOne(method, []Value{right, left}).Truth()
		}
	}
	if left.kind == right.kind {
		s.runtimeError(fmt.Sprintf("attempt to compare two %s values", left.kind))
	}
	s.runtimeError(fmt.Sprintf("attempt to compare %s with %s", left.kind, right.kind))
	return false
}
