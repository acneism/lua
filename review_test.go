package lua

import (
	"strings"
	"testing"
)

func TestBinaryChainsReuseRegisters(t *testing.T) {
	for _, operation := range []string{" + x", " * 1", " == x", " + (x*x)"} {
		proto := compileTest(t, "local x=1 return x"+strings.Repeat(operation, 3000))
		if proto.maxRegisters > 10 {
			t.Fatalf("%s retained temporary operands: %d registers", operation, proto.maxRegisters)
		}
	}
}

func TestCDepthUnwinds(t *testing.T) {
	s := NewState(Options{})
	for _, source := range []string{
		"local function f() return pcall(f) end return f()",
		"local function f(n) if n==0 then return 1 end return coroutine.wrap(f)(n-1) end return pcall(f,300)",
		"local t=setmetatable({}, {__index=function(t,k) return t[k] end}) return pcall(function() return t.x end)",
		"local function f() return xpcall(f,function(e) return e end) end return f()",
	} {
		if _, err := s.Call(s.Load(compileTest(t, source), nil)); err != nil {
			t.Fatal(err)
		}
		if s.main.cDepth != 0 || s.main.nativeDepth != 0 || len(s.main.frames) != 0 || len(s.threads) != 0 {
			t.Fatal("C call failure retained execution state")
		}
		values, err := s.Call(s.Load(compileTest(t, "return 7"), nil))
		if err != nil || len(values) != 1 || values[0].Float() != 7 {
			t.Fatalf("state cannot be reused: %v %v", values, err)
		}
	}
}
