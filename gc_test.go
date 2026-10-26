package lua

import "testing"

func collectTest(t *testing.T, s *State) {
	t.Helper()
	if _, err := s.Call(s.Globals().RawGet(String("collectgarbage"))); err != nil {
		t.Fatal(err)
	}
}

func weakTestTable(t *testing.T, s *State, mode string) *Table {
	t.Helper()
	w := s.NewTable(0, 4)
	meta := s.NewTable(0, 1)
	_ = meta.RawSet(String("__mode"), String(mode))
	_ = w.SetMetatable(meta)
	_ = s.Globals().RawSet(String("weak"), w.Value())
	return w
}

func TestWeakHostRoots(t *testing.T) {
	s := NewState(Options{})
	w := weakTestTable(t, s, "k")
	values, err := s.Call(s.Load(compileTest(t, "return coroutine.create(function() end)"), nil))
	if err != nil {
		t.Fatal(err)
	}
	keys := []Value{s.NewTable(0, 0).Value(), s.Function("host", func(*State, []Value) ([]Value, error) { return nil, nil }), values[0]}
	var releases []func()
	for _, key := range keys {
		_ = w.RawSet(key, Number(1))
		releases = append(releases, s.Retain(key))
	}
	second := s.Retain(keys[0])
	collectTest(t, s)
	for _, key := range keys {
		if w.RawGet(key).IsNil() {
			t.Fatal("registered host value was collected", key.Type())
		}
	}
	for _, release := range releases {
		release()
		release()
	}
	collectTest(t, s)
	if w.RawGet(keys[0]).IsNil() || !w.RawGet(keys[1]).IsNil() || !w.RawGet(keys[2]).IsNil() {
		t.Fatal("independent registrations or releases changed")
	}
	second()
	collectTest(t, s)
	if !w.RawGet(keys[0]).IsNil() || len(s.hostRoots) != 0 {
		t.Fatal("released host value remains a root")
	}
}

func TestWeakNativeArguments(t *testing.T) {
	s := NewState(Options{})
	w := weakTestTable(t, s, "v")
	value := s.NewTable(0, 0).Value()
	_ = w.RawSet(Number(1), value)
	fn := s.Function("host", func(s *State, args []Value) ([]Value, error) {
		collectTest(t, s)
		if !w.RawGet(Number(1)).Equal(args[0]) {
			t.Fatal("active native argument was collected")
		}
		return nil, nil
	})
	if _, err := s.Call(fn, value); err != nil {
		t.Fatal(err)
	}
	if w.RawGet(Number(1)).IsNil() {
		t.Fatal("collection happened without an explicit request")
	}
	collectTest(t, s)
	if !w.RawGet(Number(1)).IsNil() || len(s.main.nativeArgs) != 0 {
		t.Fatal("native argument retained after return")
	}
}

func TestWeakClosedUpvalueRoot(t *testing.T) {
	s := NewState(Options{})
	w := weakTestTable(t, s, "v")
	values, err := s.Call(s.Load(compileTest(t, "local k={} weak[1]=k return function() return k end"), nil))
	if err != nil {
		t.Fatal(err)
	}
	release := s.Retain(values[0])
	collectTest(t, s)
	if w.RawGet(Number(1)).IsNil() {
		t.Fatal("closed upvalue was not traced")
	}
	release()
	collectTest(t, s)
	if !w.RawGet(Number(1)).IsNil() {
		t.Fatal("unrooted closure kept its value")
	}
}

func TestWeakThreadAndFrameRoots(t *testing.T) {
	for _, source := range []string{
		"local c=coroutine.create(function() local k={} weak[1]=k coroutine.yield() return k end) coroutine.resume(c) collectgarbage() local kept=weak[1]~=nil coroutine.resume(c) collectgarbage() return kept,weak[1]==nil",
		"local c=coroutine.wrap(function() local k={} weak[1]=k coroutine.yield() return k end) c() collectgarbage() local kept=weak[1]~=nil c() collectgarbage() return kept,weak[1]==nil",
		"collectgarbage() return weak[1]~=nil,true",
	} {
		s := NewState(Options{})
		w := weakTestTable(t, s, "v")
		fn := s.Load(compileTest(t, source), nil)
		if source[0] != 'l' {
			_ = w.RawSet(Number(1), fn)
		}
		values, err := s.Call(fn)
		if err != nil || len(values) != 2 || !values[0].Truth() || !values[1].Truth() {
			t.Fatalf("roots changed: %s: %v %v", source, values, err)
		}
	}
}

func TestWeakMetatableAndOpaqueValues(t *testing.T) {
	s := NewState(Options{})
	w := weakTestTable(t, s, "kv")
	value := s.NewTable(0, 0).Value()
	_ = s.stringMeta.RawGet(String("__index")).Table().RawSet(String("held"), value)
	_ = w.RawSet(Number(1), value)
	_ = w.RawSet(Number(2), s.Userdata(value))
	_ = w.RawSet(Number(3), String("held"))
	collectTest(t, s)
	if w.RawGet(Number(1)).IsNil() || w.RawGet(Number(2)).IsNil() || w.RawGet(Number(3)).IsNil() {
		t.Fatal("metatable root or noncollectible value was removed")
	}
	_ = s.stringMeta.RawGet(String("__index")).Table().RawSet(String("held"), Nil())
	w.SetReadOnly(true)
	collectTest(t, s)
	if !w.RawGet(Number(1)).IsNil() || w.RawGet(Number(2)).IsNil() {
		t.Fatal("read-only weak table or opaque host value changed")
	}
}

func TestWeakMaskedHandlerRoot(t *testing.T) {
	s := NewState(Options{})
	w := weakTestTable(t, s, "v")
	handler := s.Load(compileTest(t, "return ..."), nil)
	_ = w.RawSet(Number(1), handler)
	fn := s.Load(compileTest(t, "pcall(function() collectgarbage() end) return weak[1]~=nil"), nil)
	values, err := s.CallWithHandler(fn, handler)
	if err != nil || len(values) != 1 || !values[0].Truth() {
		t.Fatal("masked outer handler was collected", values, err)
	}
	collectTest(t, s)
	if !w.RawGet(Number(1)).IsNil() || len(s.main.handlerRoots) != 0 {
		t.Fatal("finished handler remained rooted")
	}
}
