package redis_test

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"os"
	"strconv"
	"strings"
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

func scriptCase(random *rand.Rand) redistest.Case {
	key := fmt.Sprintf("k%d", random.IntN(8))
	other := fmt.Sprintf("o%d", random.IntN(8))
	value := fmt.Sprint(random.IntN(10000) - 5000)
	item := redistest.Case{Keys: []string{key, other}, Args: []string{value}}
	switch random.IntN(10) {
	case 0:
		item.Source = `return {redis.call('SET',KEYS[1],ARGV[1]),redis.call('GET',KEYS[1])}`
	case 1:
		item.Setup = [][]string{{"SET", key, value}}
		item.Source = `return {redis.call('INCR',KEYS[1]),redis.call('GET',KEYS[1])}`
	case 2:
		item.Setup = [][]string{{"RPUSH", key, "a", "b"}}
		item.Source = `return {pcall(redis.call,'GET',KEYS[1]),redis.pcall('INCR',KEYS[1])}`
	case 3:
		item.Setup = [][]string{{"HSET", key, "f0", value}}
		item.Source = `redis.call('HSET',KEYS[1],'f1',ARGV[1]) return redis.call('HGETALL',KEYS[1])`
	case 4:
		item.Source = `redis.call('LPUSH',KEYS[1],ARGV[1],'x') return redis.call('LRANGE',KEYS[1],0,-1)`
	case 5:
		item.Setup = [][]string{{"SET", key, value}}
		item.Source = `return {redis.call('EXISTS',KEYS[1]),redis.call('DEL',KEYS[1]),redis.call('GET',KEYS[1])}`
	case 6:
		item.Setup = [][]string{{"SET", key, value}}
		item.Source = "#!lua flags=no-writes\nreturn redis.pcall('SET',KEYS[1],ARGV[1])"
	case 7:
		item.Source = `return {redis.pcall('NOPE'),redis.pcall('GET',true)}`
	case 8:
		item.Source = fmt.Sprintf("return {redis.call('ECHO',%d),redis.call('ECHO',1/%d)}", random.IntN(1000000)-500000, random.IntN(99)+1)
	case 9:
		item.Setup = [][]string{{"SET", key, value}}
		item.Source = `return redis.call('MGET',KEYS[1],KEYS[2])`
	}
	return item
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

func luaCase(random *rand.Rand) string {
	a, b := random.IntN(20001)-10000, random.IntN(20001)-10000
	switch random.IntN(8) {
	case 0:
		return fmt.Sprintf("local a,b=%d,%d return {a+b,a-b,a*b,a/(b+1),a%%(b+1),tostring(a)..b}", a, b)
	case 1:
		return fmt.Sprintf("return {string.format('%%.8g',%d/7),string.format('%%08d',%d),tostring(%d/9)}", a, b, a)
	case 2:
		return fmt.Sprintf("return {tonumber(' %d '),tonumber('0x%x'),tonumber('bad'),('%d'+1)*2}", a, random.IntN(65536), b)
	case 3:
		words := []string{"abc def", "aaaab", "x1z", "zYX 123", "word__next"}
		word := words[random.IntN(len(words))]
		patterns := []string{"%a+", "([a-z]+)", "a-", "%d+", "%f[%a]%a+"}
		pattern := patterns[random.IntN(len(patterns))]
		return fmt.Sprintf("local s=%q local p=%q local a,n=string.gsub(s,p,'%%0x') local x,y=string.find(s,p) return {a,n,x,y,string.match(s,p)}", word, pattern)
	case 4:
		return fmt.Sprintf("local t={%d,%d,4,-7,0,19} table.insert(t,3,7) table.remove(t,2) table.sort(t) return {table.concat(t,','),#t}", a, b)
	case 5:
		var source strings.Builder
		source.WriteString("local t={} ")
		for range 8 {
			fmt.Fprintf(&source, "t['k%d']=%d ", random.IntN(16), random.IntN(100))
		}
		source.WriteString("local r={} for k,v in pairs(t) do r[#r+1]=k..':'..v end return table.concat(r,',')")
		return source.String()
	case 6:
		return fmt.Sprintf("math.randomseed(%d) return {math.random(),math.random(-7,13),math.random(1000)}", a)
	default:
		return fmt.Sprintf("local function f(n,...) if n==0 then return ... end return f(n-1,...) end return {f(%d,%d,%d,'x')}", random.IntN(10), a, b)
	}
}
