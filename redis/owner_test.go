package redis_test

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/acneism/lua/redis"
)

func TestOwnerDecisions(t *testing.T) {
	data, err := os.ReadFile("testdata/owner-decisions.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name   string `json:"name"`
		Source string `json:"src"`
		Want   string `json:"want"`
	}
	if err = json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	engine := redis.NewEngine(redis.Config{})
	for _, item := range cases {
		t.Run(item.Name, func(t *testing.T) {
			digest := sha1.Sum([]byte(item.Source))
			want := strings.ReplaceAll(item.Want, "<sha>", hex.EncodeToString(digest[:]))
			reply := engine.Eval([]byte(item.Source), nil, nil, redis.Run{})
			if got := string(reply.AppendRESP(nil, 2)); got != want {
				t.Errorf("want %q\n got %q", want, got)
			}
		})
	}
}
