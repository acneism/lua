package lua

import (
	"os"
	"strings"
	"testing"
)

func TestLongBinaryChains(t *testing.T) {
	count := 200000
	if os.Getenv("LUA_STRESS") == "1" {
		count = 3000000
	}
	for _, item := range []struct {
		op, initial string
		want        Value
	}{
		{"+", "1", Number(float64(count + 1))},
		{"*", "1", Number(1)},
		{"==", "1", Bool(false)},
		{"and", "true", Bool(true)},
		{"or", "false", Bool(false)},
	} {
		t.Run(item.op, func(t *testing.T) {
			source := "local x=" + item.initial + " return x" + strings.Repeat(" "+item.op+" x", count)
			p, err := Compile([]byte(source), "@chain")
			if err != nil {
				t.Fatal(err)
			}
			if p.maxRegisters > 5 {
				t.Fatalf("chain retained %d registers", p.maxRegisters)
			}
			s := NewState(Options{})
			values, err := s.Call(s.Load(p, nil))
			if err != nil || len(values) != 1 || !values[0].Equal(item.want) {
				t.Fatalf("chain result: %v %v", values, err)
			}
		})
	}
}

func TestLongSuffixChains(t *testing.T) {
	for _, item := range []struct{ prefix, link, suffix string }{
		{"local t={} t.a=t local r=t", ".a", " return r==t"},
		{"local t={} t.a=t local r=t", "['a']", " return r==t"},
		{"local function f() return f end local r=f", "()", " return r==f"},
		{"local o={} function o:m() return self end local r=o", ":m()", " return r==o"},
	} {
		t.Run(item.link, func(t *testing.T) {
			p, err := Compile([]byte(item.prefix+strings.Repeat(item.link, 100000)+item.suffix), "@suffix")
			if err != nil {
				t.Fatal(err)
			}
			if p.maxRegisters > 12 {
				t.Fatalf("suffix retained %d registers", p.maxRegisters)
			}
			s := NewState(Options{})
			values, err := s.Call(s.Load(p, nil))
			if err != nil || len(values) != 1 || !values[0].Equal(Bool(true)) {
				t.Fatalf("suffix result: %v %v", values, err)
			}
		})
	}
}
