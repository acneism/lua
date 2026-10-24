package luatest

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/acneism/lua"
)

func fixture(t *testing.T, directory, name string, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunCorpusWithHostOptions(t *testing.T) {
	var originals []snippet
	readJSON(t, filepath.Join("..", "testdata", "snippets.json"), &originals)
	wanted := map[string]bool{
		"return undefined_name":                        true,
		"return pcall(function() return rawlen end)":   true,
		"math.pi = 3":                                  true,
		"return string.format('%.3f', math.sin(0))":    true,
		"return pcall(function() coroutine.x = 1 end)": true,
	}
	var library, ordinary []snippet
	for _, item := range originals {
		if !wanted[item.Src] {
			continue
		}
		delete(wanted, item.Src)
		if item.Src == "return string.format('%.3f', math.sin(0))" {
			library = append(library, item)
		} else {
			ordinary = append(ordinary, item)
		}
	}
	if len(wanted) != 0 || len(library) != 1 || len(ordinary) != 4 {
		t.Fatalf("missing captured fixtures: %v", wanted)
	}
	directory := t.TempDir()
	fixture(t, directory, "limits.json", []limit{{Src: library[0].Src, Steps: 2000000}})
	fixture(t, directory, "normalization.json", []normalization{})
	paths := []string{
		fixture(t, directory, "snippets.json", ordinary),
		fixture(t, directory, "libm.json", library),
	}
	trigCalls, hookCalls := 0, 0
	RunCorpus(t, paths, lua.Options{
		Math: &lua.Math{Sin: func(x float64) float64 {
			trigCalls++
			return math.Sin(x)
		}},
		HookEvery: 3,
		Hook: func() error {
			hookCalls++
			return nil
		},
	})
	if trigCalls != 1 || hookCalls == 0 {
		t.Fatalf("host options were not used: trig=%d hook=%d", trigCalls, hookCalls)
	}
}
