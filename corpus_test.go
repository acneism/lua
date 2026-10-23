package lua

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
)

const captureWrapper = `local function __capture__(ok, ...)
  if not ok then return "ERR " .. tostring((...)) end
  local n = select('#', ...)
  local out = {}
  for i = 1, n do out[i] = tostring((select(i, ...))) end
  return table.concat(out, ",")
end
local f, e = loadstring(%s, "@user_script")
if not f then return "ERR " .. tostring(e) end
return __capture__(pcall(f))`

type referenceSnippet struct {
	Src  string `json:"src"`
	Want string `json:"want"`
}

type pendingSnippet struct {
	Src    string `json:"src"`
	Stage  string `json:"stage"`
	Reason string `json:"reason"`
}

type corpusNormalization struct {
	Src  string `json:"src"`
	Rule string `json:"rule"`
}

func equalNaNFields(got, want string) bool {
	left, right := strings.Split(got, ","), strings.Split(want, ",")
	if len(left) != len(right) {
		return false
	}
	for index, value := range left {
		if value == right[index] {
			continue
		}
		if (value == "nan" || value == "-nan") && (right[index] == "nan" || right[index] == "-nan") {
			continue
		}
		return false
	}
	return true
}

func readCorpus(t *testing.T) []referenceSnippet {
	t.Helper()
	data, err := os.ReadFile("testdata/snippets.json")
	if err != nil {
		t.Fatal(err)
	}
	var snippets []referenceSnippet
	if err := json.Unmarshal(data, &snippets); err != nil {
		t.Fatal(err)
	}
	return snippets
}

func quoteLua(src string) string {
	var out strings.Builder
	out.WriteByte('"')
	for index := 0; index < len(src); index++ {
		switch c := src[index]; c {
		case '"':
			out.WriteString(`\"`)
		case '\\':
			out.WriteString(`\\`)
		default:
			if c < 32 || c == 127 {
				fmt.Fprintf(&out, `\%03d`, c)
			} else {
				out.WriteByte(c)
			}
		}
	}
	out.WriteByte('"')
	return out.String()
}

func captureEnvironment(s *State) *Table {
	meta := s.NewTable(0, 1)
	meta.set(String("__index"), s.Function("", func(s *State, args []Value) ([]Value, error) {
		s.raise("Script attempted to access nonexistent global variable '"+args[1].String()+"'", 1)
		return nil, nil
	}))
	s.globals.metatable = meta
	s.globals.readonly = true
	for _, name := range []string{"coroutine", "string", "table", "math"} {
		s.globals.RawGet(String(name)).Table().readonly = true
	}
	outer := s.NewTable(0, 32)
	for key, value, _ := s.globals.Next(Nil()); !key.IsNil(); key, value, _ = s.globals.Next(key) {
		outer.set(key, value)
	}
	join := s.NewTable(0, 1)
	join.set(String("concat"), s.Function("concat", func(_ *State, args []Value) ([]Value, error) {
		table := args[0].Table()
		parts := make([]string, table.Len())
		for index := range parts {
			parts[index] = table.RawGet(Number(float64(index + 1))).String()
		}
		return []Value{String(strings.Join(parts, args[1].String()))}, nil
	}))
	outer.set(String("table"), join.Value())
	return outer
}

func TestCorpus(t *testing.T) {
	normalizationData, err := os.ReadFile("testdata/normalization.json")
	if err != nil {
		t.Fatal(err)
	}
	var normalizations []corpusNormalization
	if err := json.Unmarshal(normalizationData, &normalizations); err != nil {
		t.Fatal(err)
	}
	normalizing := make(map[string]string)
	for _, item := range normalizations {
		if _, duplicate := normalizing[item.Src]; duplicate || item.Rule != "nan-sign" {
			t.Fatalf("invalid normalization: %+v", item)
		}
		normalizing[item.Src] = item.Rule
	}
	data, err := os.ReadFile("testdata/pending.json")
	if err != nil {
		t.Fatal(err)
	}
	var pending []pendingSnippet
	if err := json.Unmarshal(data, &pending); err != nil {
		t.Fatal(err)
	}
	waiting := make(map[string]pendingSnippet)
	for _, item := range pending {
		if _, duplicate := waiting[item.Src]; duplicate || item.Stage == "" || item.Reason == "" {
			t.Fatalf("invalid pending entry: %+v", item)
		}
		waiting[item.Src] = item
	}
	ran := 0
	for index, snippet := range readCorpus(t) {
		if _, deferred := waiting[snippet.Src]; deferred {
			delete(waiting, snippet.Src)
			continue
		}
		ran++
		rule := normalizing[snippet.Src]
		delete(normalizing, snippet.Src)
		t.Run(fmt.Sprintf("%03d", index+1), func(t *testing.T) {
			steps := 0
			s := NewState(Options{HookEvery: 1000, Hook: func() error {
				steps += 1000
				if steps > 1000000 {
					return errors.New("corpus snippet exceeded one million steps")
				}
				return nil
			}})
			env := captureEnvironment(s)
			proto, err := Compile([]byte(fmt.Sprintf(captureWrapper, quoteLua(snippet.Src))), "@capture")
			if err != nil {
				t.Fatal(err)
			}
			values, err := s.Call(s.Load(proto, env))
			if err != nil {
				t.Fatalf("wrapper failed: %v; source: %s", err, snippet.Src)
			}
			if len(values) != 1 || values[0].kind != TypeString {
				t.Fatalf("unexpected wrapper result: %v", values)
			}
			got := strings.TrimPrefix(values[0].text, "ERR ")
			if got != snippet.Want {
				if rule == "nan-sign" && runtime.GOARCH == "arm64" && equalNaNFields(got, snippet.Want) {
					return
				}
				t.Fatalf("want %q\ngot  %q\nsource: %s", snippet.Want, got, snippet.Src)
			}
		})
	}
	if len(waiting) > 0 {
		t.Fatalf("%d pending sources are absent from corpus", len(waiting))
	}
	if len(normalizing) > 0 {
		t.Fatalf("%d normalization entries did not execute", len(normalizing))
	}
	if ran == 0 {
		t.Fatal("no corpus entries executed")
	}
	t.Logf("executed %d snippets; %d explicitly pending", ran, len(pending))
}
