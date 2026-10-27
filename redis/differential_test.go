package redis_test

import (
	"bytes"
	"math/rand/v2"
	"os"
	"strconv"
	"testing"

	"github.com/acneism/lua/internal/redistest"
	"github.com/acneism/lua/redis"
)

func differentialClients(t *testing.T) (*redistest.Client, *redistest.Client) {
	t.Helper()
	address := os.Getenv("REDIS_REF_ADDR")
	if address == "" {
		t.Skip("REDIS_REF_ADDR is not set")
	}
	first, err := redistest.Dial(address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	if err = first.RequireVersion(); err != nil {
		t.Fatal(err)
	}
	second, err := redistest.Dial(address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	if database := os.Getenv("REDIS_REF_DB"); database != "" {
		for _, client := range []*redistest.Client{first, second} {
			reply, err := client.Do("SELECT", database)
			if err != nil || reply.Type == redis.ErrorReply {
				t.Fatalf("SELECT %s: %v %+v", database, err, reply)
			}
		}
	}
	return first, second
}

func differentialSeeds(t *testing.T) ([]uint64, int) {
	t.Helper()
	seeds := []uint64{1, 42, 139, 99991, 0xdeadbeef}
	steps := 2000
	if text := os.Getenv("LUA_DIFF_SEED"); text != "" {
		seed, err := strconv.ParseUint(text, 0, 64)
		if err != nil {
			t.Fatal(err)
		}
		seeds = []uint64{seed}
	}
	if text := os.Getenv("LUA_DIFF_STEPS"); text != "" {
		n, err := strconv.Atoi(text)
		if err != nil || n < 1 {
			t.Fatalf("invalid LUA_DIFF_STEPS %q", text)
		}
		steps = n
	}
	return seeds, steps
}

func compareRun(t *testing.T, item redistest.Case, engine *redis.Engine, host *redistest.Host, reference *redistest.Client, seed uint64, step int, snapshot bool) {
	t.Helper()
	proto := item.Proto
	if proto != 3 {
		proto = 2
	}
	if _, err := reference.Do("HELLO", strconv.Itoa(proto)); err != nil {
		t.Fatal(err)
	}
	if err := redistest.Prepare(reference, item.Setup); err != nil {
		t.Fatal(err)
	}
	_, expected, err := item.ReferenceWire(reference)
	if err != nil {
		t.Fatalf("seed %d step %d reference: %v", seed, step, err)
	}
	var before []redistest.Record
	if snapshot {
		before, err = redistest.Snapshot(reference)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = redistest.Prepare(host.Client, item.Setup); err != nil {
		t.Fatal(err)
	}
	actual := item.Execute(engine, host).AppendRESP(nil, proto)
	if !bytes.Equal(expected, actual) {
		t.Fatalf("seed %d step %d source %q: want %q, got %q", seed, step, item.Source, expected, actual)
	}
	if snapshot {
		after, err := redistest.Snapshot(host.Client)
		if err != nil {
			t.Fatal(err)
		}
		if len(before) != len(after) {
			t.Fatalf("seed %d step %d key counts differ", seed, step)
		}
		for i, record := range before {
			if record != after[i] {
				t.Fatalf("seed %d step %d DUMP differs at %q", seed, step, record.Key)
			}
		}
	}
}

func TestScriptScenario(t *testing.T) {
	reference, backend := differentialClients(t)
	host := &redistest.Host{Client: backend}
	engine := redis.NewEngine(redis.Config{})
	item := redistest.Case{Source: `redis.call('SET',KEYS[1],ARGV[1]) redis.call('INCR',KEYS[2]) local v=redis.call('MGET',KEYS[1],KEYS[2],KEYS[3]) return {v,redis.pcall('INCR',KEYS[1]),redis.call('HGETALL','h')}`, Keys: []string{"k", "counter", "missing"}, Args: []string{"value"}, Setup: [][]string{{"HSET", "h", "a", "1", "b", "2"}}}
	compareRun(t, item, engine, host, reference, 0, 0, true)
}

func TestScriptDifferential(t *testing.T) {
	reference, backend := differentialClients(t)
	host := &redistest.Host{Client: backend}
	engine := redis.NewEngine(redis.Config{})
	seeds, steps := differentialSeeds(t)
	for _, seed := range seeds {
		random := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
		for step := range steps {
			item := scriptCase(random)
			compareRun(t, item, engine, host, reference, seed, step, true)
		}
	}
}

func TestLuaDifferential(t *testing.T) {
	reference, backend := differentialClients(t)
	host := &redistest.Host{Client: backend}
	engine := redis.NewEngine(redis.Config{})
	seeds, steps := differentialSeeds(t)
	for _, seed := range seeds {
		random := rand.New(rand.NewPCG(seed, seed^0x243f6a8885a308d3))
		for step := range steps {
			compareRun(t, redistest.Case{Source: luaCase(random)}, engine, host, reference, seed, step, false)
		}
	}
}
