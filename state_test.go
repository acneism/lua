package lua

import (
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func compileTest(t *testing.T, source string) *Proto {
	t.Helper()
	proto, err := Compile([]byte(source), "@test")
	if err != nil {
		t.Fatal(err)
	}
	return proto
}

func TestAbortCannotBeCaught(t *testing.T) {
	cause := errors.New("stop requested")
	for _, source := range []string{
		"while true do end",
		"return pcall(function() while true do end end)",
		"return xpcall(function() while true do end end,function() return 'caught' end)",
		"local co=coroutine.create(function() while true do end end) return coroutine.resume(co)",
		"return load(function() return 'x' end)",
	} {
		t.Run(source, func(t *testing.T) {
			ticks := 0
			s := NewState(Options{HookEvery: 10, Hook: func() error {
				ticks++
				if ticks >= 5 {
					return cause
				}
				return nil
			}})
			_, err := s.Call(s.Load(compileTest(t, source), nil))
			var failure *Error
			if !errors.Is(err, cause) || !errors.As(err, &failure) || failure.Line == 0 {
				t.Fatalf("abort lost: %v", err)
			}
			if len(s.main.frames) != 0 || len(s.threads) != 0 {
				t.Fatal("aborted execution retained frames or coroutines")
			}
			s.options.Hook = nil
			values, err := s.Call(s.Load(compileTest(t, "return 7"), nil))
			if err != nil || len(values) != 1 || values[0].Float() != 7 {
				t.Fatalf("state cannot be reused: %v %v", values, err)
			}
		})
	}
}

func TestNativeFailuresUnwind(t *testing.T) {
	cause := errors.New("host abort")
	for _, coroutine := range []bool{false, true} {
		for _, broken := range []bool{false, true} {
			s := NewState(Options{})
			host := s.Function("host", func(_ *State, _ []Value) ([]Value, error) {
				if broken {
					panic("host bug")
				}
				return nil, cause
			})
			if err := s.Globals().RawSet(String("host"), host); err != nil {
				t.Fatal(err)
			}
			source := "return pcall(function() return host() end)"
			if coroutine {
				source = "local co=coroutine.create(function() coroutine.yield(1) return host() end) coroutine.resume(co) return pcall(coroutine.resume,co)"
			}
			_, err := s.Call(s.Load(compileTest(t, source), nil))
			var failure *Error
			if !errors.As(err, &failure) || failure.Abort == nil || failure.Internal != broken {
				t.Fatalf("lost failure: %v", err)
			}
			if !broken && !errors.Is(err, cause) {
				t.Fatal(err)
			}
			if len(s.main.frames) != 0 || len(s.threads) != 0 {
				t.Fatal("failure retained execution state")
			}
		}
	}
}

func TestProtoAcrossStates(t *testing.T) {
	proto := compileTest(t, "local function f(x) return x*2 end return f(...)")
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func(n int) {
			defer workers.Done()
			s := NewState(Options{})
			for repeat := 0; repeat < 20; repeat++ {
				values, err := s.Call(s.Load(proto, nil), Number(float64(n)))
				if err != nil || len(values) != 1 || values[0].Float() != float64(n*2) {
					t.Errorf("state %d: %v %v", n, values, err)
					return
				}
			}
		}(i)
	}
	workers.Wait()
}

func TestCoroutinesDoNotLeak(t *testing.T) {
	runtime.GC()
	before := runtime.NumGoroutine()
	s := NewState(Options{})
	proto := compileTest(t, "local co=coroutine.create(function() local x=1 coroutine.yield(x) return x end) coroutine.resume(co) return co")
	for repeat := 0; repeat < 100; repeat++ {
		values, err := s.Call(s.Load(proto, nil))
		if err != nil || len(values) != 1 {
			t.Fatalf("%v %v", values, err)
		}
		thread := values[0].object.thread
		if thread.status != "dead" || len(thread.frames) != 0 || thread.open != nil {
			t.Fatal("suspended coroutine survived the call boundary")
		}
		if !thread.entry.IsNil() {
			t.Fatal("closed coroutine retained its entry function")
		}
	}
	runtime.GC()
	if after := runtime.NumGoroutine(); after > before+1 {
		t.Fatalf("goroutines grew from %d to %d", before, after)
	}
}

func TestTailCallsReuseFrames(t *testing.T) {
	s := NewState(Options{HookEvery: 100})
	maxDepth := 0
	s.options.Hook = func() error { maxDepth = max(maxDepth, len(s.current.frames)); return nil }
	proto := compileTest(t, "local function f(n) if n==0 then return 'done' end return f(n-1) end return f(1000000)")
	values, err := s.Call(s.Load(proto, nil))
	if err != nil || len(values) != 1 || values[0].Text() != "done" {
		t.Fatalf("%v %v", values, err)
	}
	if maxDepth > 2 {
		t.Fatalf("tail recursion used %d frames", maxDepth)
	}
	for _, value := range s.main.tail[:cap(s.main.tail)] {
		if !value.IsNil() {
			t.Fatal("tail buffer retained a value")
		}
	}
	for _, frame := range s.main.frames[:cap(s.main.frames)] {
		if frame.function != nil {
			t.Fatal("frame cache retained a closure")
		}
	}
}

func TestStringLimitAndMemoryAbort(t *testing.T) {
	s := NewState(Options{MaxString: 5})
	_, err := s.Call(s.Load(compileTest(t, "return 'abcdef'"), nil))
	if err == nil || !strings.Contains(err.Error(), "string length overflow") {
		t.Fatalf("literal limit failed: %v", err)
	}
	_, err = s.Call(s.Load(compileTest(t, "return 'abc'..'def'"), nil))
	if err == nil || !strings.Contains(err.Error(), "string length overflow") {
		t.Fatalf("got %v", err)
	}
	budget := NewBudget(4096)
	s = NewState(Options{Memory: budget})
	_, err = s.Call(s.Load(compileTest(t, "return pcall(function() local t={} while true do t[#t+1]={} end end)"), nil))
	if !abortError(err) || !strings.Contains(err.Error(), "memory limit") || budget.Used() == 0 {
		t.Fatalf("memory limit failed: %v", err)
	}
}

func TestNativeReentryAndUserdata(t *testing.T) {
	s := NewState(Options{})
	user := s.Userdata("host object")
	if user.Type() != TypeUserdata || user.Userdata() != "host object" || Nil().Userdata() != nil {
		t.Fatal("userdata API failed")
	}
	callback := s.Load(compileTest(t, "return ..."), nil)
	native := s.Function("callback", func(s *State, args []Value) ([]Value, error) { return s.Call(callback, args...) })
	values, err := s.Call(native, user, Number(7), String("text"))
	if err != nil || len(values) != 3 || !values[0].Equal(user) || values[1].Float() != 7 || values[2].Text() != "text" {
		t.Fatalf("%v %v", values, err)
	}
}

func TestNativeResultsSurviveCleanup(t *testing.T) {
	s := NewState(Options{})
	fn := s.Function("echo", func(_ *State, args []Value) ([]Value, error) { return args, nil })
	values, err := s.Call(fn, String("first"), Number(7))
	if err != nil || len(values) != 2 || values[0].Text() != "first" || values[1].Float() != 7 {
		t.Fatal("native results were cleared", values, err)
	}
	if _, err := s.Call(fn, String("second")); err != nil {
		t.Fatal(err)
	}
	if values[0].Text() != "first" || values[1].Float() != 7 {
		t.Fatal("native results changed after state reuse", values)
	}
}
