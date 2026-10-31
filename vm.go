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
	count := copy(t.stack[base:base+p.parameters], args)
	clearRegisters(t.stack[base+count : limit])
	if p.argRegister >= 0 {
		t.stack[base+p.argRegister] = Nil()
	}
	if extra > 0 {
		copy(t.stack[start:base], args[p.parameters:])
	}
	f := frame{function: fn, start: int32(start), base: int32(base), returnDest: int32(dest), want: int32(want), site: site}
	t.frames = append(t.frames, f)
	t.used, t.top = limit, base+p.parameters
	if p.argRegister >= 0 && p.needsArg && !p.usesVararg {
		arg := s.NewTable(extra, 1)
		copy(arg.array, t.stack[start:base])
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
	t.frames = append(t.frames, frame{function: fn, site: site, start: int32(start), base: int32(start + len(args))})
	t.nativeDepth++
	defer func() {
		t.nativeDepth--
		t.frames = t.frames[:depth]
		for len(t.nativeRoots) > 0 && t.nativeRoots[len(t.nativeRoots)-1].frame >= depth {
			t.nativeRoots[len(t.nativeRoots)-1] = nativeRoot{}
			t.nativeRoots = t.nativeRoots[:len(t.nativeRoots)-1]
		}
	}()
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
	f := &t.frames[len(t.frames)-1]
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

func (s *State) execute(stop int) (result []Value) {
	t := s.current

	if s.options.Hook != nil {
		s.steps += vmCallWeight - 1
		s.tick()
	}

	for len(t.frames) > stop {
		frameIndex := len(t.frames) - 1
		f := &t.frames[frameIndex]
		proto := f.function.proto
		base, pc := int(f.base), int(f.pc)
		code, constants := proto.code, proto.constants
		registers := (*[256]Value)(t.stack[base : base+256])
		remaining := s.options.HookEvery - s.steps
		for {
			word := code[pc]
			pc++
			op, register := opcode(word), uint8(word>>8)
			switch op {
			case comparisonBase + 0, comparisonBase + 1, comparisonBase + 2, comparisonBase + 3, comparisonBase + 4, comparisonBase + 5, comparisonBase + 6, comparisonBase + 7, comparisonBase + 8, comparisonBase + 9, comparisonBase + 10, comparisonBase + 11, comparisonBase + 12, comparisonBase + 13, comparisonBase + 14, comparisonBase + 15, comparisonBase + 16, comparisonBase + 17, comparisonBase + 18, comparisonBase + 19, comparisonBase + 20, comparisonBase + 21, comparisonBase + 22, comparisonBase + 23:
				mode := op - comparisonBase
				left, right := &registers[uint8(word>>8)], &registers[uint8(word>>16)]
				if mode&1 != 0 {
					left = &constants[uint8(word>>8)]
				}
				if mode&2 != 0 {
					right = &constants[uint8(word>>16)]
				}
				var matched bool
				if left.object == &numberMarker && right.object == &numberMarker {
					switch mode / 8 {
					case 0:
						matched = left.number == right.number
					case 1:
						matched = left.number < right.number
					case 2:
						matched = left.number <= right.number
					}
				} else {
					t.frames[frameIndex].pc = int32(pc)
					s.steps = s.options.HookEvery - remaining
					i := f.function.proto.instruction(pc - 1)
					matched = s.binary(i.text, *left, *right, i.left, i.right).Truth()
					f = &t.frames[frameIndex]
					registers = (*[256]Value)(t.stack[base : base+256])
					remaining = s.options.HookEvery - s.steps
				}
				if matched == (mode&4 != 0) {
					target := int(uint32(code[pc] >> 16))
					if target < pc {
						remaining -= vmCallWeight
						if remaining <= 0 {
							t.frames[frameIndex].pc = int32(pc)
							s.pollHook()
							f = &t.frames[frameIndex]
							registers = (*[256]Value)(t.stack[base : base+256])
							remaining = s.options.HookEvery - s.steps
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
					registers[register] = Number(luaNaN(left.number+right.number, left.number, right.number))
					continue
				}
				t.frames[frameIndex].pc = int32(pc)
				s.steps = s.options.HookEvery - remaining
				if s.enterArithmeticMethod(*left, *right, 0, base+int(register)) {
					frameIndex++
					f = &t.frames[frameIndex]
					base, pc = int(f.base), 0
					code, constants = f.function.proto.code, f.function.proto.constants
					registers = (*[256]Value)(t.stack[base : base+256])
					remaining = s.options.HookEvery - s.steps
					continue
				}
			case opSubtract, opSubtract + 1, opSubtract + 2, opSubtract + 3:
				left, right := numericOperands(op, word, registers, constants)
				if left.object == &numberMarker && right.object == &numberMarker {
					registers[register] = Number(luaNaN(left.number-right.number, left.number, right.number))
					continue
				}
				t.frames[frameIndex].pc = int32(pc)
				s.steps = s.options.HookEvery - remaining
				if s.enterArithmeticMethod(*left, *right, 1, base+int(register)) {
					frameIndex++
					f = &t.frames[frameIndex]
					base, pc = int(f.base), 0
					code, constants = f.function.proto.code, f.function.proto.constants
					registers = (*[256]Value)(t.stack[base : base+256])
					remaining = s.options.HookEvery - s.steps
					continue
				}
			case opMultiply, opMultiply + 1, opMultiply + 2, opMultiply + 3:
				left, right := numericOperands(op, word, registers, constants)
				if left.object == &numberMarker && right.object == &numberMarker {
					registers[register] = Number(luaNaN(left.number*right.number, left.number, right.number))
					continue
				}
				t.frames[frameIndex].pc = int32(pc)
				s.steps = s.options.HookEvery - remaining
				if s.enterArithmeticMethod(*left, *right, 2, base+int(register)) {
					frameIndex++
					f = &t.frames[frameIndex]
					base, pc = int(f.base), 0
					code, constants = f.function.proto.code, f.function.proto.constants
					registers = (*[256]Value)(t.stack[base : base+256])
					remaining = s.options.HookEvery - s.steps
					continue
				}
			case opDivide, opDivide + 1, opDivide + 2, opDivide + 3:
				left, right := numericOperands(op, word, registers, constants)
				if left.object == &numberMarker && right.object == &numberMarker {
					registers[register] = Number(luaDivide(left.number, right.number))
					continue
				}
				t.frames[frameIndex].pc = int32(pc)
				s.steps = s.options.HookEvery - remaining
				if s.enterArithmeticMethod(*left, *right, 3, base+int(register)) {
					frameIndex++
					f = &t.frames[frameIndex]
					base, pc = int(f.base), 0
					code, constants = f.function.proto.code, f.function.proto.constants
					registers = (*[256]Value)(t.stack[base : base+256])
					remaining = s.options.HookEvery - s.steps
					continue
				}
			case opModulo, opModulo + 1, opModulo + 2, opModulo + 3:
				left, right := numericOperands(op, word, registers, constants)
				if left.object == &numberMarker && right.object == &numberMarker {
					registers[register] = Number(luaModulo(left.number, right.number))
					continue
				}
				t.frames[frameIndex].pc = int32(pc)
				s.steps = s.options.HookEvery - remaining
				if s.enterArithmeticMethod(*left, *right, 4, base+int(register)) {
					frameIndex++
					f = &t.frames[frameIndex]
					base, pc = int(f.base), 0
					code, constants = f.function.proto.code, f.function.proto.constants
					registers = (*[256]Value)(t.stack[base : base+256])
					remaining = s.options.HookEvery - s.steps
					continue
				}
			case opPower, opPower + 1, opPower + 2, opPower + 3:
				left, right := numericOperands(op, word, registers, constants)
				if left.object == &numberMarker && right.object == &numberMarker {
					registers[register] = Number(luaPower(left.number, right.number))
					continue
				}
				t.frames[frameIndex].pc = int32(pc)
				s.steps = s.options.HookEvery - remaining
				if s.enterArithmeticMethod(*left, *right, 5, base+int(register)) {
					frameIndex++
					f = &t.frames[frameIndex]
					base, pc = int(f.base), 0
					code, constants = f.function.proto.code, f.function.proto.constants
					registers = (*[256]Value)(t.stack[base : base+256])
					remaining = s.options.HookEvery - s.steps
					continue
				}
			case numericBase + 24, numericBase + 25, numericBase + 26, numericBase + 27, numericBase + 28, numericBase + 29, numericBase + 30, numericBase + 31, numericBase + 32, numericBase + 33, numericBase + 34, numericBase + 35, numericBase + 36, numericBase + 37, numericBase + 38, numericBase + 39, numericBase + 40, numericBase + 41, numericBase + 42, numericBase + 43, numericBase + 44, numericBase + 45, numericBase + 46, numericBase + 47, numericJumpBase + 0, numericJumpBase + 1, numericJumpBase + 2, numericJumpBase + 3, numericJumpBase + 4, numericJumpBase + 5, numericJumpBase + 6, numericJumpBase + 7, numericJumpBase + 8, numericJumpBase + 9, numericJumpBase + 10, numericJumpBase + 11, numericJumpBase + 12, numericJumpBase + 13, numericJumpBase + 14, numericJumpBase + 15, numericJumpBase + 16, numericJumpBase + 17, numericJumpBase + 18, numericJumpBase + 19, numericJumpBase + 20, numericJumpBase + 21, numericJumpBase + 22, numericJumpBase + 23:
				operation := op
				fused := op >= numericJumpBase
				if fused {
					operation -= numericJumpBase - (numericBase + 6*4)
				}
				left, right := numericOperands(operation, word, registers, constants)
				if left.object == &numberMarker && right.object == &numberMarker {
					x, y := left.number, right.number
					var value Value
					switch (operation - numericBase) / 4 {
					case 6:
						value = Bool(x < y)
					case 7:
						value = Bool(x <= y)
					case 8:
						value = Bool(x > y)
					case 9:
						value = Bool(x >= y)
					case 10:
						value = Bool(x == y)
					case 11:
						value = Bool(x != y)
					}
					registers[register] = value
					if fused {
						branch := code[pc]
						pc++
						if value.Truth() == (opcode(branch) == opJumpTrue) {
							target := int(uint32(branch >> 16))
							if target < pc {
								remaining -= vmCallWeight
								if remaining <= 0 {
									t.frames[frameIndex].pc = int32(pc)
									s.pollHook()
									f = &t.frames[frameIndex]
									registers = (*[256]Value)(t.stack[base : base+256])
									remaining = s.options.HookEvery - s.steps
								}
							}
							pc = target
						}
					}
					continue
				}
			case opPoll:
				remaining -= vmPollStride
				if remaining <= 0 {
					t.frames[frameIndex].pc = int32(pc)
					s.pollHook()
					f = &t.frames[frameIndex]
					registers = (*[256]Value)(t.stack[base : base+256])
					remaining = s.options.HookEvery - s.steps
				}

				continue

			case opCallDirect, opCallDirectUpvalue, opCallDirectConstant, opCallDirectUpvalueConstant, opCallArithmetic, opCallArithmeticUpvalue:
				mode := op - opCallDirect
				value := registers[uint8(word>>16)]
				if mode&1 != 0 {
					value = f.function.upvalues[uint8(word>>16)].get()
				}
				var argument Value
				if op == opCallArithmetic || op == opCallArithmeticUpvalue {
					if value.Type() != TypeFunction || value.object.function.proto == nil || value.object.function.proto.vararg {
						registers[register] = value
						continue
					}
					packed := code[pc]
					operation := opcode(packed) - numericBase
					left, right := &registers[uint8(packed>>16)], &registers[uint8(packed>>32)]
					if operation&1 != 0 {
						left = &constants[uint16(packed>>16)]
					}
					if operation&2 != 0 {
						right = &constants[uint16(packed>>32)]
					}
					if left.object != &numberMarker || right.object != &numberMarker {
						registers[register] = value
						continue
					}
					x, y := left.number, right.number
					switch operation / 4 {
					case 0:
						argument = Number(luaNaN(x+y, x, y))
					case 1:
						argument = Number(luaNaN(x-y, x, y))
					case 2:
						argument = Number(luaNaN(x*y, x, y))
					case 3:
						argument = Number(luaDivide(x, y))
					case 4:
						argument = Number(luaModulo(x, y))
					case 5:
						argument = Number(luaPower(x, y))
					}
				} else {
					argument = registers[uint8(word>>24)]
					if mode&2 != 0 {
						argument = constants[uint16(word>>24)]
					}
				}
				var callee *function
				var proto *Proto
				if object := value.object; object != nil && object.kind == TypeFunction {
					callee = object.function
					proto = callee.proto
				}
				start := max(t.used, t.top)
				if proto != nil && !proto.vararg && len(t.frames) < freshCalls-1 && start+256 <= len(t.stack) && (argument.Type() != TypeString || len(argument.Text()) <= s.options.MaxString) {
					pc += 2
					remaining -= vmCallWeight
					if remaining <= 0 {
						s.pollPreparedHook(frameIndex, pc, int(register), value, argument)
						remaining = s.options.HookEvery - s.steps
					}
					t.frames[frameIndex].pc = int32(pc)
					parameters, size := proto.parameters, proto.maxRegisters
					if parameters > 0 {
						t.stack[start] = argument
					}
					clearRegisters(t.stack[start+min(parameters, 1) : start+size])
					depth := len(t.frames)
					if depth == cap(t.frames) {
						t.frames = append(t.frames, frame{})
					} else {
						t.frames = t.frames[:depth+1]
					}
					entered := &t.frames[depth]
					entered.function, entered.site = callee, nil
					entered.start, entered.base, entered.pc = int32(start), int32(start), 0
					entered.returnDest, entered.want = int32(base+int(uint8(word>>40))), int32(int16(word>>48))
					entered.metacallTop, entered.tailcalls = 0, 0
					t.used, t.top = start+size, start+parameters
					frameIndex++
					f = &t.frames[frameIndex]
					base, pc = start, 0
					code, constants = proto.code, proto.constants
					registers = (*[256]Value)(t.stack[base : base+256])
					continue
				}
				registers[register] = value
				continue
			case opCall:
				remaining -= vmCallWeight
				if remaining <= 0 {
					t.frames[frameIndex].pc = int32(pc)
					s.pollHook()
					f = &t.frames[frameIndex]
					registers = (*[256]Value)(t.stack[base : base+256])
					remaining = s.options.HookEvery - s.steps
				}

				count := int(int16(word >> 16))
				value := registers[register]
				if count >= 0 && value.Type() == TypeFunction && value.object.function.proto != nil && !value.object.function.proto.vararg && len(t.frames) < freshCalls-1 {
					callee := value.object.function
					start := max(t.used, t.top)
					if start+256 <= len(t.stack) {
						t.frames[frameIndex].pc = int32(pc)
						parameters := callee.proto.parameters
						provided := min(parameters, count)
						for argument := 0; argument < provided; argument++ {
							t.stack[start+argument] = registers[int(register)+1+argument]
						}
						clearRegisters(t.stack[start+provided : start+callee.proto.maxRegisters])
						depth := len(t.frames)
						if depth == cap(t.frames) {
							t.frames = append(t.frames, frame{})
						} else {
							t.frames = t.frames[:depth+1]
						}
						entered := &t.frames[depth]
						entered.function, entered.site = callee, nil
						entered.start, entered.base, entered.pc = int32(start), int32(start), 0
						entered.returnDest, entered.want = int32(base+int(uint8(word>>48))), int32(int16(word>>32))
						entered.metacallTop, entered.tailcalls = 0, 0
						t.used, t.top = start+callee.proto.maxRegisters, start+parameters
						frameIndex++
						f = &t.frames[frameIndex]
						proto = callee.proto
						base, pc = start, 0
						code, constants = proto.code, proto.constants
						registers = (*[256]Value)(t.stack[base : base+256])
						continue
					}
				}
			case opReturn:
				if int16(word>>16) == 1 && f.want == 1 && f.returnDest >= 0 && f.metacallTop == 0 && frameIndex != stop && (t.open == nil || t.open.index < base) {
					dest := int(f.returnDest)
					t.stack[dest] = registers[register]
					t.used, t.top = int(f.start), dest+1
					t.frames = t.frames[:frameIndex]
					frameIndex--
					f = &t.frames[frameIndex]
					proto = f.function.proto
					base, pc = int(f.base), int(f.pc)
					code, constants = proto.code, proto.constants
					registers = (*[256]Value)(t.stack[base : base+256])
					next := code[pc]
					if opcode(next) == opMove {
						registers[uint8(next>>8)] = registers[uint8(next>>16)]
						pc++
					}
					continue
				}
				if f.returnDest >= 0 {
					count := int(int16(word >> 16))
					if count < 0 {
						count = t.top - base - int(register)
					}
					wanted, dest := int(f.want), int(f.returnDest)
					if wanted < 0 {
						wanted = count
					}
					if t.open != nil && t.open.index >= base {
						t.close(base)
					}
					if wanted == 1 {
						if count > 0 {
							t.stack[dest] = registers[register]
						} else {
							t.stack[dest] = Nil()
						}
					} else {
						copy(t.stack[dest:dest+wanted], t.stack[base+int(register):base+int(register)+count])
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
					if frameIndex == stop {
						s.steps = s.options.HookEvery - remaining
						return nil
					}
					frameIndex--
					f = &t.frames[frameIndex]
					proto = f.function.proto
					base, pc = int(f.base), int(f.pc)
					code, constants = proto.code, proto.constants
					registers = (*[256]Value)(t.stack[base : base+256])
					next := code[pc]
					if opcode(next) == opMove {
						registers[uint8(next>>8)] = registers[uint8(next>>16)]
						pc++
					}
					continue
				}
			case opConstant:
				value := constants[uint32(word>>16)]
				if value.Type() == TypeString && len(value.Text()) > s.options.MaxString {
					t.frames[frameIndex].pc = int32(pc)
					s.runtimeError("string length overflow")
				}
				registers[register] = value
				continue
			case opMove:
				registers[register] = registers[uint8(word>>16)]
				continue
			case opLoadNil:
				clear(registers[int(register) : int(uint32(word>>16))+1])
				continue
			case opGetUpvalue:
				registers[register] = f.function.upvalues[uint32(word>>16)].get()
				continue
			case opSetUpvalue:
				f.function.upvalues[register].set(registers[uint8(word>>16)])
				continue
			case opGetGlobal:
				value := f.function.env.RawGet(constants[uint32(word>>16)])
				if !value.IsNil() {
					registers[register] = value
					continue
				}
			case opGetTable, opGetTableK:
				value := registers[uint8(word>>16)]
				if value.Type() == TypeTable {
					key := registers[uint8(word>>48)]
					if op == opGetTableK {
						key = constants[uint16(word>>48)]
					}
					found := value.object.table.RawGet(key)
					if !found.IsNil() {
						registers[register] = found
						continue
					}
				}
			case opSetTable:
				value := registers[register]
				if value.Type() == TypeTable && !value.object.table.readonly && value.object.table.metatable == nil {
					table := value.object.table
					key := registers[uint8(word>>16)]
					if key.object == &numberMarker {
						index := int(key.number)
						if index > 0 && index <= len(table.array) && float64(index) == key.number {
							table.setArray(index-1, registers[uint8(word>>48)])
							continue
						}
					}
					t.frames[frameIndex].pc = int32(pc)
					if err := table.RawSet(key, registers[uint8(word>>48)]); err != nil {
						s.runtimeError(err.Error())
					}
					continue
				}
			case opNegate:
				value := registers[uint8(word>>16)]
				if value.object == &numberMarker {
					registers[register] = Number(-value.number)
					continue
				}
			case opNot:
				registers[register] = Bool(!registers[uint8(word>>16)].Truth())
				continue
			case opLength:
				value := registers[uint8(word>>16)]
				if value.Type() == TypeString {
					registers[register] = Number(float64(len(value.Text())))
					continue
				}
				if value.Type() == TypeTable {
					registers[register] = Number(float64(value.object.table.Len()))
					continue
				}
			case opJump:
				if register > 0 {
					t.close(base + int(register) - 1)
				}
				target := int(uint32(word >> 16))
				if target < pc {
					remaining -= vmCallWeight
					if remaining <= 0 {
						t.frames[frameIndex].pc = int32(pc)
						s.pollHook()
						f = &t.frames[frameIndex]
						registers = (*[256]Value)(t.stack[base : base+256])
						remaining = s.options.HookEvery - s.steps
					}
				}
				pc = target
				continue
			case opJumpFalse:
				if !registers[register].Truth() {
					target := int(uint32(word >> 16))
					if target < pc {
						remaining -= vmCallWeight
						if remaining <= 0 {
							t.frames[frameIndex].pc = int32(pc)
							s.pollHook()
							f = &t.frames[frameIndex]
							registers = (*[256]Value)(t.stack[base : base+256])
							remaining = s.options.HookEvery - s.steps
						}
					}
					pc = target
				}
				continue
			case opJumpTrue:
				if registers[register].Truth() {
					target := int(uint32(word >> 16))
					if target < pc {
						remaining -= vmCallWeight
						if remaining <= 0 {
							t.frames[frameIndex].pc = int32(pc)
							s.pollHook()
							f = &t.frames[frameIndex]
							registers = (*[256]Value)(t.stack[base : base+256])
							remaining = s.options.HookEvery - s.steps
						}
					}
					pc = target
				}
				continue
			case opClose:
				if t.open != nil {
					t.close(base + int(register))
				}
				continue
			case opForLoop, opForAccumulate:
				remaining -= vmCallWeight
				if remaining <= 0 {
					t.frames[frameIndex].pc = int32(pc)
					s.pollHook()
					f = &t.frames[frameIndex]
					registers = (*[256]Value)(t.stack[base : base+256])
					remaining = s.options.HookEvery - s.steps
				}
				step := registers[register+2].number
				next := registers[register].number + step
				limit := registers[register+1].number
				if step > 0 && next <= limit || step <= 0 && limit <= next {
					registers[register].number = next
					registers[register+3] = Number(next)
					pc = int(uint32(word >> 16))
					if op == opForAccumulate {
						sum := &registers[uint8(word>>48)]
						if sum.object == &numberMarker {
							sum.number = luaNaN(sum.number+next, sum.number, next)
							pc++
						}
					}
				}
				continue
			}
			t.frames[frameIndex].pc = int32(pc)
			s.steps = s.options.HookEvery - remaining
			if values, done := s.executeSlow(frameIndex, pc, remaining); done {
				return values
			}
			break
		}
	}
	return nil
}

func (s *State) enterArithmeticMethod(left, right Value, index, dest int) bool {
	method := s.binaryTableMethod(left, index)
	if method.IsNil() {
		method = s.binaryTableMethod(right, index)
	}
	if method.Type() != TypeFunction || method.object.function.proto == nil {
		return false
	}
	t := s.current
	s.steps += vmCallWeight
	if s.steps >= s.options.HookEvery {
		s.pollBinaryHook(method, left, right)
	}
	t.cDepth++
	if t.cDepth == 200 {
		s.runtimeError("C stack overflow")
	}
	if t.cDepth >= 225 {
		panic(plainError("error in error handling"))
	}
	previousTop := t.top
	callee := method.object.function
	proto := callee.proto
	start := max(t.used, t.top)
	if !proto.vararg && len(t.frames) < freshCalls-1 && start+256 <= len(t.stack) {
		parameters := proto.parameters
		provided := min(parameters, 2)
		if provided > 0 {
			t.stack[start] = left
		}
		if provided > 1 {
			t.stack[start+1] = right
		}
		clearRegisters(t.stack[start+provided : start+proto.maxRegisters])
		depth := len(t.frames)
		if depth == cap(t.frames) {
			t.frames = append(t.frames, frame{})
		} else {
			t.frames = t.frames[:depth+1]
		}
		entered := &t.frames[depth]
		entered.function, entered.site = callee, nil
		entered.start, entered.base, entered.pc = int32(start), int32(start), 0
		entered.returnDest, entered.want = int32(dest), 1
		entered.metacallTop, entered.tailcalls = int32(previousTop+1), 0
		t.used, t.top = start+proto.maxRegisters, start+parameters
	} else {
		arguments := [2]Value{left, right}
		s.push(callee, arguments[:], dest, 1, nil)
		t.frames[len(t.frames)-1].metacallTop = int32(previousTop + 1)
	}
	t.boundary++
	return true
}

func (s *State) meta(value Value, name string) Value {
	var table *Table
	if value.Type() == TypeTable {
		table = value.Table().metatable
	}
	if value.Type() == TypeString {
		table = s.stringMeta
	}
	if table != nil {
		return table.RawGet(String(name))
	}
	return Nil()
}

func (s *State) get(value, key Value, info nameInfo) Value {
	for depth := 0; depth < 100; depth++ {
		if value.Type() == TypeTable {
			result := value.Table().RawGet(key)
			if !result.IsNil() {
				return result
			}
		}
		meta := s.meta(value, "__index")
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
		if meta.Type() == TypeFunction {
			s.invokeOne(meta, []Value{value, key, assigned})
			return
		}
		value, info = meta, nameInfo{}
	}
	s.runtimeError("loop in settable")
}

func (s *State) callable(value Value, args []Value, info nameInfo) (Value, []Value) {
	meta := s.meta(value, "__call")
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
		if !equal && left.Type() == right.Type() && (left.Type() == TypeTable || left.Type() == TypeUserdata) {
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
	product := float64(math.Floor(left/right) * right)
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
		method = s.meta(left, "__lt")
		if !method.IsNil() && method.Equal(s.meta(right, "__lt")) {
			return !s.invokeOne(method, []Value{right, left}).Truth()
		}
	}
	if left.Type() == right.Type() {
		s.runtimeError(fmt.Sprintf("attempt to compare two %s values", left.Type()))
	}
	s.runtimeError(fmt.Sprintf("attempt to compare %s with %s", left.Type(), right.Type()))
	return false
}

func (s *State) executeSlow(frameIndex, pc, remaining int) ([]Value, bool) {
	t := s.current
	f := &t.frames[frameIndex]
	base := int(f.base)
	constants := f.function.proto.constants
	i := f.function.proto.instruction(pc - 1)
	if i.op == opTailCall || i.op == opForCall {
		remaining -= vmCallWeight
		if remaining <= 0 {
			t.frames[frameIndex].pc = int32(pc)
			s.pollHook()
			f = &t.frames[frameIndex]
			remaining = s.options.HookEvery - s.steps
		}

		s.steps = s.options.HookEvery - remaining
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
	case opTestSet:
		if t.stack[b].Truth() == (i.c != 0) {
			t.stack[a] = t.stack[b]
		} else {
			t.frames[frameIndex].pc = int32(i.d)
		}
	case opConcat:
		for reg := i.c; reg > i.b; reg-- {
			leftName, rightName := nameInfo{kind: "register", register: int16(reg - 1)}, nameInfo{kind: "register", register: int16(reg)}
			value := s.binary("..", t.stack[base+reg-1], t.stack[base+reg], leftName, rightName)
			t.stack[base+reg-1] = value
		}
		t.stack[a] = t.stack[b]
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
		if count > 0 {
			table.sequence = -1
		}
	case opClosure:
		child := f.function.proto.children[i.b]
		upvalues := make([]*upvalue, len(child.upvalues))
		for index, descriptor := range child.upvalues {
			if descriptor.local {
				upvalues[index] = t.capture(base + descriptor.index)
			} else {
				upvalues[index] = f.function.upvalues[descriptor.index]
			}
		}
		t.stack[a] = s.closure(child, f.function.env, upvalues)
	case opVararg:
		t.writeResults(a, i.b, t.stack[int(f.start):int(f.base)])
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
			remaining -= vmCallWeight
			if remaining <= 0 {
				s.pollBinaryHook(method, left, right)
				remaining = s.options.HookEvery - s.steps
			}
			s.steps = s.options.HookEvery - remaining
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
	case opForCall:
		values := s.invoke(t.stack[a], []Value{t.stack[a+1], t.stack[a+2]})
		t.writeResults(a+3, i.c, values)
		if !t.stack[a+3].IsNil() {
			t.stack[a+2] = t.stack[a+3]
			t.frames[frameIndex].pc = int32(i.b)
		}
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
