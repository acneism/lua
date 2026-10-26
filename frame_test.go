package lua

import (
	"errors"
	"strings"
	"testing"
)

func TestFrameLevels(t *testing.T) {
	s := NewState(Options{})
	if _, ok := s.Frame(0); ok {
		t.Fatal("idle state has a frame")
	}
	if _, ok := s.Frame(-1); ok {
		t.Fatal("negative frame exists")
	}
	var captured []Frame
	where := s.Function("where", func(s *State, _ []Value) ([]Value, error) {
		captured = nil
		for level := 0; ; level++ {
			frame, ok := s.Frame(level)
			if !ok {
				break
			}
			captured = append(captured, frame)
		}
		return nil, nil
	})
	_ = s.Globals().RawSet(String("where"), where)
	for _, item := range []struct {
		source string
		frames []Frame
	}{
		{"return where()", []Frame{{"C", "=[C]", -1}, {"main", "@frames", 1}}},
		{"local function f() return where() end return f()", []Frame{{"C", "=[C]", -1}, {"Lua", "@frames", 1}, {"tail", "=(tail call)", -1}}},
		{"local function f() return where() end local function g() return f() end return g()", []Frame{{"C", "=[C]", -1}, {"Lua", "@frames", 1}, {"tail", "=(tail call)", -1}, {"tail", "=(tail call)", -1}}},
	} {
		proto, err := Compile([]byte(item.source), "@frames")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.Call(s.Load(proto, nil)); err != nil {
			t.Fatal(err)
		}
		if len(captured) != len(item.frames) {
			t.Fatalf("%s: %+v", item.source, captured)
		}
		for i, want := range item.frames {
			if captured[i] != want {
				t.Fatalf("level %d: got %+v, want %+v", i, captured[i], want)
			}
		}
	}
}

func TestFrameGoCallsGo(t *testing.T) {
	s := NewState(Options{})
	inner := s.Function("inner", func(s *State, _ []Value) ([]Value, error) {
		for level := range 2 {
			frame, ok := s.Frame(level)
			if !ok || frame.What != "C" || frame.Source != "=[C]" || frame.Line != -1 {
				t.Fatalf("Go frame %d: %+v %v", level, frame, ok)
			}
		}
		frame, ok := s.Frame(2)
		if !ok || frame.What != "main" {
			t.Fatalf("Lua caller: %+v %v", frame, ok)
		}
		return nil, nil
	})
	outer := s.Function("outer", func(s *State, _ []Value) ([]Value, error) { return s.Call(inner) })
	_ = s.Globals().RawSet(String("outer"), outer)
	if _, err := s.Call(s.Load(compileTest(t, "outer()"), nil)); err != nil {
		t.Fatal(err)
	}
}

func testRedisHandler(t *testing.T, s *State) Value {
	t.Helper()
	debug := s.NewTable(0, 1)
	_ = debug.RawSet(String("getinfo"), s.Function("getinfo", func(s *State, args []Value) ([]Value, error) {
		frame, ok := s.Frame(int(args[0].Float()))
		if !ok {
			return []Value{Nil()}, nil
		}
		table := s.NewTable(0, 3)
		_ = table.RawSet(String("what"), String(frame.What))
		_ = table.RawSet(String("source"), String(frame.Source))
		_ = table.RawSet(String("currentline"), Number(float64(frame.Line)))
		return []Value{table.Value()}, nil
	}))
	_ = s.Globals().RawSet(String("debug"), debug.Value())
	proto, err := Compile([]byte(redisErrorHandlerSource), "@err_handler_def")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Call(s.Load(proto, nil)); err != nil {
		t.Fatal(err)
	}
	return s.Globals().RawGet(String("__redis__err__handler"))
}

func TestHandlerSeesErrorPoint(t *testing.T) {
	for _, item := range []struct {
		name, source, chunk string
		line                int
	}{
		{"Lua error", "local function f()\nerror('x')\nend\nf()", "@user_script", 2},
		{"VM error", "local function f()\nlocal t=nil\nreturn t.x\nend\nf()", "@user_script", 3},
		{"Go calls Go", "string.gsub('a','a',error)", "=[C]", -1},
		{"named loadstring", "loadstring('error(1)','=mychunk')()", "=mychunk", 1},
		{"raw loadstring", "loadstring('error(1)')()", "error(1)", 1},
		{"handler error", "error(math)", "@err_handler_def", 10},
	} {
		t.Run(item.name, func(t *testing.T) {
			s := NewState(Options{})
			handler := testRedisHandler(t, s)
			s.Globals().RawGet(String("math")).Table().SetReadOnly(true)
			proto, err := Compile([]byte(item.source), "@user_script")
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.CallWithHandler(s.Load(proto, nil), handler)
			var failure *Error
			if !errors.As(err, &failure) || failure.Value.Type() != TypeTable {
				t.Fatalf("handler did not replace error: %v", err)
			}
			table := failure.Value.Table()
			if table.RawGet(String("source")).Text() != item.chunk || table.RawGet(String("line")).Float() != float64(item.line) {
				t.Fatalf("error point changed: source %v line %v", table.RawGet(String("source")), table.RawGet(String("line")))
			}
			if len(s.main.frames) != 0 || s.main.handlerActive {
				t.Fatal("handler state retained")
			}
		})
	}
}

func TestXpcallHandlerSeesLiveFrames(t *testing.T) {
	s := NewState(Options{})
	testRedisHandler(t, s)
	source := "local e={err='custom'}\nlocal ok,r=xpcall(function()\nerror(e)\nend,__redis__err__handler)\nreturn e.source,e.line,r==e"
	proto, err := Compile([]byte(source), "@user_script")
	if err != nil {
		t.Fatal(err)
	}
	values, err := s.Call(s.Load(proto, nil))
	if err != nil || len(values) != 3 || values[0].Text() != "@user_script" || values[1].Float() != 3 || !values[2].Truth() {
		t.Fatalf("xpcall ran after unwind: %v %v", values, err)
	}
}

func TestDirectHandlerTailFrame(t *testing.T) {
	s := NewState(Options{})
	testRedisHandler(t, s)
	proto, err := Compile([]byte("local function f() return __redis__err__handler('x') end local t=f() return t.source,t.line"), "@user_script")
	if err != nil {
		t.Fatal(err)
	}
	values, err := s.Call(s.Load(proto, nil))
	if err != nil || len(values) != 2 || values[0].Text() != "=(tail call)" || values[1].Float() != -1 {
		t.Fatalf("lost tail frame missing: %v %v", values, err)
	}
}

func TestHandlerMaskingAndAbort(t *testing.T) {
	s := NewState(Options{})
	calls := 0
	handler := s.Function("handler", func(_ *State, args []Value) ([]Value, error) { calls++; return args, nil })
	inner := s.Load(compileTest(t, "error('inner')"), nil)
	_ = s.Globals().RawSet(String("nested"), s.Function("nested", func(s *State, _ []Value) ([]Value, error) {
		_, err := s.Call(inner)
		if calls != 0 {
			t.Error("nested State.Call inherited handler")
		}
		return nil, err
	}))
	_, err := s.CallWithHandler(s.Load(compileTest(t, "pcall(function() error('caught') end) nested()"), nil), handler)
	if err == nil || calls != 1 {
		t.Fatalf("wrong handler scope: %v, %d calls", err, calls)
	}
	cause := errors.New("abort")
	_ = s.Globals().RawSet(String("abort"), s.Function("abort", func(_ *State, _ []Value) ([]Value, error) { return nil, cause }))
	_, err = s.CallWithHandler(s.Load(compileTest(t, "abort()"), nil), handler)
	if !errors.Is(err, cause) || calls != 1 {
		t.Fatalf("abort reached handler: %v %d", err, calls)
	}
	_ = s.Globals().RawSet(String("panic"), s.Function("panic", func(_ *State, _ []Value) ([]Value, error) { panic("internal") }))
	_, err = s.CallWithHandler(s.Load(compileTest(t, "panic()"), nil), handler)
	var failure *Error
	if !errors.As(err, &failure) || !failure.Internal || calls != 1 {
		t.Fatalf("internal panic reached handler: %v %d", err, calls)
	}
}

func TestHandlerRecursiveFailure(t *testing.T) {
	s := NewState(Options{})
	handler := s.Load(compileTest(t, "error('again')"), nil)
	_, err := s.CallWithHandler(s.Load(compileTest(t, "error('first')"), nil), handler)
	var failure *Error
	if !errors.As(err, &failure) || failure.Value.Text() != "error in error handling" {
		t.Fatalf("recursive handler did not stop: %v", err)
	}
	if len(s.main.frames) != 0 || s.main.cDepth != 0 || s.main.boundary != 0 {
		t.Fatal("handler recursion leaked state")
	}
	values, err := s.Call(s.Load(compileTest(t, "return 'clean'"), nil))
	if err != nil || !strings.Contains(values[0].Text(), "clean") {
		t.Fatal(values, err)
	}
}

func TestCallWithHandlerUsesCallDepth(t *testing.T) {
	s := NewState(Options{})
	probe := s.Function("probe", func(s *State, args []Value) ([]Value, error) {
		return append([]Value{Number(float64(s.current.cDepth))}, args...), nil
	})
	handler := s.Function("handler", func(_ *State, args []Value) ([]Value, error) { return args, nil })
	first, err := s.Call(probe, Number(17), String("x"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.CallWithHandler(probe, handler, Number(17), String("x"))
	if err != nil || len(first) != len(second) {
		t.Fatal(second, err)
	}
	for i := range first {
		if !first[i].Equal(second[i]) {
			t.Fatalf("CallWithHandler changed depth or args: %v %v", first, second)
		}
	}
}

func TestCoroutineDoesNotInheritHandler(t *testing.T) {
	s := NewState(Options{})
	calls := 0
	handler := s.Function("handler", func(_ *State, args []Value) ([]Value, error) { calls++; return args, nil })
	fn := s.Load(compileTest(t, "local c=coroutine.create(function() error('child') end) return coroutine.resume(c)"), nil)
	values, err := s.CallWithHandler(fn, handler)
	if err != nil || calls != 0 || len(values) != 2 || values[0].Truth() {
		t.Fatalf("coroutine inherited handler: %v %v %d", values, err, calls)
	}
}

const redisErrorHandlerSource = `local dbg = debug
debug = nil
function __redis__err__handler(err)
  local i = dbg.getinfo(2,'nSl')
  if i and i.what == 'C' then
    i = dbg.getinfo(3,'nSl')
  end
  if type(err) ~= 'table' then
    err = {err='ERR ' .. tostring(err)}  end  if i then
    err['source'] = i.source
    err['line'] = i.currentline
  end  return err
end
`
