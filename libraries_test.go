package lua

import (
	"context"
	"errors"
	"math"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestPatternRecursiveHook(t *testing.T) {
	if os.Getenv("LUA_PATTERN_HOOK_CHILD") != "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPatternRecursiveHook$")
		cmd.Env = append(os.Environ(), "LUA_PATTERN_HOOK_CHILD=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("recursive hook subprocess: %v\n%s", err, output)
		}
		return
	}
	cause := errors.New("recursive pattern interrupted")
	s := NewState(Options{HookEvery: 1000, Hook: func() error { return cause }})
	fn := s.globals.RawGet(String("string")).Table().RawGet(String("find"))
	_, err := s.Call(fn, String(strings.Repeat("a", 40)), String(strings.Repeat("a?", 40)+strings.Repeat("a", 40)))
	if !errors.Is(err, cause) {
		t.Fatalf("recursive match lost hook abort: %v", err)
	}
	s.options.Hook = nil
	values, err := s.Call(fn, String("abc"), String("a?bc"))
	if err != nil || len(values) != 2 || values[1].number != 3 {
		t.Fatalf("state after match abort: %v %v", values, err)
	}
}

func TestPatternDepthLimit(t *testing.T) {
	if os.Getenv("LUA_PATTERN_DEPTH_CHILD") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestPatternDepthLimit$")
		cmd.Env = append(os.Environ(), "LUA_PATTERN_DEPTH_CHILD=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("pattern subprocess: %v\n%s", err, output)
		}
		return
	}
	s := NewState(Options{})
	fn := s.globals.RawGet(String("string")).Table().RawGet(String("find"))
	_, err := s.Call(fn, String(strings.Repeat("a", 5000000)), String(strings.Repeat("a?", 5000000)))
	if err == nil || !strings.Contains(err.Error(), "pattern too complex") {
		t.Fatalf("missing depth limit: %v", err)
	}
}

func TestStringRepHook(t *testing.T) {
	cause := errors.New("rep interrupted")
	calls := 0
	s := NewState(Options{Hook: func() error {
		calls++
		if calls == 3 {
			return cause
		}
		return nil
	}})
	fn := s.globals.RawGet(String("string")).Table().RawGet(String("rep"))
	_, err := s.Call(fn, String("abc"), Number(10000000))
	if !errors.Is(err, cause) || calls != 3 {
		t.Fatalf("rep did not poll hook by chunks: %v, %d", err, calls)
	}
	s.options.Hook = nil
	values, err := s.Call(fn, String("abc"), Number(10001))
	if err != nil || values[0].Text() != strings.Repeat("abc", 10001) {
		t.Fatalf("rep after abort: %v", err)
	}
}

func TestMathOptions(t *testing.T) {
	custom := &Math{}
	fields := []*func(float64) float64{&custom.Sin, &custom.Cos, &custom.Tan, &custom.Asin, &custom.Acos, &custom.Atan}
	names := []string{"sin", "cos", "tan", "asin", "acos", "atan"}
	for index, field := range fields {
		*field = func(x float64) float64 { return float64(index) + x }
	}
	custom.Atan2 = func(y, x float64) float64 { return y*10 + x }
	s := NewState(Options{Math: custom})
	table := s.globals.RawGet(String("math")).Table()
	for index, name := range names {
		values, err := s.Call(table.RawGet(String(name)), Number(0.25))
		if err != nil || values[0].number != float64(index)+0.25 {
			t.Fatalf("%s override: %v %v", name, values, err)
		}
	}
	values, err := s.Call(table.RawGet(String("atan2")), Number(2), Number(3))
	if err != nil || values[0].number != 23 {
		t.Fatalf("atan2 override: %v %v", values, err)
	}
	fallback := NewState(Options{Math: &Math{Sin: custom.Sin}})
	values, err = fallback.Call(fallback.globals.RawGet(String("math")).Table().RawGet(String("cos")), Number(0))
	if err != nil || values[0].number != 1 {
		t.Fatalf("partial override fallback: %v %v", values, err)
	}
	near := math.Nextafter(1, 0)
	if got, want := defaultAcos(near), 2*math.Asin(math.Sqrt((1-near)*0.5)); got != want {
		t.Fatalf("acos near one: %g %g", got, want)
	}
}

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
