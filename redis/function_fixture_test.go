package redis_test

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/acneism/lua/redis"
)

type functionFixture struct {
	Args  [][]byte `json:"args"`
	Proto int      `json:"proto"`
	Want  []byte   `json:"want"`
}

func TestFunctionFixtures(t *testing.T) {
	data, err := os.ReadFile("testdata/functions.json")
	if err != nil {
		t.Fatal(err)
	}
	var steps []functionFixture
	if err = json.Unmarshal(data, &steps); err != nil {
		t.Fatal(err)
	}
	engine := redis.NewEngine(redis.Config{})
	for index, step := range steps {
		got := engine.Command(step.Args, redis.Run{Proto: step.Proto})
		want := step.Want
		if string(step.Args[0]) == "FUNCTION" && string(step.Args[1]) == "LIST" {
			parsed, _, err := redis.ParseRESP(want)
			if err != nil {
				t.Fatal(err)
			}
			want = normalizeFunctionList(parsed).AppendRESP(nil, step.Proto)
			got = normalizeFunctionList(got)
		}
		if bytes.HasPrefix(want, []byte("-ERR ")) && got.Type == redis.ErrorReply && maskConflict(strings.TrimRight(string(want[1:]), "\r\n")) == maskConflict(got.Str) {
			continue
		}
		if actual := got.AppendRESP(nil, step.Proto); !bytes.Equal(want, actual) {
			t.Fatalf("step %d proto %d %q: want %q, got %q", index, step.Proto, step.Args, want, actual)
		}
	}
	if len(engine.FunctionDump()) < 10 || engine.Libraries() == nil {
		t.Fatal("empty registry view")
	}
}
