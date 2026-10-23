package lua

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func checkReference(t *testing.T, source string, options Options) {
	t.Helper()
	want, found := "", false
	for _, snippet := range readCorpus(t) {
		if snippet.Src == source {
			want, found = snippet.Want, true
			break
		}
	}
	if !found {
		t.Fatalf("source has no captured Redis answer: %s", source)
	}
	s := NewState(options)
	env := captureEnvironment(s)
	proto, err := Compile([]byte(fmt.Sprintf(captureWrapper, quoteLua(source))), "@capture")
	if err != nil {
		t.Fatal(err)
	}
	values, err := s.Call(s.Load(proto, env))
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || strings.TrimPrefix(values[0].Text(), "ERR ") != want {
		t.Fatalf("want %q, got %v", want, values)
	}
}

func TestLocalDeclarationScope(t *testing.T) {
	for _, source := range []string{
		"local x = 1 local x = x + 1 return x",
		"local function f() return 1 end local f = function() return f() + 1 end return f()",
		"local a, b = 1, a return b",
	} {
		checkReference(t, source, Options{})
	}
}

func TestErrorNames(t *testing.T) {
	for _, source := range []string{
		"local x x()",
		"local u local function f() return u.x end return f()",
		"local t = {} return t.foo.bar",
		"local t = {} return t:nomethod()",
	} {
		checkReference(t, source, Options{})
	}
}

func TestConstructorListPrecedence(t *testing.T) {
	for _, source := range []string{
		"local t={[1]=1,[2]=2,3} return t[1],t[2]",
		"local t={1,2,[2]=\"x\"} return t[1],t[2]",
	} {
		checkReference(t, source, Options{})
	}
}

func TestCallDepthLimit(t *testing.T) {
	ticks := 0
	checkReference(t, "local function f(n) return f(n+1) + 1 end return f(1)", Options{HookEvery: 1000, Hook: func() error {
		ticks++
		if ticks > 500 {
			return errors.New("depth limit did not stop recursion")
		}
		return nil
	}})
}
