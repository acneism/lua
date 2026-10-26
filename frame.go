package lua

type Frame struct {
	What   string
	Source string
	Line   int
}

func (s *State) Frame(level int) (Frame, bool) {
	if level < 0 {
		return Frame{}, false
	}
	for i := len(s.current.frames) - 1; i >= 0; i-- {
		frame := &s.current.frames[i]
		if level == 0 {
			if frame.function.proto == nil {
				return Frame{What: "C", Source: "=[C]", Line: -1}, true
			}
			what := "Lua"
			if frame.function.proto.line == 0 {
				what = "main"
			}
			return Frame{What: what, Source: frame.function.proto.chunk, Line: frame.sourceLine()}, true
		}
		level--
		if frame.function.proto != nil {
			if int64(level) < frame.tailcalls {
				return Frame{What: "tail", Source: "=(tail call)", Line: -1}, true
			}
			level -= int(frame.tailcalls)
		}
	}
	return Frame{}, false
}
