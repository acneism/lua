package lua

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"strconv"
	"testing"
)

func TestRedisCompilerDump(t *testing.T) {
	path := "testdata/compiler-dump.json"
	if override := os.Getenv("LUA_DUMP_FIXTURE"); override != "" {
		path = override
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cases []referenceSnippet
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool)
	for n, item := range cases {
		if len(item.Src) > 4096 || seen[item.Src] {
			t.Fatalf("invalid dump source %d", n)
		}
		seen[item.Src] = true
		t.Run(strconv.Itoa(n+1), func(t *testing.T) {
			s := NewState(Options{})
			values, err := s.Call(s.Globals().RawGet(String("loadstring")), String(item.Src), String("@user_script"))
			if err != nil {
				t.Fatal(err)
			}
			if len(values) == 2 && values[0].IsNil() {
				if values[1].Text() != item.Want {
					t.Fatalf("want %q; got %v", item.Want, values)
				}
				return
			}
			if len(values) != 1 || values[0].Type() != TypeFunction {
				t.Fatalf("loadstring result: %v", values)
			}
			dumped, err := s.Call(s.Globals().RawGet(String("string")).Table().RawGet(String("dump")), values[0])
			if err != nil || len(dumped) != 1 {
				t.Fatalf("dump result: %v %v", dumped, err)
			}
			got := hex.EncodeToString([]byte(dumped[0].Text()))
			if got != item.Want {
				at := 0
				for at < len(got) && at < len(item.Want) && got[at] == item.Want[at] {
					at++
				}
				t.Fatalf("dump differs at hex %d: want %s; got %s", at, item.Want, got)
			}
		})
	}
	for _, path := range []string{"testdata/snippets.json", "testdata/libm.json", "luatest/testdata/environment.json"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var corpus []referenceSnippet
		if err := json.Unmarshal(data, &corpus); err != nil {
			t.Fatal(err)
		}
		for _, item := range corpus {
			if len(item.Src) <= 4096 && !seen[item.Src] {
				t.Fatalf("missing dump source in %s: %s", path, item.Src)
			}
		}
	}
	t.Logf("compared %d captured Redis prototypes", len(cases))
}
