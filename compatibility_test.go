package lua

import (
	"math"
	"strings"
	"testing"
)

func TestArithmeticNaNOperands(t *testing.T) {
	proto := compileTest(t, "local a,b=... return a+b,a-b,a*b,a/b")
	s := NewState(Options{})
	for _, pair := range [][2]float64{
		{math.Float64frombits(0x7ff8000000000011), math.Float64frombits(0xfff8000000000022)},
		{math.Float64frombits(0xfff8000000000011), math.Float64frombits(0x7ff8000000000022)},
		{math.Float64frombits(0x7ff0000000000011), 2},
		{2, math.Float64frombits(0xfff0000000000022)},
	} {
		left, right := pair[0], pair[1]
		want := math.Float64bits(left) | 1<<51
		if !math.IsNaN(left) {
			want = math.Float64bits(right) | 1<<51
		}
		values, err := s.Call(s.Load(proto, nil), Number(left), Number(right))
		if err != nil || len(values) != 4 {
			t.Fatalf("arithmetic call: %v %v", values, err)
		}
		for index, operator := range []string{"+", "-", "*", "/"} {
			generic := s.binary(operator, Number(left), Number(right), nameInfo{}, nameInfo{})
			if got := math.Float64bits(values[index].Float()); got != want {
				t.Fatalf("VM %x %s %x: got %x want %x", math.Float64bits(left), operator, math.Float64bits(right), got, want)
			}
			if got := math.Float64bits(generic.Float()); got != want {
				t.Fatalf("generic %s: got %x want %x", operator, got, want)
			}
		}
	}
}

func TestInheritedSyntaxDepth(t *testing.T) {
	ran := 0
	for _, item := range readCorpus(t) {
		if strings.HasPrefix(item.Src, "local function try(n) local f, e = loadstring(") {
			if strings.Contains(item.Src, "for n = 193, 199") || strings.Contains(item.Src, "for n = 190, 197") || strings.Contains(item.Src, "for n = 188, 196") || strings.Contains(item.Src, "for n = 194, 199") {
				checkReference(t, item.Src, Options{})
				ran++
			}
		}
	}
	if ran != 4 {
		t.Fatalf("expected four captured syntax-depth probes, executed %d", ran)
	}
}

func TestReferenceNaNArithmetic(t *testing.T) {
	ran := 0
	for _, item := range readCorpus(t) {
		if strings.Contains(item.Src, "local vals = {P, N, 1, -1, 0, I, -I}") || strings.Contains(item.Src, "pair('fmod', math.fmod)") || strings.HasPrefix(item.Src, "local I = 1/0 return tostring(math.log10(-1))") {
			checkReference(t, item.Src, Options{})
			ran++
		}
	}
	if ran != 3 {
		t.Fatalf("expected three captured NaN probes, executed %d", ran)
	}
}
