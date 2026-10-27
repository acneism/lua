package lua

import (
	"errors"
	"testing"
)

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

func TestStepZeroCollects(t *testing.T) {
	s := NewState(Options{})
	w := weakTestTable(t, s, "v")
	_ = w.RawSet(Number(1), s.NewTable(0, 0).Value())
	values, err := s.Call(s.Globals().RawGet(String("collectgarbage")), String("step"), Number(0))
	if err != nil || len(values) != 1 || values[0].Type() != TypeBoolean || !values[0].Truth() || !w.RawGet(Number(1)).IsNil() {
		t.Fatal("step with zero did not follow the full-collection rule", values, err)
	}
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
	if !w.RawGet(Number(1)).IsNil() || len(s.main.nativeRoots) != 0 {
		t.Fatal("native argument retained after return")
	}
}

func TestWeakNativeArgumentsAfterGrowth(t *testing.T) {
	for _, route := range []string{"host", "Lua", "callable"} {
		t.Run(route, func(t *testing.T) {
			s := NewState(Options{})
			w := weakTestTable(t, s, "v")
			original, replacement := s.NewTable(0, 0).Value(), s.NewTable(0, 0).Value()
			_ = w.RawSet(Number(1), original)
			_ = w.RawSet(Number(2), replacement)
			grow := s.Load(compileTest(t, "local function f(n) if n==0 then return 0 end return n+f(n-1) end return f(100)"), nil)
			fn := s.Function("host", func(s *State, args []Value) ([]Value, error) {
				index := 0
				if route == "callable" {
					index = 1
				}
				stack := &s.current.stack[0]
				if _, err := s.Call(grow); err != nil {
					return nil, err
				}
				if &s.current.stack[0] == stack {
					t.Fatal("native arguments did not cross stack growth")
				}
				if !args[index].Equal(original) {
					t.Fatal("native argument changed during reentry")
				}
				args[index] = replacement
				collectTest(t, s)
				if route == "host" && !w.RawGet(Number(1)).IsNil() || !w.RawGet(Number(2)).Equal(replacement) {
					t.Fatal("collector traced the relocated copy instead of active arguments")
				}
				return args[index:], nil
			})
			var values []Value
			var err error
			if route == "host" {
				values, err = s.Call(fn, original)
			} else {
				if route == "callable" {
					object, meta := s.NewTable(0, 0), s.NewTable(0, 1)
					_ = meta.RawSet(String("__call"), fn)
					_ = object.SetMetatable(meta)
					fn = object.Value()
				}
				_ = s.Globals().RawSet(String("host"), fn)
				values, err = s.Call(s.Load(compileTest(t, "return host(...)"), nil), original)
			}
			if err != nil || len(values) != 1 || !values[0].Equal(replacement) {
				t.Fatal("native return aliased a cleared stack", values, err)
			}
			collectTest(t, s)
			if !w.RawGet(Number(2)).IsNil() || len(s.main.nativeRoots) != 0 {
				t.Fatal("finished native arguments stayed rooted")
			}
		})
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

func TestWeakPendingHookArguments(t *testing.T) {
	proto := compileTest(t, "local function use(v) return weak[1]==v end\nlocal function make() local v={} weak[1]=v return v end\nreturn use(make())")
	for period := 1; period <= 16; period++ {
		var s *State
		inside := false
		s = NewState(Options{HookEvery: period, Hook: func() error {
			if inside {
				return nil
			}
			inside = true
			defer func() { inside = false }()
			_, err := s.Call(s.Globals().RawGet(String("collectgarbage")))
			return err
		}})
		weakTestTable(t, s, "v")
		values, err := s.Call(s.Load(proto, nil))
		if err != nil || len(values) != 1 || !values[0].Truth() {
			t.Fatalf("period %d: %v %v", period, values, err)
		}
		if len(s.main.hookRoots) != 0 {
			t.Fatal("hook roots retained after return")
		}
	}
}

func TestWeakPreparedHookCallee(t *testing.T) {
	var s *State
	inside, mutated := false, false
	s = NewState(Options{HookEvery: 8, Hook: func() error {
		frame, ok := s.Frame(0)
		setter := s.Globals().RawGet(String("setter"))
		if inside || mutated || !ok || frame.What != "main" || frame.Line != 4 || setter.Type() != TypeFunction {
			return nil
		}
		inside, mutated = true, true
		defer func() { inside = false }()
		if _, err := s.Call(setter); err != nil {
			return err
		}
		_, err := s.Call(s.Globals().RawGet(String("collectgarbage")))
		return err
	}})
	w := weakTestTable(t, s, "v")
	warm := s.Load(compileTest(t, "local function f(n) if n==0 then return 0 end return 1+f(n-1) end return f(10)"), nil)
	if _, err := s.Call(warm); err != nil {
		t.Fatal(err)
	}
	fn := s.Load(compileTest(t, "local target=function(v) return weak[1]~=nil end\nweak[1]=target\nsetter=function() target=nil end do local a,b,c=1,2,3 end\nlocal result=target(1)\nreturn result"), nil)
	values, err := s.Call(fn)
	if err != nil || len(values) != 1 || !values[0].Truth() || !mutated {
		t.Fatal("prepared callee was lost across hook", values, err, mutated)
	}
	collectTest(t, s)
	if !w.RawGet(Number(1)).IsNil() {
		t.Fatal("finished prepared call retained callee")
	}
}

func TestWeakMetamethodHookRoots(t *testing.T) {
	for _, operation := range []string{"+", ".."} {
		observed := false
		for period := 1; period <= 32; period++ {
			var s *State
			inside, mutated := false, false
			s = NewState(Options{HookEvery: period, Hook: func() error {
				frame, ok := s.Frame(0)
				setter := s.Globals().RawGet(String("setter"))
				if inside || mutated || !ok || frame.What != "main" || frame.Line != 7 || setter.Type() != TypeFunction {
					return nil
				}
				inside, mutated = true, true
				defer func() { inside = false }()
				if _, err := s.Call(setter); err != nil {
					return err
				}
				_, err := s.Call(s.Globals().RawGet(String("collectgarbage")))
				return err
			}})
			w := weakTestTable(t, s, "v")
			fn := s.Load(compileTest(t, "local target=function(a,b) return weak[1]~=nil end\nweak[1]=target\nlocal mt={__add=target,__concat=target}\nlocal v=setmetatable({},mt)\ntarget=nil\nsetter=function() mt.__add=nil mt.__concat=nil end\nreturn v"+operation+"1"), nil)
			values, err := s.Call(fn)
			if err != nil || len(values) != 1 || !values[0].Truth() {
				t.Fatalf("%s, period %d: %v %v", operation, period, values, err)
			}
			observed = observed || mutated
			collectTest(t, s)
			if mutated && !w.RawGet(Number(1)).IsNil() || len(s.main.hookRoots) != 0 {
				t.Fatalf("%s, period %d: finished metamethod retained hook roots", operation, period)
			}
		}
		if !observed {
			t.Fatalf("%s did not exercise the metamethod hook", operation)
		}
	}
}

func TestWeakHookAbortReleasesRoots(t *testing.T) {
	stop := errors.New("stop")
	s := NewState(Options{HookEvery: 1, Hook: func() error { return stop }})
	w := weakTestTable(t, s, "v")
	fn := s.Load(compileTest(t, "return ..."), nil)
	value := s.NewTable(0, 0).Value()
	_ = w.RawSet(Number(1), value)
	if _, err := s.Call(fn, value); !errors.Is(err, stop) || len(s.main.hookRoots) != 0 {
		t.Fatal("aborted hook retained roots", err)
	}
	s.options.Hook = nil
	collectTest(t, s)
	if !w.RawGet(Number(1)).IsNil() {
		t.Fatal("aborted call retained its argument")
	}
}
