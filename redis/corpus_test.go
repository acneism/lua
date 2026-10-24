package redis_test

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/acneism/lua/internal/redistest"
	"github.com/acneism/lua/redis"
)

type replayHost struct {
	t        *testing.T
	trace    []redistest.Trace
	position int
	current  *redistest.Trace
	called   bool
}

func (h *replayHost) Lookup(argv [][]byte) (redis.Command, bool) {
	if h.current != nil && h.current.Call && !h.called {
		h.t.Errorf("previous host call was skipped")
	}
	if h.position >= len(h.trace) {
		h.t.Errorf("unexpected lookup %q", argv)
		return redis.Command{}, false
	}
	step := &h.trace[h.position]
	h.position++
	h.current = step
	h.called = false
	if len(argv) != len(step.Argv) {
		h.t.Errorf("lookup %q, expected %q", argv, step.Argv)
		return redis.Command{}, false
	}
	for i, arg := range argv {
		if string(arg) != step.Argv[i] {
			h.t.Errorf("argument %d: got %q, expected %q", i, arg, step.Argv[i])
		}
	}
	return step.Command, step.Found
}

func (h *replayHost) Check(_ [][]byte) *redis.Reply { return nil }
func (h *replayHost) Log(_ int, _ string)           {}
func (h *replayHost) Call(_ [][]byte, proto int) redis.Reply {
	if h.current == nil || !h.current.Call {
		h.t.Errorf("unexpected host call")
		return redis.Error("ERR unexpected host call")
	}
	if expected := h.current.Proto; expected != 0 && expected != proto {
		h.t.Errorf("host protocol %d, expected %d", proto, expected)
	}
	h.called = true
	reply, n, err := redis.ParseRESP(h.current.Reply)
	if err != nil || n != len(h.current.Reply) {
		h.t.Fatalf("invalid captured command response: %v", err)
	}
	return reply
}

func TestEngineCorpus(t *testing.T) {
	data, err := os.ReadFile("testdata/engine.json")
	if err != nil {
		t.Fatal(err)
	}
	var entries []redistest.Case
	if err = json.Unmarshal(data, &entries); err != nil {
		t.Fatal(err)
	}
	engine := redis.NewEngine(redis.Config{})
	for _, item := range entries {
		t.Run(item.Name, func(t *testing.T) {
			host := &replayHost{t: t, trace: item.Trace}
			reply := item.Execute(engine, host)
			proto := item.Proto
			if proto != 3 {
				proto = 2
			}
			actual := reply.AppendRESP(nil, proto)
			if !bytes.Equal(actual, item.Want) {
				t.Errorf("want %q\n got %q", item.Want, actual)
			}
			if host.position != len(host.trace) || host.current != nil && host.current.Call && !host.called {
				t.Errorf("not all captured host calls consumed")
			}
		})
	}
}
