package lua

import (
	"encoding/json"
	"fmt"
	"os"
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

type corpusLimit struct {
	Src   string `json:"src"`
	Steps int    `json:"steps"`
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
