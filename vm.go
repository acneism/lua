package lua

import (
	"fmt"
	"math"
)

func clearRegisters(values []Value) {
	switch len(values) {
	case 1:
		values[0] = Nil()
	case 2:
		values[0], values[1] = Nil(), Nil()
	case 3:
		values[0], values[1], values[2] = Nil(), Nil(), Nil()
	default:
		clear(values)
	}
}

func (s *State) push(fn *function, args []Value, dest, want int, site *instruction) {
	t := s.current
	if len(t.frames) >= freshCalls-1 && len(t.frames) >= t.callLimit-1 {
		s.growFrames()
	}
	p := fn.proto
	start := max(t.used, t.top)
	extra := 0
	if p.vararg {
		extra = max(0, len(args)-p.parameters)
	}
	base := start + extra
	limit := base + p.maxRegisters
	t.ensure(max(limit, base+256))
	count := min(p.parameters, len(args))
	for index := range count {
		t.stack[base+index] = args[index]
	}
	clearRegisters(t.stack[base+count : limit])
	if p.argRegister >= 0 {
		t.stack[base+p.argRegister] = Nil()
	}
	for index := range extra {
		t.stack[start+index] = args[p.parameters+index]
	}
	depth := len(t.frames)
	if depth == cap(t.frames) {
		t.frames = append(t.frames, frame{})
	} else {
		t.frames = t.frames[:depth+1]
	}
	entered := &t.frames[depth]
	entered.function, entered.proto, entered.site = fn, fn.proto, site
	entered.start, entered.base, entered.pc = int32(start), int32(base), 0
	entered.returnDest, entered.want = int32(dest), int32(want)
	entered.metacallTop, entered.tailcalls = 0, 0
	t.used, t.top = limit, base+p.parameters
	if p.needsArgTable() {
		arg := s.NewTable(extra, 1)
		for index, value := range t.stack[start:base] {
			arg.setArray(index, value)
		}
		arg.sequence = -1
		arg.set(String("n"), Number(float64(extra)))
		t.stack[base+p.argRegister] = arg.Value()
	}
}

func (s *State) native(fn *function, args []Value, site *instruction) []Value {
	t := s.current
	used, top := t.used, t.top
	start := max(used, top)
	end := start + len(args)
	t.ensure(end)
	copy(t.stack[start:end], args)
	t.used, t.top = end, end
	defer func() { t.used, t.top = used, top }()
	return s.nativeStack(fn, t.stack[start:end], site, start)
}

func (s *State) nativeStack(fn *function, args []Value, site *instruction, start int) []Value {
	t := s.current
	if len(t.frames) >= freshCalls-1 && len(t.frames) >= t.callLimit-1 {
		s.growFrames()
	}
	depth := len(t.frames)
	if depth == cap(t.frames) {
		t.frames = append(t.frames, frame{})
	} else {
		t.frames = t.frames[:depth+1]
	}
	entered := &t.frames[depth]
	entered.function, entered.proto, entered.site = fn, fn.proto, site
	entered.start, entered.base, entered.pc = int32(start), int32(start+len(args)), 0
	entered.returnDest, entered.want, entered.metacallTop, entered.tailcalls = 0, 0, 0, 0
	t.nativeDepth++
	s.tick()
	values, err := fn.native(s, args)
	if err != nil {
		if failure, ok := err.(*Error); ok {
			if failure.cDepth == 0 {
				failure.cDepth = t.cDepth
			}
			if failure.Line == 0 {
				failure.Line = s.currentLine()
			}
			panic(s.processPanic(failure))
		}
		panic(&Error{Message: err.Error(), Value: String(err.Error()), Abort: err, Line: s.currentLine()})
	}
	t.nativeDepth--
	t.frames = t.frames[:depth]
	if len(t.nativeRoots) > 0 {
		t.dropNativeRoots(depth)
	}
	return values
}

func (t *thread) dropNativeRoots(depth int) {
	for len(t.nativeRoots) > 0 && t.nativeRoots[len(t.nativeRoots)-1].frame >= depth {
		t.nativeRoots[len(t.nativeRoots)-1] = nativeRoot{}
		t.nativeRoots = t.nativeRoots[:len(t.nativeRoots)-1]
	}
}

func (p *Proto) needsArgTable() bool { return p.argRegister >= 0 && p.needsArg && !p.usesVararg }

func (t *thread) pushLua(callee *function, start, base, dest, want int) {
	depth := len(t.frames)
	t.frames = t.frames[:depth+1]
	entered := &t.frames[depth]
	entered.function, entered.proto = callee, callee.proto
	entered.start, entered.base, entered.returnDest, entered.want = int32(start), int32(base), int32(dest), int32(want)
	entered.pc, entered.metacallTop, entered.tailcalls = 0, 0, 0
	t.used = base + callee.proto.maxRegisters
}

func (s *State) newClosure() {
	t := s.current
	f := t.topFrame()
	base := int(f.base)
	i := f.proto.instruction(int(f.pc) - 1)
	child := f.proto.children[i.b]
	closure, upvalues := s.closure(child, f.function.env, len(child.upvalues))
	for index, descriptor := range child.upvalues {
		if descriptor.local {
			upvalues[index] = t.capture(base + descriptor.index)
		} else {
			upvalues[index] = f.function.upvalues[descriptor.index]
		}
	}
	t.stack[base+i.a] = closure
}

func (s *State) nativeIterator(fn *function) {
	t := s.current
	frameIndex := len(t.frames) - 1
	i := t.frames[frameIndex].proto.instruction(int(t.frames[frameIndex].pc) - 1)
	a := int(t.frames[frameIndex].base) + i.a
	t.cDepth++
	if t.cDepth == 200 {
		s.runtimeError("C stack overflow")
	}
	if t.cDepth >= 225 {
		panic(plainError("error in error handling"))
	}
	t.boundary++
	t.stack[a+4], t.stack[a+5] = t.stack[a+1], t.stack[a+2]
	values := s.nativeStack(fn, t.stack[a+4:a+6], i, a+4)
	t.cDepth--
	t.boundary--
	t.writeResults(a+3, i.c, values)
	if t.stack[a+3].IsNil() {
		t.frames[frameIndex].pc = int32(i.d)
	} else {
		t.stack[a+2] = t.stack[a+3]
	}
}

func (t *thread) returnValues(frameIndex, first, count int) {
	f := &t.frames[frameIndex]
	base := int(f.base)
	wanted, dest := int(f.want), int(f.returnDest)
	if wanted < 0 {
		wanted = count
	}
	if t.open != nil && t.open.index >= base {
		t.close(base)
	}
	if wanted == 1 {
		if count > 0 {
			t.stack[dest] = t.stack[first]
		} else {
			t.stack[dest] = Nil()
		}
	} else {
		copy(t.stack[dest:dest+wanted], t.stack[first:first+count])
		if wanted > count {
			clear(t.stack[dest+count : dest+wanted])
		}
	}
	t.used, t.top = int(f.start), dest+wanted
	if f.metacallTop > 0 {
		t.top = int(f.metacallTop) - 1
		t.cDepth--
		t.boundary--
	}
	t.frames = t.frames[:frameIndex]
}

func (t *thread) writeResults(dest, want int, values []Value) {
	if want == 1 && dest < len(t.stack) {
		if len(values) > 0 {
			t.stack[dest] = values[0]
		} else {
			t.stack[dest] = Nil()
		}
		t.top = dest + 1
		return
	}
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
	f := &t.frames[len(t.frames)-1]
	if f.want < -1 {
		s.returnCompare(first(values))
		return false, nil
	}
	if t.open != nil && t.open.index >= int(f.base) {
		t.close(int(f.base))
	}
	metaTop := int(f.metacallTop)
	if metaTop > 0 {
		t.cDepth--
		t.boundary--
	}
	start, dest, count := int(f.start), int(f.returnDest), int(f.want)
	if dest < 0 {
		result := append([]Value(nil), values...)
		t.used, t.top = start, start
		t.frames = t.frames[:len(t.frames)-1]
		return true, result
	}
	if count < 0 {
		count = len(values)
	}
	t.ensure(dest + count)
	copy(t.stack[dest:dest+count], values)
	if count > len(values) {
		clear(t.stack[dest+len(values) : dest+count])
	}
	t.frames = t.frames[:len(t.frames)-1]
	t.used, t.top = start, dest+count
	if metaTop > 0 {
		t.top = metaTop - 1
	}
	return false, nil
}

type activation struct {
	t     *thread
	f     *frame
	base  int
	index int
	stop  int
}

func (t *thread) topFrame() *frame { return &t.frames[len(t.frames)-1] }

func (s *State) execute(stop int) (result []Value) {
	if s.options.Hook != nil {
		s.budget -= vmCallWeight - 1
		s.tick()
	}
	active := activation{t: s.current, stop: stop}

frames:
	for {
		thread := active.t
		index := len(thread.frames) - 1
		if index < active.stop {
			return nil
		}
		entered := &thread.frames[index]
		active.f, active.index, active.base = entered, index, int(entered.base)
		pc := int(entered.pc)
		code, constants := entered.proto.code, entered.proto.constants
		registers := (*[256]Value)(thread.stack[active.base : active.base+256])
		for {
			word := code[pc]
			pc++
			switch op := opcode(word); op {
			case comparisonBase + 0, comparisonBase + 1, comparisonBase + 2, comparisonBase + 3, comparisonBase + 4, comparisonBase + 5, comparisonBase + 6, comparisonBase + 7, comparisonBase + 8, comparisonBase + 9, comparisonBase + 10, comparisonBase + 11, comparisonBase + 12, comparisonBase + 13, comparisonBase + 14, comparisonBase + 15, comparisonBase + 16, comparisonBase + 17, comparisonBase + 18, comparisonBase + 19, comparisonBase + 20, comparisonBase + 21, comparisonBase + 22, comparisonBase + 23:
				mode := op - comparisonBase
				left, right := &registers[uint8(word>>8)], &registers[uint8(word>>16)]
				if mode&leftConstant != 0 {
					left = &constants[uint8(word>>8)]
				}
				if mode&rightConstant != 0 {
					right = &constants[uint8(word>>16)]
				}
				var matched, known bool
				if left.object == &numberMarker && right.object == &numberMarker {
					known = true
					switch comparisonKind(mode) {
					case compareEqual:
						matched = left.number == right.number
					case compareLess:
						matched = left.number < right.number
					case compareLessEqual:
						matched = left.number <= right.number
					}
				} else if comparisonKind(mode) == compareEqual {
					matched, known = fastEqual(left, right)
				}
				if !known {
					active.f.pc = int32(pc)
					s.compareFrame(left, right)
					continue frames
				}
				if matched == (mode&expectTrue != 0) {
					target := int(uint32(code[pc] >> 16))
					if target < pc {
						s.budget -= vmCallWeight
						if s.budget <= 0 {
							s.pollJump(pc, target)
							continue frames
						}
					}
					pc = target
				} else {
					pc++
				}
				continue
			case opAdd, opAdd + 1, opAdd + 2, opAdd + 3:
				left, right := numericOperands(op, word, registers, constants)
				if left.object == &numberMarker && right.object == &numberMarker {
					setNumber(&registers[uint8(word>>8)], luaNaN(left.number+right.number, left.number, right.number))
					continue
				}
				active.f.pc = int32(pc)
				s.arithmeticFrame(left, right)
				continue frames
			case opSubtract, opSubtract + 1, opSubtract + 2, opSubtract + 3:
				left, right := numericOperands(op, word, registers, constants)
				if left.object == &numberMarker && right.object == &numberMarker {
					setNumber(&registers[uint8(word>>8)], luaNaN(left.number-right.number, left.number, right.number))
					continue
				}
				active.f.pc = int32(pc)
				s.arithmeticFrame(left, right)
				continue frames
			case opMultiply, opMultiply + 1, opMultiply + 2, opMultiply + 3:
				left, right := numericOperands(op, word, registers, constants)
				if left.object == &numberMarker && right.object == &numberMarker {
					setNumber(&registers[uint8(word>>8)], luaNaN(left.number*right.number, left.number, right.number))
					continue
				}
				active.f.pc = int32(pc)
				s.arithmeticFrame(left, right)
				continue frames
			case opDivide, opDivide + 1, opDivide + 2, opDivide + 3:
				left, right := numericOperands(op, word, registers, constants)
				if left.object == &numberMarker && right.object == &numberMarker {
					setNumber(&registers[uint8(word>>8)], luaDivide(left.number, right.number))
					continue
				}
				active.f.pc = int32(pc)
				s.arithmeticFrame(left, right)
				continue frames
			case opModulo, opModulo + 1, opModulo + 2, opModulo + 3:
				left, right := numericOperands(op, word, registers, constants)
				if left.object == &numberMarker && right.object == &numberMarker {
					x, y := left.number, right.number
					if quotient := x / y; quotient > -0x1p52 && quotient < 0x1p52 {
						whole := float64(int64(quotient))
						if whole > quotient {
							whole--
						} else if whole == quotient {
							whole = quotient
						}
						product := float64(whole * y)
						if remainder := x - product; remainder == remainder {
							setNumber(&registers[uint8(word>>8)], remainder)
							continue
						}
					}
				}
				active.f.pc = int32(pc)
				s.arithmeticFrame(left, right)
				continue frames
			case opPower, opPower + 1, opPower + 2, opPower + 3:
				left, right := numericOperands(op, word, registers, constants)
				active.f.pc = int32(pc)
				if left.object == &numberMarker && right.object == &numberMarker {
					powerInto(&registers[uint8(word>>8)], left.number, right.number)
					pc = int(active.f.pc)
					continue
				}
				s.arithmeticFrame(left, right)
				continue frames
			case opPoll:
				s.budget -= vmPollStride
				if s.budget <= 0 {
					active.f.pc = int32(pc)
					s.pollHook()
					continue frames
				}
				continue
			case opCallFused, opCallFusedUpvalue:
				value := &registers[uint8(word>>16)]
				if op == opCallFusedUpvalue {
					value = active.f.function.upvalues[uint8(word>>16)].slot
				}
				count := int(uint8(word >> 24))
				call := code[pc+count]
				active.f.pc = int32(pc + count + 1)
				arguments := code[pc-1] >> 32
				t := active.t
				start := t.used
				if value.luaFunction() && s.budget > vmCallWeight && len(t.frames) < freshCalls-1 && len(t.frames) < cap(t.frames) && !value.object.function.proto.needsArgTable() {
					callee := value.object.function
					parameters, frameBase := callee.proto.parameters, start
					if callee.proto.vararg {
						frameBase += max(0, count-parameters)
					}
					if frameBase+256 <= len(t.stack) {
						window := (*[256]Value)(t.stack[frameBase : frameBase+256])
						provided := min(parameters, count)
						for argument := 0; argument < provided; argument++ {
							index := uint8(arguments)
							arguments >>= 8
							if index < fusedConstant {
								window[uint8(argument)] = registers[index]
							} else if index != fusedArithmetic {
								window[uint8(argument)] = constants[index-fusedConstant]
							} else {
								load := code[int(active.f.pc)-count-1+argument]
								left, right := numericOperands(opcode(load), load, registers, constants)
								if left.object != &numberMarker || right.object != &numberMarker {
									goto fallback
								}
								x, y := left.number, right.number
								switch opcode(load).numericOperation() {
								case operationAdd:
									window[uint8(argument)] = Number(luaNaN(x+y, x, y))
								case operationSubtract:
									window[uint8(argument)] = Number(luaNaN(x-y, x, y))
								default:
									window[uint8(argument)] = Number(luaNaN(x*y, x, y))
								}
							}
						}
						for argument := provided; argument < count; argument++ {
							index := uint8(arguments)
							arguments >>= 8
							if index == fusedArithmetic {
								goto fallback
							}
							if frameBase != start {
								if index < fusedConstant {
									t.stack[start+argument-provided] = registers[index]
								} else {
									t.stack[start+argument-provided] = constants[index-fusedConstant]
								}
							}
						}
						s.budget -= vmCallWeight
						t.pushLua(callee, start, frameBase, active.base+int(uint8(call>>48)), int(int16(call>>32)))
						clearRegisters(t.stack[frameBase+provided : frameBase+callee.proto.maxRegisters])
						continue frames
					}
				}
			fallback:
				pc = int(active.f.pc) - count - 1
				registers[uint8(code[pc-1]>>8)] = *value
				continue
			case opCall:
				s.budget -= vmCallWeight
				if s.budget <= 0 {
					s.pollRetry(pc)
					continue frames
				}
				a := uint8(word >> 8)
				argStart := active.base + int(a) + 1
				count := int(int16(word >> 16))
				t := active.t
				start := t.used
				if count < 0 {
					count = t.top - argStart
					start = max(start, t.top)
				}
				object := registers[a].object
				if object != nil && object.kind == TypeTable {
					method, ok := s.cached(eventCall, object.table.metatable)
					if !ok || method.object == nil {
						break
					}
					object, argStart, count = method.object, argStart-1, count+1
				}
				if object == nil || object.kind != TypeFunction {
					break
				}
				callee := object.function
				active.f.pc = int32(pc)
				if callee.native != nil {
					values, status := s.callNative(callee, argStart, count)
					if status == nativeDone {
						pc = int(active.f.pc)
						continue
					}
					if status == nativeYielded {
						return values
					}
					continue frames
				}
				frameBase := start
				if callee.proto.vararg {
					frameBase += max(0, count-callee.proto.parameters)
				}
				dest, want := active.base+int(uint8(word>>48)), int(int16(word>>32))
				if callee.proto.needsArgTable() || len(t.frames) >= freshCalls-1 || len(t.frames) == cap(t.frames) || frameBase+256 > len(t.stack) {
					s.push(callee, t.stack[argStart:argStart+count], dest, want, nil)
					continue frames
				}
				parameters := callee.proto.parameters
				provided := min(parameters, count)
				window := (*[256]Value)(t.stack[frameBase : frameBase+256])
				for argument, value := range t.stack[argStart : argStart+provided] {
					window[uint8(argument)] = value
				}
				for argument := 0; argument < frameBase-start; argument++ {
					t.stack[start+argument] = t.stack[argStart+parameters+argument]
				}
				t.pushLua(callee, start, frameBase, dest, want)
				clearRegisters(t.stack[frameBase+provided : frameBase+callee.proto.maxRegisters])
				continue frames
			case opReturn:
				t, f := active.t, active.f
				if want := f.want; want >= 0 && int32(int16(word>>16)) >= want && (t.open == nil || t.open.index < active.base) {
					dest := int(f.returnDest)
					if want == 1 {
						t.stack[dest] = registers[uint8(word>>8)]
					} else {
						for index := 0; index < int(want); index++ {
							t.stack[dest+index] = registers[int(uint8(word>>8))+index]
						}
					}
					t.used = int(f.start)
					if f.metacallTop > 0 {
						t.top = int(f.metacallTop) - 1
						t.cDepth--
						t.boundary--
					}
					t.frames = t.frames[:active.index]
					continue frames
				}
				if f.returnDest >= 0 {
					from := int(uint8(word >> 8))
					count := int(int16(word >> 16))
					if count < 0 {
						count = t.top - active.base - from
					}
					if f.want < -1 {
						value := Nil()
						if count > 0 {
							value = registers[from]
						}
						s.returnCompare(value)
						continue frames
					}
					t.returnValues(active.index, active.base+from, count)
					continue frames
				}
			case opConstant:
				registers[uint8(word>>8)] = constants[uint32(word>>16)]
				continue
			case opMove:
				registers[uint8(word>>8)] = registers[uint8(word>>16)]
				continue
			case opLoadNil:
				for index, last := int(uint8(word>>8)), int(uint32(word>>16)); index <= last; index++ {
					registers[uint8(index)] = Nil()
				}
				continue
			case opGetUpvalue:
				registers[uint8(word>>8)] = active.f.function.upvalues[uint32(word>>16)].get()
				continue
			case opSetUpvalue:
				active.f.function.upvalues[uint8(word>>8)].set(registers[uint8(word>>16)])
				continue
			case opGetGlobal:
				value, known := active.f.function.env.lookup(constants[uint32(word>>16)].text)
				if known && !value.IsNil() {
					registers[uint8(word>>8)] = value
					continue
				}
				active.f.pc = int32(pc)
				if s.globalFrame() {
					continue frames
				}
				pc = int(active.f.pc)
				continue
			case opGetTable, opGetTableK:
				value, key := &registers[uint8(word>>16)], &registers[uint8(word>>48)]
				if op == opGetTableK {
					key = &constants[uint16(word>>48)]
				}
				if object := value.object; object != nil && object.kind == TypeTable {
					table := object.table
					if key.text != nil {
						found, known := table.lookup(key.text)
						if known && (!found.IsNil() || table.metatable == nil) {
							registers[uint8(word>>8)] = found
							continue
						}
					} else if key.object == &numberMarker {
						index := int(key.number)
						if uint(index-1) < uint(table.arraySize()) && float64(index) == key.number {
							if found := table.arrayAt(index - 1); !found.IsNil() || table.metatable == nil {
								registers[uint8(word>>8)] = found
								continue
							}
						}
					}
				}
				active.f.pc = int32(pc)
				if s.indexFrame(value, key) {
					continue frames
				}
				pc = int(active.f.pc)
				continue
			case opSelf:
				a := uint8(word >> 8)
				value, key := &registers[uint8(word>>16)], rkOperand(int(int16(word>>48)), registers, constants)
				found, hit := Nil(), false
				if key.text != nil {
					if object := value.object; object != nil {
						if object.kind == TypeTable {
							table := object.table
							for range 100 {
								var known bool
								found, known = table.lookup(key.text)
								if !known {
									break
								}
								if !found.IsNil() || table.metatable == nil {
									hit = true
									break
								}
								method, ok := s.cached(eventIndex, table.metatable)
								if !ok || method.object == nil || method.object.kind != TypeTable {
									break
								}
								table = method.object.table
							}
						}
					} else if method, ok := s.cached(eventIndex, s.stringMeta); ok && value.text != nil && method.object != nil && method.object.kind == TypeTable {
						var known bool
						found, known = method.object.table.lookup(key.text)
						hit = known && !found.IsNil()
					}
				}
				active.f.pc = int32(pc)
				if !hit {
					s.selfFrame(value, key)
					continue frames
				}
				self := *value
				registers[a+1] = self
				if word>>24&255 != 0 && found.luaFunction() {
					s.callSelf(self, found)
					continue frames
				}
				registers[a] = found
				continue
			case opSetTable, opSetTableRK:
				value := &registers[uint8(word>>8)]
				key, assigned := &registers[uint8(word>>16)], &registers[uint8(word>>48)]
				if op == opSetTableRK {
					key, assigned = rkOperand(int(int32(word>>16)), registers, constants), rkOperand(int(int16(word>>48)), registers, constants)
				}
				if object := value.object; object != nil && object.kind == TypeTable && !object.table.readonly {
					table := object.table
					if key.text != nil {
						if table.storeLive(key.text, assigned) {
							continue
						}
					} else if key.object == &numberMarker && table.metatable == nil {
						if index := int(key.number); float64(index) == key.number && table.storeNumber(index-1, assigned) {
							continue
						}
					}
				}
				active.f.pc = int32(pc)
				if s.setFrame(value, key, assigned) {
					continue frames
				}
				pc = int(active.f.pc)
				continue
			case opNegate:
				value := &registers[uint8(word>>16)]
				if value.object == &numberMarker {
					registers[uint8(word>>8)] = Number(-value.number)
					continue
				}
			case opNot:
				registers[uint8(word>>8)] = Bool(!registers[uint8(word>>16)].Truth())
				continue
			case opLength:
				value := &registers[uint8(word>>16)]
				if value.object == nil && value.text != nil {
					setNumber(&registers[uint8(word>>8)], float64(len(value.text.text)))
					continue
				}
				if object := value.object; object != nil && object.kind == TypeTable {
					if object.table.sequence >= 0 {
						setNumber(&registers[uint8(word>>8)], float64(object.table.sequence))
						continue
					}
					active.f.pc = int32(pc)
					lengthInto(&registers[uint8(word>>8)], object.table)
					pc = int(active.f.pc)
					continue
				}
			case opJump:
				target := int(uint32(word >> 16))
				if target < pc {
					s.budget -= vmCallWeight
					if s.budget <= 0 {
						s.pollJump(pc, target)
						continue frames
					}
				}
				pc = target
				continue
			case opJumpFalse:
				if !registers[uint8(word>>8)].Truth() {
					target := int(uint32(word >> 16))
					if target < pc {
						s.budget -= vmCallWeight
						if s.budget <= 0 {
							s.pollJump(pc, target)
							continue frames
						}
					}
					pc = target
				}
				continue
			case opJumpTrue:
				if registers[uint8(word>>8)].Truth() {
					target := int(uint32(word >> 16))
					if target < pc {
						s.budget -= vmCallWeight
						if s.budget <= 0 {
							s.pollJump(pc, target)
							continue frames
						}
					}
					pc = target
				}
				continue
			case opTestSet:
				target, source := &registers[uint8(word>>8)], &registers[uint8(word>>16)]
				if source.Truth() == (uint16(word>>48) != 0) {
					*target = *source
				} else {
					pc++
				}
				continue
			case opForIterator:
				a := uint8(word >> 8)
				object := registers[a].object
				if object == nil || object.kind != TypeFunction || object.function.native == nil {
					break
				}
				if table, control := &registers[a+1], &registers[a+2]; object.function == s.ipairsStep && uint16(word>>48) == 2 && s.budget > 1 && active.t.cDepth < 199 && table.object != nil && table.object.kind == TypeTable && control.object == &numberMarker {
					index := int(control.number)
					if items := table.object.table; uint(index) < uint(items.arraySize()) {
						s.budget--
						value := items.arrayAt(index)
						if value.IsNil() {
							registers[a+3], registers[a+4] = Nil(), Nil()
							pc = int(uint32(word >> 16))
							continue
						}
						number := float64(index + 1)
						control.number = number
						setNumber(&registers[a+3], number)
						registers[a+4] = value
						continue
					}
				}
				active.f.pc = int32(pc)
				s.nativeIterator(object.function)
				continue frames
			case opNewTable:
				active.f.pc = int32(pc)
				s.newTableInto()
				pc = int(active.f.pc)
				continue
			case opClosure:
				active.f.pc = int32(pc)
				s.newClosure()
				pc = int(active.f.pc)
				continue
			case opVararg:
				from, wanted := int(uint8(word>>8)), int(int32(uint32(word>>16)))
				if wanted < 0 {
					wanted = active.base - int(active.f.start)
					active.t.top = active.base + from + wanted
					if from+wanted > len(registers) {
						active.f.pc = int32(pc)
						s.varargsAbove(from)
						continue frames
					}
				}
				varargs := active.t.stack[int(active.f.start):active.base]
				for index := 0; index < wanted; index++ {
					if index < len(varargs) {
						registers[from+index] = varargs[index]
					} else {
						registers[from+index] = Nil()
					}
				}
				continue
			case opClose:
				if active.t.open != nil {
					active.f.pc = int32(pc)
					active.t.close(active.base + int(uint8(word>>8)))
					pc = int(active.f.pc)
				}
				continue
			case opForPrep:
				a := uint8(word >> 8)
				if registers[a].object == &numberMarker && registers[a+1].object == &numberMarker && registers[a+2].object == &numberMarker {
					registers[a].number -= registers[a+2].number
					pc = int(uint32(word >> 16))
					continue
				}
			case opForLoop:
				s.budget -= vmCallWeight
				if s.budget <= 0 {
					s.pollRetry(pc)
					continue frames
				}
				a := uint8(word >> 8)
				step := registers[a+2].number
				next := registers[a].number + step
				limit := registers[a+1].number
				if step > 0 && next <= limit || step <= 0 && limit <= next {
					registers[a].number = next
					setNumber(&registers[a+3], next)
					pc = int(uint32(word >> 16))
				}
				continue
			}
			active.f.pc = int32(pc)
			if values, done := s.executeSlow(); done {
				return values
			}
			continue frames
		}
	}
}

func fastEqual(left, right *Value) (bool, bool) {
	if left.text != right.text {
		return false, left.text == nil || right.text == nil || left.text.hash != right.text.hash
	}
	if left.object == right.object {
		return left.object != &booleanMarker || left.number == right.number, true
	}
	return false, left.object == nil || right.object == nil || left.object.table == nil
}

func (s *State) compareFrame(left, right *Value) {
	f := s.current.topFrame()
	mode := opcode(f.proto.code[f.pc-1]) - comparisonBase
	if method, ok := s.compareMethod(left, right, mode); ok {
		s.enterPair(method, *left, *right, 0, compareWant-int(mode&expectTrue)/expectTrue)
		return
	}
	if !s.enterCompareMethod(*left, *right, int(mode)) {
		s.compareSlow(*left, *right, mode)
	}
}

func (s *State) arithmeticFrame(left, right *Value) {
	f := s.current.topFrame()
	word := f.proto.code[f.pc-1]
	if !s.enterArithmeticMethod(left, right, opcode(word).numericOperation(), int(f.base)+int(uint8(word>>8))) {
		s.executeSlow()
	}
}

func (s *State) varargsAbove(from int) {
	t := s.current
	f := t.topFrame()
	t.writeResults(int(f.base)+from, -1, t.stack[int(f.start):int(f.base)])
}

func powerInto(slot *Value, x, y float64) {
	setNumber(slot, luaPower(x, y))
}

func lengthInto(slot *Value, table *Table) {
	setNumber(slot, float64(table.Len()))
}

func (s *State) newTableInto() {
	t := s.current
	f := t.topFrame()
	i := f.proto.instruction(int(f.pc) - 1)
	table := s.NewTable(i.b, i.c)
	t.stack[int(f.base)+i.a] = table.Value()
}

const (
	nativeDone = iota
	nativeMoved
	nativeYielded
)

func (s *State) callNative(fn *function, argStart, count int) ([]Value, int) {
	t := s.current
	f := t.topFrame()
	word := f.proto.code[f.pc-1]
	dest, want := int(f.base)+int(uint8(word>>48)), int(int16(word>>32))
	stack, frames := &t.stack[0], &t.frames[0]
	values := s.nativeStack(fn, t.stack[argStart:argStart+count], &callSite, argStart)
	if t.yielded {
		t.resumeDest, t.resumeWant, t.resumeReturn = dest, want, false
		t.yielded = false
		t.status = "suspended"
		return values, nativeYielded
	}
	t.writeResults(dest, want, values)
	if stack != &t.stack[0] || frames != &t.frames[0] {
		return nil, nativeMoved
	}
	return nil, nativeDone
}

func (s *State) globalFrame() bool {
	t := s.current
	f := t.topFrame()
	word := f.proto.code[f.pc-1]
	value := f.function.env.getString(f.proto.constants[uint32(word>>16)].text)
	if value.IsNil() {
		s.executeSlow()
		return true
	}
	t.stack[int(f.base)+int(uint8(word>>8))] = value
	return false
}

func (s *State) metaIndexResult(found, receiver, key Value, dest int) {
	t := s.current
	if found.object.function.proto != nil {
		s.enterPair(found, receiver, key, dest, 1)
		return
	}
	f := t.topFrame()
	result := first(s.callMethod(found, 2, receiver, key, Nil(), f.proto.instruction(int(f.pc)-1)))
	t.stack[dest] = result
}

func (s *State) indexFrame(value, key *Value) bool {
	t := s.current
	f := t.topFrame()
	dest := int(f.base) + int(uint8(f.proto.code[f.pc-1]>>8))
	name := *key
	if object := value.object; object != nil && object.kind == TypeTable && object.table.metatable != nil {
		table := object.table
		if method, ok := s.cached(eventIndex, table.metatable); ok && method.luaFunction() && (name.text != nil && table.getString(name.text).IsNil() || name.text == nil && table.rawMissing(name)) {
			s.enterPair(method, *value, name, dest, 1)
			return true
		}
	}
	found, receiver, status := s.indexChain(*value, name)
	switch status {
	case indexFound:
		t.stack[dest] = found
		return false
	case indexMethod:
		s.metaIndexResult(found, receiver, name, dest)
	default:
		s.executeSlow()
	}
	return true
}

func (s *State) selfFrame(value, key *Value) {
	t := s.current
	f := t.topFrame()
	word := f.proto.code[f.pc-1]
	dest := int(f.base) + int(uint8(word>>8))
	self, name := *value, *key
	found, receiver, status := s.indexChain(self, name)
	switch status {
	case indexFound:
		t.stack[dest+1] = self
		if word>>24&255 != 0 && found.luaFunction() {
			s.callSelf(self, found)
			return
		}
		t.stack[dest] = found
	case indexMethod:
		t.stack[dest+1] = self
		s.metaIndexResult(found, receiver, name, dest)
	default:
		s.executeSlow()
	}
}

func (s *State) setFrame(target, name, written *Value) bool {
	t := s.current
	value, key, assigned := *target, *name, *written
	if value.Type() != TypeTable || value.object.table.readonly {
		s.executeSlow()
		return true
	}
	f := t.topFrame()
	base := int(f.base)
	table := value.object.table
	if table.metatable != nil {
		if key.text != nil && table.replaceLive(key.text, assigned) {
			return false
		}
		if method, ok := s.cached(eventNewIndex, table.metatable); ok && method.luaFunction() && (key.text != nil || table.rawMissing(key)) {
			s.enterMethod(method, 3, value, key, assigned, base, 0)
			return true
		}
		if table.RawGet(key).IsNil() {
			if key.IsNil() || key.object == &numberMarker && key.number != key.number {
				s.executeSlow()
				return true
			}
			method := s.eventMethod(table.metatable, eventNewIndex)
			if method.Type() == TypeFunction {
				if method.object.function.proto != nil {
					s.enterMethod(method, 3, value, key, assigned, base, 0)
					return true
				}
				s.callMethod(method, 3, value, key, assigned, f.proto.instruction(int(f.pc)-1))
				return true
			}
			if !method.IsNil() {
				s.executeSlow()
				return true
			}
		}
	}
	if key.object == &numberMarker {
		index := int(key.number)
		if index > 0 && index <= table.arraySize() && float64(index) == key.number {
			table.setArray(index-1, assigned)
			return false
		}
	} else if key.text != nil && table.replaceString(key.text, assigned) {
		return false
	}
	if err := table.RawSet(key, assigned); err != nil {
		s.runtimeError(err.Error())
	}
	return false
}

func (s *State) enterArithmeticMethod(left, right *Value, operation, dest int) bool {
	method := s.binaryTableMethod(*left, operation)
	if method.IsNil() {
		method = s.binaryTableMethod(*right, operation)
	}
	if method.Type() != TypeFunction || method.object.function.proto == nil {
		return false
	}
	s.enterPair(method, *left, *right, dest, 1)
	return true
}

func (s *State) enterPair(method, left, right Value, dest, want int) {
	t := s.current
	callee := method.object.function
	proto := callee.proto
	start := max(t.used, t.top)
	depth := len(t.frames)
	if s.budget <= vmCallWeight || t.cDepth >= 199 || proto.vararg || depth >= freshCalls-1 || depth == cap(t.frames) || start+256 > len(t.stack) {
		s.enterMethod(method, 2, left, right, Nil(), dest, want)
		return
	}
	s.budget -= vmCallWeight
	t.cDepth++
	parameters := proto.parameters
	provided := min(parameters, 2)
	if provided > 0 {
		t.stack[start] = left
	}
	if provided > 1 {
		t.stack[start+1] = right
	}
	clearRegisters(t.stack[start+provided : start+proto.maxRegisters])
	t.frames = t.frames[:depth+1]
	entered := &t.frames[depth]
	entered.function, entered.proto = callee, callee.proto
	entered.start, entered.base, entered.pc = int32(start), int32(start), 0
	entered.returnDest, entered.want = int32(dest), int32(want)
	entered.metacallTop, entered.tailcalls = int32(t.top+1), 0
	t.used, t.top = start+proto.maxRegisters, start+parameters
	t.boundary++
}

func (s *State) meta(value Value, name string) Value {
	if event, ok := eventFor(name); ok {
		return s.metaFor(value, event)
	}
	table := s.metatableOf(value)
	if table == nil {
		return Nil()
	}
	return table.RawGet(String(name))
}

func (s *State) get(value, key Value, info nameInfo) Value {
	for depth := 0; depth < 100; depth++ {
		if value.Type() == TypeTable {
			result := value.Table().RawGet(key)
			if !result.IsNil() {
				return result
			}
		}
		meta := s.metaFor(value, eventIndex)
		if meta.IsNil() {
			if value.Type() == TypeTable {
				return Nil()
			}
			s.typeError("index", value, info)
		}
		if meta.Type() == TypeFunction {
			return s.invokeOne(meta, []Value{value, key})
		}
		value, info = meta, nameInfo{}
	}
	s.runtimeError("loop in gettable")
	return Nil()
}

func (s *State) set(value, key, assigned Value, info nameInfo) {
	for depth := 0; depth < 100; depth++ {
		if value.Type() == TypeTable {
			table := value.Table()
			if table.readonly {
				s.runtimeError("Attempt to modify a readonly table")
			}
			if !table.RawGet(key).IsNil() || s.metaFor(value, eventNewIndex).IsNil() {
				if err := table.RawSet(key, assigned); err != nil {
					s.runtimeError(err.Error())
				}
				return
			}
			if key.IsNil() {
				s.runtimeError("table index is nil")
			}
			if key.Type() == TypeNumber && math.IsNaN(key.number) {
				s.runtimeError("table index is NaN")
			}
		}
		meta := s.metaFor(value, eventNewIndex)
		if meta.IsNil() {
			s.typeError("index", value, info)
		}
		if meta.Type() == TypeFunction {
			s.invokeOne(meta, []Value{value, key, assigned})
			return
		}
		value, info = meta, nameInfo{}
	}
	s.runtimeError("loop in settable")
}

func (s *State) callable(value Value, args []Value, info nameInfo) (Value, []Value) {
	meta := s.metaFor(value, eventCall)
	if meta.Type() != TypeFunction {
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
		if value.Type() == TypeString {
			return Number(float64(len(value.Text())))
		}
		if value.Type() == TypeTable {
			return Number(float64(value.Table().Len()))
		}
		s.typeError("get length of", value, info)
	case "-":
		if number, ok := value.toNumber(); ok {
			return Number(-number)
		}
		if method := s.metaFor(value, eventNegate); !method.IsNil() {
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
		if !equal && left.Type() == right.Type() && (left.Type() == TypeTable || left.Type() == TypeUserdata) {
			method := s.metaFor(left, eventEqual)
			if !method.IsNil() && method.Equal(s.metaFor(right, eventEqual)) {
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
		method := s.metaFor(left, eventConcat)
		if method.IsNil() {
			method = s.metaFor(right, eventConcat)
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
				return Number(luaNaN(a+b, a, b))
			case "-":
				return Number(luaNaN(a-b, a, b))
			case "*":
				return Number(luaNaN(a*b, a, b))
			case "/":
				return Number(luaDivide(a, b))
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

func binaryMethodName(operator string) string {
	switch operator {
	case "+":
		return "__add"
	case "-":
		return "__sub"
	case "*":
		return "__mul"
	case "/":
		return "__div"
	case "%":
		return "__mod"
	case "^":
		return "__pow"
	case "..":
		return "__concat"
	}
	return ""
}

func (s *State) binaryMethod(operator string, left, right Value) Value {
	name := binaryMethodName(operator)
	if name == "" {
		return Nil()
	}
	if operator == ".." {
		_, leftOK := left.toString()
		_, rightOK := right.toString()
		if leftOK && rightOK {
			return Nil()
		}
	} else {
		_, leftOK := left.toNumber()
		_, rightOK := right.toNumber()
		if leftOK && rightOK {
			return Nil()
		}
	}
	method := s.meta(left, name)
	if method.IsNil() {
		method = s.meta(right, name)
	}
	return method
}

func luaModulo(left, right float64) float64 {
	product := float64(luaFloor(left/right) * right)
	return luaNaN(left-product, left, right)
}

func luaNaN(value, left, right float64) float64 {
	if !math.IsNaN(value) {
		return value
	}
	if math.IsNaN(left) {
		return math.Float64frombits(math.Float64bits(left) | 1<<51)
	}
	if math.IsNaN(right) {
		return math.Float64frombits(math.Float64bits(right) | 1<<51)
	}
	return math.Float64frombits(0xfff8000000000000)
}

func luaDivide(left, right float64) float64 {
	return luaNaN(left/right, left, right)
}

func luaPower(left, right float64) float64 {
	value := armPow(left, right)
	if math.IsNaN(value) && !math.IsNaN(left) && !math.IsNaN(right) {
		return math.Copysign(value, -1)
	}
	return value
}

func (s *State) less(left, right Value, equal bool) bool {
	if left.Type() == right.Type() {
		if left.Type() == TypeNumber {
			if equal {
				return left.number <= right.number
			}
			return left.number < right.number
		}
		if left.Type() == TypeString {
			if equal {
				return left.Text() <= right.Text()
			}
			return left.Text() < right.Text()
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
		method = s.metaFor(left, eventLess)
		if !method.IsNil() && method.Equal(s.metaFor(right, eventLess)) {
			return !s.invokeOne(method, []Value{right, left}).Truth()
		}
	}
	if left.Type() == right.Type() {
		s.runtimeError(fmt.Sprintf("attempt to compare two %s values", left.Type()))
	}
	s.runtimeError(fmt.Sprintf("attempt to compare %s with %s", left.Type(), right.Type()))
	return false
}

func (s *State) executeSlow() ([]Value, bool) {
	t := s.current
	frameIndex := len(t.frames) - 1
	f := &t.frames[frameIndex]
	base := int(f.base)
	constants := f.proto.constants
	i := f.proto.instruction(int(f.pc) - 1)
	if i.op == opTailCall {
		s.budget -= vmCallWeight
		if s.budget <= 0 {
			s.pollHook()
			f = &t.frames[frameIndex]
		}
	}
	a, b, c := base+i.a, base+i.b, base+i.c
	switch i.op {
	case opGetGlobal:
		t.stack[a] = s.get(f.function.env.Value(), constants[i.b], nameInfo{})
	case opSetGlobal:
		s.set(f.function.env.Value(), constants[i.a], t.stack[b], nameInfo{})
	case opGetTable:
		t.stack[a] = s.get(t.stack[b], t.stack[c], i.left)
	case opGetTableK:
		t.stack[a] = s.get(t.stack[b], constants[i.c], i.left)
	case opSetTable:
		s.set(t.stack[a], t.stack[b], t.stack[c], i.left)
	case opSetTableRK:
		var key, value Value
		if i.b < 0 {
			key = constants[-1-i.b]
		} else {
			key = t.stack[b]
		}
		if i.c < 0 {
			value = constants[-1-i.c]
		} else {
			value = t.stack[c]
		}
		s.set(t.stack[a], key, value, i.left)
	case opSelf:
		value := t.stack[b]
		var key Value
		if i.c < 0 {
			key = constants[-1-i.c]
		} else {
			key = t.stack[c]
		}
		t.stack[a+1] = value
		t.stack[a] = s.get(value, key, i.left)
	case opConcat:
		for reg := i.c; reg > i.b; reg-- {
			leftName, rightName := nameInfo{kind: "register", register: int16(reg - 1)}, nameInfo{kind: "register", register: int16(reg)}
			value := s.binary("..", t.stack[base+reg-1], t.stack[base+reg], leftName, rightName)
			t.stack[base+reg-1] = value
		}
		t.stack[a] = t.stack[b]
	case opSetList:
		count := i.c
		if count < 0 {
			count = t.top - b
		}
		table := t.stack[a].Table()
		if i.d+count > table.arraySize() {
			table.resize(i.d+count, len(table.nodes))
		}
		for index, value := range t.stack[b : b+count] {
			table.setArray(i.d+index, value)
		}
		if count > 0 {
			table.sequence = -1
		}
	case opUnary:
		t.stack[a] = s.unary(i.text, t.stack[b], i.left)
	case opBinary:
		var left, right Value
		if i.b < 0 {
			left = constants[-1-i.b]
		} else {
			left = t.stack[b]
		}
		if i.c < 0 {
			right = constants[-1-i.c]
		} else {
			right = t.stack[c]
		}
		method := s.binaryMethod(i.text, left, right)
		if method.Type() == TypeFunction && method.object.function.proto != nil {
			s.budget -= vmCallWeight
			if s.budget <= 0 {
				s.pollBinaryHook(method, left, right)
			}
			arguments := [2]Value{left, right}
			previousTop := t.top
			t.cDepth++
			if t.cDepth == 200 {
				s.runtimeError("C stack overflow")
			}
			if t.cDepth >= 225 {
				panic(plainError("error in error handling"))
			}
			s.push(method.object.function, arguments[:], a, 1, nil)
			t.boundary++
			t.frames[len(t.frames)-1].metacallTop = int32(previousTop + 1)
			break
		}
		t.stack[a] = s.binary(i.text, left, right, i.left, i.right)
	case opCall, opTailCall:
		count := i.b
		if count < 0 {
			count = t.top - a - 1
		}
		fn := t.stack[a]
		args := t.stack[a+1 : a+1+count]
		argStart := a + 1
		if fn.Type() != TypeFunction {
			fn, args = s.callable(fn, args, i.left)
			argStart = -1
		}
		if fn.object.function.native != nil {
			t.resumeDest, t.resumeWant, t.resumeReturn = base+i.d, i.c, i.op == opTailCall
			var values []Value
			if argStart >= 0 {
				values = s.nativeStack(fn.object.function, args, i, argStart)
			} else {
				values = s.native(fn.object.function, args, i)
			}
			if t.yielded {
				t.yielded = false
				t.status = "suspended"
				return values, true
			}
			if i.op == opTailCall {
				done, values := s.finish(values)
				if done {
					return values, true
				}
			} else {
				t.writeResults(base+i.d, i.c, values)
			}
		} else if i.op == opTailCall {
			metaTop := f.metacallTop
			tailcalls := f.tailcalls + 1
			t.tail = append(t.tail[:0], args...)
			t.close(int(f.base))
			t.frames = t.frames[:frameIndex]
			t.used, t.top = int(f.start), int(f.start)
			s.push(fn.object.function, t.tail, int(f.returnDest), int(f.want), i)
			t.frames[len(t.frames)-1].metacallTop = metaTop
			t.frames[len(t.frames)-1].tailcalls = tailcalls
		} else {
			s.push(fn.object.function, args, base+i.d, i.c, i)
		}
	case opReturn:
		count := i.b
		if count < 0 {
			count = t.top - a
		}
		done, values := s.finish(t.stack[a : a+count])
		if done {
			return values, true
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
		t.frames[frameIndex].pc = int32(i.b)
	case opForIterator:
		arguments := [2]Value{t.stack[a+1], t.stack[a+2]}
		values := s.invoke(t.stack[a], arguments[:])
		t.writeResults(a+3, i.c, values)
		if !t.stack[a+3].IsNil() {
			t.stack[a+2] = t.stack[a+3]
		} else {
			t.frames[frameIndex].pc = int32(i.d)
		}
	}
	return nil, false
}
