package lua

type handlerOwner struct{ sequence uint64 }

func (s *State) CallWithHandler(fn, handler Value, args ...Value) ([]Value, error) {
	return s.call(fn, args, handler, true)
}

func (s *State) protectedHandler(fn Value, args []Value, handler Value, active bool) ([]Value, error) {
	t := s.current
	previous, previousID, previousActive := t.handler, t.handlerID, t.handlerActive
	t.handler, t.handlerActive = handler, active
	if active {
		if s.handlers == nil {
			s.handlers = &handlerOwner{}
		}
		s.handlers.sequence++
		t.handlerID = s.handlers.sequence
	} else {
		t.handlerID = 0
	}
	defer func() { t.handler, t.handlerID, t.handlerActive = previous, previousID, previousActive }()
	return s.protected(fn, args)
}

func (s *State) processPanic(raised any) any {
	failure, ok := raised.(*Error)
	if !ok || failure.Abort != nil || failure.Internal {
		return raised
	}
	t := s.current
	if failure.Line == 0 {
		failure.Line = s.currentLine()
	}
	if failure.cDepth == 0 {
		failure.cDepth = t.cDepth
	}
	if !t.handlerActive || failure.handlerOwner == s.handlers && failure.handlerID == t.handlerID {
		return failure
	}
	if t.cDepth >= 225 || t.handler.Type() != TypeFunction {
		failure = plainError("error in error handling")
		failure.Line = s.currentLine()
		failure.cDepth = t.cDepth
		failure.handlerOwner, failure.handlerID = s.handlers, t.handlerID
		return failure
	}
	t.boundary++
	values, err := s.protected(t.handler, []Value{failure.Value})
	t.boundary--
	if err != nil {
		return err
	}
	failure.Value = first(values)
	failure.Message = failure.Value.String()
	failure.handlerOwner, failure.handlerID = s.handlers, t.handlerID
	return failure
}
