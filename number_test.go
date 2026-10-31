package lua

import (
	"math"
	"testing"
)

func TestNaNFormattingPreservesSign(t *testing.T) {
	for _, test := range []struct {
		number float64
		want   string
	}{
		{math.Copysign(math.NaN(), 1), "nan"},
		{math.Copysign(math.NaN(), -1), "-nan"},
	} {
		if got := Number(test.number).String(); got != test.want {
			t.Fatalf("got %q, want %q", got, test.want)
		}
	}
}

func TestLuaModuloRounding(t *testing.T) {
	checkReference(t, "return 2^53 % 3.5", Options{})
	checkReference(t, "return 1e100 % 3.5", Options{})
}

func TestDivisionNaNSign(t *testing.T) {
	for _, source := range []string{
		"return string.format('[%f][%5.1f][%-8f][%+f][%08f][%08f][%e][%G]', 1/0, 0/0, -1/0, 1/0, 1/0, -(0/0), -1/0, 0/0)",
		"return tostring(math.fmod(1, 0)), tostring(math.fmod(0, 0)), tostring(math.fmod(1/0, 1)), tostring(math.fmod(0/0, 1))",
	} {
		checkReference(t, source, Options{})
	}
}

func TestNaNNormalizationIsNarrow(t *testing.T) {
	for _, test := range []struct {
		got, want string
		equal     bool
	}{
		{"inf,-nan", "inf,nan", true},
		{"nan,1", "-nan,1", true},
		{"nan,1", "-nan,2", false},
		{"error -nan", "error nan", false},
		{"nan", "nan,1", false},
		{"0", "nan", false},
	} {
		if got := equalNaNFields(test.got, test.want); got != test.equal {
			t.Fatalf("normalization %q vs %q: %v", test.got, test.want, got)
		}
	}
}

func TestLuaFloorMatchesMath(t *testing.T) {
	values := []float64{0, math.Copysign(0, -1), 0.5, -0.5, 1, -1, 2.5, -2.5, 1e15 + 0.5, -1e15 - 0.5, 0x1p52 - 0.5, -0x1p52 + 0.5, 0x1p52, -0x1p52, 0x1p53 + 2, 1e300, -1e300, math.Inf(1), math.Inf(-1), math.NaN(), 4.9e-324, -4.9e-324}
	for _, value := range values {
		got, want := luaFloor(value), math.Floor(value)
		if math.Float64bits(got) != math.Float64bits(want) && !(math.IsNaN(got) && math.IsNaN(want)) {
			t.Fatalf("floor(%v) = %v, want %v", value, got, want)
		}
	}
}
