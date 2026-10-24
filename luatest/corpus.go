package luatest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/acneism/lua"
)

const wrapper = `local function __capture__(ok, ...)
  if not ok then return "ERR " .. tostring((...)) end
  local n = select('#', ...)
  local out = {}
  for i = 1, n do out[i] = tostring((select(i, ...))) end
  return table.concat(out, ",")
end
local f, e = loadstring(%s, "@user_script")
if not f then return "ERR " .. tostring(e) end
return __capture__(pcall(f))`

type snippet struct {
	Src  string `json:"src"`
	Want string `json:"want"`
}

type limit struct {
	Src   string `json:"src"`
	Steps int    `json:"steps"`
}

type normalization struct {
	Src  string `json:"src"`
	Rule string `json:"rule"`
}

func readJSON(t *testing.T, path string, dest any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, dest); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

func RunCorpus(t *testing.T, files []string, options lua.Options) {
	t.Helper()
	var snippets []snippet
	limits, normalizations := make(map[string]int), make(map[string]string)
	directories, seen := make(map[string]bool), make(map[string]bool)
	for _, file := range files {
		var items []snippet
		readJSON(t, file, &items)
		for _, item := range items {
			if seen[item.Src] {
				t.Fatalf("duplicate corpus source in %s: %s", file, item.Src)
			}
			seen[item.Src] = true
			snippets = append(snippets, item)
		}
		directory := filepath.Dir(file)
		if directories[directory] {
			continue
		}
		directories[directory] = true
		var budget []limit
		readJSON(t, filepath.Join(directory, "limits.json"), &budget)
		for _, item := range budget {
			if _, duplicate := limits[item.Src]; duplicate || item.Steps <= 1000000 || item.Steps > 10000000 {
				t.Fatalf("invalid corpus limit: %+v", item)
			}
			limits[item.Src] = item.Steps
		}
		var rules []normalization
		readJSON(t, filepath.Join(directory, "normalization.json"), &rules)
		for _, item := range rules {
			if _, duplicate := normalizations[item.Src]; duplicate || item.Rule != "nan-sign" {
				t.Fatalf("invalid normalization: %+v", item)
			}
			normalizations[item.Src] = item.Rule
		}
	}
	if len(snippets) == 0 {
		t.Fatal("no corpus entries executed")
	}
	for index, item := range snippets {
		steps := limits[item.Src]
		delete(limits, item.Src)
		if steps == 0 {
			steps = 1000000
		}
		rule := normalizations[item.Src]
		delete(normalizations, item.Src)
		t.Run(fmt.Sprintf("%03d", index+1), func(t *testing.T) {
			runSnippet(t, item, options, steps, rule)
		})
	}
	for source := range limits {
		if seen[source] {
			t.Fatalf("corpus limit did not execute: %s", source)
		}
	}
	for source := range normalizations {
		if seen[source] {
			t.Fatalf("normalization did not execute: %s", source)
		}
	}
	t.Logf("executed %d snippets", len(snippets))
}

func runSnippet(t *testing.T, item snippet, options lua.Options, limit int, rule string) {
	t.Helper()
	steps, hostSteps := 0, 0
	hostHook, hostEvery := options.Hook, options.HookEvery
	if hostEvery <= 0 {
		hostEvery = 10000
	}
	options.HookEvery = min(1000, hostEvery)
	options.Hook = func() error {
		steps += options.HookEvery
		if steps > limit {
			return fmt.Errorf("corpus snippet exceeded %d steps", limit)
		}
		hostSteps += options.HookEvery
		if hostHook != nil && hostSteps >= hostEvery {
			hostSteps = 0
			return hostHook()
		}
		return nil
	}
	s := lua.NewState(options)
	env := environment(t, s)
	proto, err := lua.Compile([]byte(fmt.Sprintf(wrapper, quote(item.Src))), "@capture")
	if err != nil {
		t.Fatal(err)
	}
	values, err := s.Call(s.Load(proto, env))
	if err != nil {
		t.Fatalf("wrapper failed: %v; source: %s", err, item.Src)
	}
	if len(values) != 1 || values[0].Type() != lua.TypeString {
		t.Fatalf("unexpected wrapper result: %v", values)
	}
	got := strings.TrimPrefix(values[0].Text(), "ERR ")
	if got != item.Want && (rule != "nan-sign" || runtime.GOARCH != "arm64" || !equalNaNFields(got, item.Want)) {
		t.Fatalf("want %q\ngot  %q\nsource: %s", item.Want, got, item.Src)
	}
}

func set(t *testing.T, table *lua.Table, key string, value lua.Value) {
	t.Helper()
	if err := table.RawSet(lua.String(key), value); err != nil {
		t.Fatal(err)
	}
}

func environment(t *testing.T, s *lua.State) *lua.Table {
	t.Helper()
	globals := s.Globals()
	meta := s.NewTable(0, 1)
	set(t, meta, "__index", s.Function("", func(s *lua.State, args []lua.Value) ([]lua.Value, error) {
		message := lua.String("Script attempted to access nonexistent global variable '" + args[1].String() + "'")
		return s.Call(globals.RawGet(lua.String("error")), message, lua.Number(2))
	}))
	if err := globals.SetMetatable(meta); err != nil {
		t.Fatal(err)
	}
	globals.SetReadOnly(true)
	for _, name := range []string{"coroutine", "string", "table", "math"} {
		globals.RawGet(lua.String(name)).Table().SetReadOnly(true)
	}
	outer := s.NewTable(0, 32)
	for key, value, _ := globals.Next(lua.Nil()); !key.IsNil(); key, value, _ = globals.Next(key) {
		if err := outer.RawSet(key, value); err != nil {
			t.Fatal(err)
		}
	}
	join := s.NewTable(0, 1)
	set(t, join, "concat", s.Function("concat", func(_ *lua.State, args []lua.Value) ([]lua.Value, error) {
		table := args[0].Table()
		parts := make([]string, table.Len())
		for index := range parts {
			parts[index] = table.RawGet(lua.Number(float64(index + 1))).String()
		}
		return []lua.Value{lua.String(strings.Join(parts, args[1].String()))}, nil
	}))
	set(t, outer, "table", join.Value())
	return outer
}

func quote(src string) string {
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

func equalNaNFields(got, want string) bool {
	left, right := strings.Split(got, ","), strings.Split(want, ",")
	if len(left) != len(right) {
		return false
	}
	for index, value := range left {
		if value != right[index] && ((value != "nan" && value != "-nan") || (right[index] != "nan" && right[index] != "-nan")) {
			return false
		}
	}
	return true
}
