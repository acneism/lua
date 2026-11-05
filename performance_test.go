package lua

import (
	"errors"
	"fmt"
	"strconv"
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

func TestHookConditionalPairs(t *testing.T) {
	cause := errors.New("conditional chain interrupted")
	calls := 0
	s := NewState(Options{HookEvery: 100, Hook: func() error { calls++; return cause }})
	source := "local x=true " + strings.Repeat("if x then end ", 10000) + "return 1"
	_, err := s.Call(s.Load(compileTest(t, source), nil))
	if !errors.Is(err, cause) || calls != 1 {
		t.Fatalf("conditional chain did not poll: %v, callbacks %d", err, calls)
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

func pageOffset(t *testing.T, pointer any) uint64 {
	t.Helper()
	address, err := strconv.ParseUint(fmt.Sprintf("%p", pointer), 0, 64)
	if err != nil {
		t.Fatal(err)
	}
	return address % 4096
}

func TestRegistersAndCodeUseSeparatePageHalves(t *testing.T) {
	proto := compileTest(t, strings.Repeat("do local function f(x) return x+1 end end ", 200))
	for index, child := range proto.children {
		start := pageOffset(t, &child.code[0])
		if start < 2048 || start+8*uint64(len(child.code)) > 4096 {
			t.Fatalf("function %d has %d words of code from page offset %d", index, len(child.code), start)
		}
	}
	s := NewState(Options{})
	if _, err := s.Call(s.Load(proto, nil)); err != nil {
		t.Fatal(err)
	}
	for range 6 {
		if offset := pageOffset(t, &s.main.stack[0]); offset > 8 {
			t.Fatalf("stack of %d values starts at page offset %d", len(s.main.stack), offset)
		}
		s.main.ensure(len(s.main.stack) + 1)
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
