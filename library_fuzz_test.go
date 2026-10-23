package lua

import (
	"errors"
	"strings"
	"testing"
)

func fuzzLibraryState() *State {
	ticks := 0
	return NewState(Options{MaxString: 8192, HookEvery: 10, Hook: func() error {
		ticks++
		if ticks > 500 {
			return errors.New("fuzz step budget")
		}
		return nil
	}})
}

func fuzzLibraryResult(t *testing.T, values []Value, err error) string {
	t.Helper()
	var failure *Error
	if errors.As(err, &failure) && failure.Internal {
		t.Fatalf("library raised internal error: %v", err)
	}
	if err != nil {
		return "error:" + err.Error()
	}
	var out strings.Builder
	for _, value := range values {
		out.WriteString(value.kind.String())
		out.WriteByte(':')
		out.WriteString(value.String())
		out.WriteByte(0)
	}
	return out.String()
}

func FuzzPattern(f *testing.F) {
	for _, seed := range []struct {
		source, pattern, replacement string
		mode                         byte
	}{
		{"abc", "%f[%a]%w+", "x", 0}, {"(a(b)c)", "%b()", "%0", 1},
		{"aaa", "a-", "x", 3}, {"abc", "()", "%1", 3},
		{"aa", "(a)%1", "%1", 2}, {"\x00a", "[%z]", "%", 3},
		{"abc", "[", "", 0}, {"aaa", "a*a*a*a*b", "x", 1},
	} {
		f.Add(seed.source, seed.pattern, seed.replacement, seed.mode)
	}
	f.Fuzz(func(t *testing.T, source, pattern, replacement string, mode byte) {
		if len(source) > 256 || len(pattern) > 128 || len(replacement) > 64 {
			t.Skip()
		}
		run := func() string {
			s := fuzzLibraryState()
			table := s.globals.RawGet(String("string")).Table()
			name := "find"
			switch mode % 4 {
			case 1:
				name = "match"
			case 2:
				name = "gmatch"
			case 3:
				name = "gsub"
			}
			args := []Value{String(source), String(pattern)}
			if mode%4 == 3 {
				args = append(args, String(replacement))
			}
			values, err := s.Call(table.RawGet(String(name)), args...)
			if mode%4 != 2 || err != nil {
				return fuzzLibraryResult(t, values, err)
			}
			fn := values[0]
			var out strings.Builder
			for range len(source) + 2 {
				values, err = s.Call(fn)
				out.WriteString(fuzzLibraryResult(t, values, err))
				if err != nil || len(values) == 0 {
					return out.String()
				}
			}
			t.Fatal("gmatch did not advance")
			return ""
		}
		if left, right := run(), run(); left != right {
			t.Fatalf("pattern result is not deterministic: %q / %q", left, right)
		}
	})
}

func FuzzFormat(f *testing.F) {
	for _, format := range []string{"%s", "%q", "%99.99f", "%#.0o", "%08x", "%", "%000000d", "%z", "%c%c", "%g\x00%s"} {
		f.Add(format, 3.7, "a\x00b\n", byte(0))
	}
	f.Fuzz(func(t *testing.T, format string, number float64, text string, mode byte) {
		if len(format) > 128 || len(text) > 256 {
			t.Skip()
		}
		run := func() string {
			s := fuzzLibraryState()
			fn := s.globals.RawGet(String("string")).Table().RawGet(String("format"))
			value := Number(number)
			switch mode % 3 {
			case 1:
				value = String(text)
			case 2:
				value = Bool(false)
			}
			values, err := s.Call(fn, String(format), value, value, value, value)
			return fuzzLibraryResult(t, values, err)
		}
		if left, right := run(), run(); left != right {
			t.Fatalf("format result is not deterministic: %q / %q", left, right)
		}
	})
}
