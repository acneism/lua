package lua

const (
	indexFound = iota
	indexMethod
	indexSlow
)

func (s *State) indexChain(value, key Value) (Value, Value, int) {
	for range 100 {
		if value.Type() == TypeString {
			method := s.eventMethod(s.stringMeta, eventIndex)
			if method.Type() == TypeFunction {
				return method, value, indexMethod
			}
			if method.Type() != TypeTable {
				return Nil(), Nil(), indexSlow
			}
			value = method
			continue
		}
		if value.Type() != TypeTable {
			return Nil(), Nil(), indexSlow
		}
		table := value.object.table
		var result Value
		if key.text != nil {
			result = table.getString(key.text)
		} else {
			result = table.RawGet(key)
		}
		if !result.IsNil() || table.metatable == nil {
			return result, Nil(), indexFound
		}
		method := s.eventMethod(table.metatable, eventIndex)
		if method.IsNil() {
			return Nil(), Nil(), indexFound
		}
		if method.Type() == TypeFunction {
			return method, value, indexMethod
		}
		value = method
	}
	return Nil(), Nil(), indexSlow
}

func (s *State) enterMetaCall() {
	t := s.current
	t.cDepth++
	if t.cDepth == 200 {
		s.runtimeError("C stack overflow")
	}
	if t.cDepth >= 225 {
		panic(plainError("error in error handling"))
	}
}

func (s *State) enterMethod(method Value, count int, first, second, third Value, dest, want int) {
	t := s.current
	s.steps += vmCallWeight
	if s.steps >= s.options.HookEvery {
		roots := [4]Value{method, first, second, third}
		s.pollHookRoots(roots[:count+1])
	}
	s.enterMetaCall()
	previousTop := t.top
	callee := method.object.function
	proto := callee.proto
	start := max(t.used, t.top)
	if !proto.vararg && len(t.frames) < freshCalls-1 && start+256 <= len(t.stack) {
		parameters := proto.parameters
		provided := min(parameters, count)
		if provided > 0 {
			t.stack[start] = first
		}
		if provided > 1 {
			t.stack[start+1] = second
		}
		if provided > 2 {
			t.stack[start+2] = third
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
		entered.returnDest, entered.want = int32(dest), int32(want)
		entered.metacallTop, entered.tailcalls = int32(previousTop+1), 0
		t.used, t.top = start+proto.maxRegisters, start+parameters
	} else {
		arguments := [3]Value{first, second, third}
		s.push(callee, arguments[:count], dest, want, nil)
		t.frames[len(t.frames)-1].metacallTop = int32(previousTop + 1)
	}
	t.boundary++
}

const compareWant = -2

func (s *State) enterCompareMethod(left, right Value, mode int) bool {
	kind := mode >> 2 & 1
	var method Value
	switch mode / 8 {
	case 0:
		if left.Type() != right.Type() || left.Type() != TypeTable && left.Type() != TypeUserdata || left.Equal(right) {
			return false
		}
		method = s.metaFor(left, eventEqual)
		if !method.Equal(s.metaFor(right, eventEqual)) {
			return false
		}
	case 1:
		if left.Type() == right.Type() && (left.Type() == TypeNumber || left.Type() == TypeString) {
			return false
		}
		method = s.metaFor(left, eventLess)
		if !method.Equal(s.metaFor(right, eventLess)) {
			return false
		}
	default:
		if left.Type() == right.Type() && (left.Type() == TypeNumber || left.Type() == TypeString) {
			return false
		}
		method = s.metaFor(left, eventLessEqual)
		if method.IsNil() || !method.Equal(s.metaFor(right, eventLessEqual)) {
			method = s.metaFor(left, eventLess)
			if !method.Equal(s.metaFor(right, eventLess)) {
				return false
			}
			left, right = right, left
			kind |= 2
		}
	}
	if method.Type() != TypeFunction || method.object.function.proto == nil {
		return false
	}
	s.enterPair(method, left, right, 0, compareWant-kind)
	return true
}

func (s *State) returnCompare(value Value) {
	t := s.current
	f := &t.frames[len(t.frames)-1]
	if t.open != nil && t.open.index >= int(f.base) {
		t.close(int(f.base))
	}
	kind := compareWant - int(f.want)
	t.used, t.top = int(f.start), int(f.metacallTop)-1
	t.cDepth--
	t.boundary--
	t.frames = t.frames[:len(t.frames)-1]
	s.jumpOn(value.Truth() != (kind&2 != 0) == (kind&1 != 0))
}

func (s *State) compareSlow(left, right Value, mode opcode) {
	t := s.current
	f := &t.frames[len(t.frames)-1]
	i := f.function.proto.instruction(int(f.pc) - 1)
	s.jumpOn(s.binary(i.text, left, right, i.left, i.right).Truth() == (mode&4 != 0))
}

func (s *State) jumpOn(jump bool) {
	t := s.current
	caller := &t.frames[len(t.frames)-1]
	pc := int(caller.pc)
	if !jump {
		caller.pc = int32(pc + 1)
		return
	}
	target := int(uint32(caller.function.proto.code[pc] >> 16))
	caller.pc = int32(target)
	if target < pc {
		s.steps += vmCallWeight
		if s.steps >= s.options.HookEvery {
			s.pollJump(len(t.frames)-1, pc, target)
		}
	}
}

func (s *State) callMethod(method Value, count int, first, second, third Value, site *instruction) []Value {
	t := s.current
	s.enterMetaCall()
	t.boundary++
	arguments := [3]Value{first, second, third}
	values := s.native(method.object.function, arguments[:count], site)
	t.cDepth--
	t.boundary--
	return values
}
