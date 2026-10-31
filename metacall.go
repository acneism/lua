package lua

const (
	indexFound = iota
	indexMethod
	indexSlow
)

func (s *State) indexChain(value, key Value) (Value, Value, int) {
	for range 100 {
		if value.Type() != TypeTable {
			return Nil(), Nil(), indexSlow
		}
		table := value.object.table
		result := table.RawGet(key)
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
		arguments := [3]Value{first, second, third}
		copy(t.stack[start:start+provided], arguments[:provided])
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
