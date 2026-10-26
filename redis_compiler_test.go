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
		t.Skip(err)
	}
	var cases []struct{ Src, Want string }
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for n, item := range cases {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			var raw *redisProto
			var failure any
			func() { defer func() { failure = recover() }(); raw = parseRedis([]byte(item.Src), "@user_script", 0) }()
			if failure != nil {
				if e, ok := failure.(*Error); ok && e.Error() == item.Want {
					return
				}
				t.Fatalf("parse: %v; want %s", failure, item.Want)
			}
			got := hex.EncodeToString(dumpRedis(raw))
			if got != item.Want {
				at := 0
				for at < len(got) && at < len(item.Want) && got[at] == item.Want[at] {
					at++
				}
				t.Fatalf("dump differs at hex %d: want %s; got %s", at, item.Want, got)
			}
		})
	}
}
