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
