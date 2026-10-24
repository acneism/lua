package lua

import (
	"errors"
	"strings"
	"testing"
)

func TestHookNumericBackedge(t *testing.T) {
	cause := errors.New("numeric loop interrupted")
	s := NewState(Options{HookEvery: 1000, Hook: func() error { return cause }})
	_, err := s.Call(s.Load(compileTest(t, "local n=0 for i=1,100000 do n=n+1 end return n"), nil))
	if !errors.Is(err, cause) {
		t.Fatalf("numeric backedge did not poll: %v", err)
	}
}

func TestHookStraightLine(t *testing.T) {
	cause := errors.New("straight code interrupted")
	calls := 0
	s := NewState(Options{HookEvery: 64, Hook: func() error { calls++; return cause }})
	_, err := s.Call(s.Load(compileTest(t, "local n=0 return n"+strings.Repeat("+1", 1000)), nil))
	if !errors.Is(err, cause) || calls != 1 {
		t.Fatalf("straight code did not poll: %v, callbacks %d", err, calls)
	}
}

func TestHookReentryGrowsRegisterStack(t *testing.T) {
	var state *State
	var nested Value
	entered, reentered := false, false
	state = NewState(Options{HookEvery: 128, Hook: func() error {
		if entered || reentered {
			return nil
		}
		entered = true
		_, err := state.Call(nested)
		reentered = true
		entered = false
		return err
	}})
	nested = state.Load(compileTest(t, "local function f(n) if n==0 then return 0 end return f(n-1)+n end return f(100)"), nil)
	fn := state.Load(compileTest(t, "local n=0 for i=1,10000 do n=n+i end return n"), nil)
	values, err := state.Call(fn)
	if err != nil || !reentered || len(values) != 1 || values[0].Float() != 10000*10001/2 {
		t.Fatalf("reentrant hook lost running registers: %v %v", values, err)
	}
}

func TestNumericLoopDoesNotAllocatePerIteration(t *testing.T) {
	s := NewState(Options{})
	measure := func(source string) float64 {
		fn := s.Load(compileTest(t, source), nil)
		var err error
		allocations := testing.AllocsPerRun(5, func() { _, err = s.Call(fn) })
		if err != nil {
			t.Fatal(err)
		}
		return allocations
	}
	small := measure("local n=0 for i=1,10 do n=n+i end return n")
	large := measure("local n=0 for i=1,10000 do n=n+i end return n")
	if large > small {
		t.Fatalf("allocations grew with loop iterations: %g -> %g", small, large)
	}
}
