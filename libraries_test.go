package lua

import (
	"errors"
	"strings"
	"testing"
)

func TestPatternFrontier(t *testing.T) {
	checkReference(t, "return string.find('a(abc) aaa aac aaabcc', '%f[%a]%w+')", Options{})
}

func TestGSubHook(t *testing.T) {
	cause := errors.New("pattern interrupted")
	s := NewState(Options{HookEvery: 1000, Hook: func() error { return cause }})
	fn := s.globals.RawGet(String("string")).Table().RawGet(String("gsub"))
	_, err := s.Call(fn, String(strings.Repeat("a", 100000)), String("a"), String("x"))
	if !errors.Is(err, cause) {
		t.Fatalf("gsub did not propagate hook abort: %v", err)
	}
	s.options.Hook = nil
	values, err := s.Call(fn, String("abc"), String("."), String("x"))
	if err != nil || len(values) != 2 || values[0].Text() != "xxx" {
		t.Fatalf("state cannot be reused after gsub abort: %v %v", values, err)
	}
}

func TestLibraryReadOnly(t *testing.T) {
	for _, source := range []string{
		"table.insert(table,1)",
		"table.remove(table,1)",
		"table.sort(table)",
	} {
		s := NewState(Options{})
		table := s.globals.RawGet(String("table")).Table()
		table.set(Number(1), Number(2))
		table.set(Number(2), Number(1))
		table.readonly = true
		_, err := s.Call(s.Load(compileTest(t, source), nil))
		if err == nil || !strings.Contains(err.Error(), "readonly") {
			t.Fatalf("library write escaped readonly protection: %s: %v", source, err)
		}
	}
}

func TestRandomStatesAreIndependent(t *testing.T) {
	left, right := NewState(Options{}), NewState(Options{})
	proto := compileTest(t, "return math.random()")
	for range 5 {
		a, err := left.Call(left.Load(proto, nil))
		if err != nil {
			t.Fatal(err)
		}
		b, err := right.Call(right.Load(proto, nil))
		if err != nil {
			t.Fatal(err)
		}
		if !a[0].Equal(b[0]) {
			t.Fatal("random state leaked between states")
		}
	}
}

func TestLuaMetamethodCallsDoNotAllocatePerCall(t *testing.T) {
	s := NewState(Options{})
	measure := func(count string) float64 {
		proto := compileTest(t, "local t=setmetatable({}, {__add=function(a,b) return b+1 end}) local n=0 for i=1,"+count+" do n=t+n end return n")
		fn := s.Load(proto, nil)
		var err error
		allocations := testing.AllocsPerRun(3, func() { _, err = s.Call(fn) })
		if err != nil {
			t.Fatal(err)
		}
		return allocations
	}
	if small, large := measure("10"), measure("1000"); large > small {
		t.Fatalf("Lua metamethod allocations grow with calls: %g -> %g", small, large)
	}
}
