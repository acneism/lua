package lua

import "sort"

func (s *State) openCoroutine() {
	table := s.NewTable(0, 8)
	functions := map[string]GoFunction{
		"create": func(s *State, args []Value) ([]Value, error) {
			return []Value{s.createThread(args).object.threadValue()}, nil
		},
		"wrap": func(s *State, args []Value) ([]Value, error) {
			t := s.createThread(args)
			wrapped := s.Function("", func(s *State, args []Value) ([]Value, error) {
				values, err := s.resume(t, args)
				if err != nil {
					if abortError(err) {
						return nil, err
					}
					failure := err.(*Error)
					if text, ok := failure.Value.toString(); ok {
						s.raise(text, 1)
					}
					return nil, failure
				}
				return values, nil
			})
			return []Value{wrapped}, nil
		},
		"resume": func(s *State, args []Value) ([]Value, error) {
			t := s.checkThread(args, 0)
			values, err := s.resume(t, args[1:])
			if abortError(err) {
				return nil, err
			}
			if err != nil {
				return []Value{Bool(false), err.(*Error).Value}, nil
			}
			return append([]Value{Bool(true)}, values...), nil
		},
		"yield": func(s *State, args []Value) ([]Value, error) {
			t := s.current
			if t == s.main || t.boundary > 0 || t.nativeDepth > 1 {
				return nil, plainError("attempt to yield across metamethod/C-call boundary")
			}
			return nil, &yieldSignal{values: args}
		},
		"status": func(s *State, args []Value) ([]Value, error) {
			return []Value{String(s.checkThread(args, 0).status)}, nil
		},
		"running": func(s *State, _ []Value) ([]Value, error) {
			if s.current == s.main {
				return []Value{Nil()}, nil
			}
			return []Value{s.current.object.threadValue()}, nil
		},
	}
	names := make([]string, 0, len(functions))
	for name := range functions {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		table.set(String(name), s.Function(name, functions[name]))
	}
	s.globals.set(String("coroutine"), table.Value())
}

func (o *object) threadValue() Value { return Value{object: o} }

func (s *State) createThread(args []Value) *thread {
	fn := argument(args, 0)
	if fn.Type() != TypeFunction || fn.object.function.proto == nil {
		s.argError(1, "Lua function expected")
	}
	s.nextID++
	t := &thread{entry: fn, status: "suspended"}
	t.object = object{kind: TypeThread, id: s.nextID, thread: t}
	s.threads = append(s.threads, t)
	return t
}

func (s *State) checkThread(args []Value, index int) *thread {
	value := argument(args, index)
	if value.Type() != TypeThread {
		s.argError(index+1, "coroutine expected")
	}
	return value.object.thread
}

func (s *State) resume(t *thread, args []Value) (values []Value, err error) {
	if t.status != "suspended" {
		return nil, plainError("cannot resume " + t.status + " coroutine")
	}
	parent := s.current
	if parent.cDepth >= 200 {
		return nil, plainError("C stack overflow")
	}
	t.cDepth = parent.cDepth + 1
	parent.status = "normal"
	s.current = t
	t.parent = parent
	t.status = "running"
	defer func() {
		t.cDepth = 0
		if raised := recover(); raised != nil {
			if failure, ok := raised.(*Error); ok {
				err = failure
			} else {
				panic(raised)
			}
		}
		if err != nil || t.status != "suspended" {
			t.status = "dead"
			t.close(0)
			clear(t.stack)
			t.frames = t.frames[:0]
			t.used, t.top, t.nativeDepth = 0, 0, 0
		}
		s.current = parent
		parent.status = "running"
		t.parent = nil
	}()
	if !t.started {
		t.started = true
		return s.protectedCall(t.entry, args, false)
	}
	if t.resumeReturn {
		done, result := s.finish(args)
		if done {
			return result, nil
		}
	} else {
		t.writeResults(t.resumeDest, t.resumeWant, args)
	}
	return s.execute(0), nil
}
