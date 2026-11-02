package redis_test

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"testing"

	"github.com/acneism/lua/internal/redistest"
	"github.com/acneism/lua/redis"
)

func functionStep(random *rand.Rand) []string {
	library := fmt.Sprintf("lib%d", random.IntN(3))
	function := fmt.Sprintf("lib%d_f%d", random.IntN(3), random.IntN(3))
	switch random.IntN(16) {
	case 0, 1, 2:
		return []string{"FUNCTION", "LOAD", functionLibraryCode(random, library)}
	case 3:
		return []string{"FUNCTION", "LOAD", "REPLACE", functionLibraryCode(random, library)}
	case 4:
		return []string{"FUNCTION", "LOAD", functionBadCode(random)}
	case 5:
		return []string{"FUNCTION", "DELETE", pick(random, library, "missing")}
	case 6:
		return []string{"FUNCTION", "LIST"}
	case 7:
		return []string{"FUNCTION", "LIST", "WITHCODE"}
	case 8:
		return []string{"FUNCTION", "LIST", "LIBRARYNAME", pick(random, "lib*", "LIB?", "lib[01]", "*0", "none", "", "lib[^0]", "\\lib0"), pick(random, "WITHCODE", "libraryname")}[:3+random.IntN(2)]
	case 9:
		return []string{"FUNCTION", "STATS"}
	case 10:
		return []string{"FUNCTION", "FLUSH", pick(random, "SYNC", "async", "bad")}
	case 11:
		return []string{"FUNCTION", pick(random, "LIST", "RESTORE", "BAD", "DELETE", "FLUSH", "LOAD"), "x", "y", "z", "w"}
	case 12:
		return []string{"FCALL_RO", function, "0"}
	case 13:
		return []string{pick(random, "FCALL", "FCALL_RO"), function, pick(random, "x", "-1", "9", "1"), "k"}
	}
	return []string{"FCALL", function, "1", "key", "value"}
}

func TestFunctionDifferential(t *testing.T) {
	reference, backend := differentialClients(t)
	seeds, steps := differentialSeeds(t)
	for _, seed := range seeds {
		random := rand.New(rand.NewPCG(seed, seed^0x452821e638d01377))
		engine := redis.NewEngine(redis.Config{})
		host := &redistest.Host{Client: backend}
		if err := redistest.Prepare(reference, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := reference.Do("FUNCTION", "FLUSH"); err != nil {
			t.Fatal(err)
		}
		for step := range steps {
			proto := 2 + random.IntN(2)
			if _, err := reference.Do("HELLO", strconv.Itoa(proto)); err != nil {
				t.Fatal(err)
			}
			command := functionStep(random)
			if random.IntN(40) == 0 {
				if _, err := reference.Do("FUNCTION", "FLUSH"); err != nil {
					t.Fatal(err)
				}
				engine.FunctionFlush()
			}
			argv := make([][]byte, len(command))
			for index, arg := range command {
				argv[index] = []byte(arg)
			}
			if err := redistest.Prepare(reference, nil); err != nil {
				t.Fatal(err)
			}
			_, wire, err := reference.CommandWire(argv)
			if err != nil {
				t.Fatalf("seed %d step %d %q: %v", seed, step, command, err)
			}
			if err = redistest.Prepare(backend, nil); err != nil {
				t.Fatal(err)
			}
			got := engine.Command(argv, redis.Run{Host: host, Proto: proto})
			if command[0] == "FUNCTION" && command[1] == "LIST" {
				parsed, _, err := redis.ParseRESP(wire)
				if err != nil {
					t.Fatal(err)
				}
				wire = normalizeFunctionList(parsed).AppendRESP(nil, proto)
				got = normalizeFunctionList(got)
			}
			if actual := got.AppendRESP(nil, proto); !bytes.Equal(wire, actual) {
				t.Fatalf("seed %d step %d proto %d %q: want %q, got %q", seed, step, proto, command, wire, actual)
			}
		}
	}
}

func functionListing(t *testing.T, client *redistest.Client, engine *redis.Engine) (string, string) {
	t.Helper()
	reply, wire, err := client.CommandWire([][]byte{[]byte("FUNCTION"), []byte("LIST"), []byte("WITHCODE")})
	if err != nil {
		t.Fatal(err)
	}
	_ = reply
	parsed, _, err := redis.ParseRESP(wire)
	if err != nil {
		t.Fatal(err)
	}
	return string(normalizeFunctionList(parsed).AppendRESP(nil, 2)), string(normalizeFunctionList(engine.FunctionList("", true)).AppendRESP(nil, 2))
}

func TestFunctionDumpRestore(t *testing.T) {
	reference, _ := differentialClients(t)
	if _, err := reference.Do("HELLO", "2"); err != nil {
		t.Fatal(err)
	}
	seeds, steps := differentialSeeds(t)
	for _, seed := range seeds {
		random := rand.New(rand.NewPCG(seed, seed^0xbe5466cf34e90c6c))
		for step := range min(steps, 400) {
			engine := redis.NewEngine(redis.Config{})
			if _, err := reference.Do("FUNCTION", "FLUSH"); err != nil {
				t.Fatal(err)
			}
			for index := range 1 + random.IntN(3) {
				code := functionLibraryCode(random, fmt.Sprintf("lib%d", index))
				if random.IntN(2) == 0 {
					code += "--" + strings.Repeat("padding ", random.IntN(200))
				}
				if reply, err := reference.Do("FUNCTION", "LOAD", code); err != nil || reply.Type == redis.ErrorReply {
					t.Fatalf("load %q: %v %+v", code, err, reply)
				}
			}
			dump, err := reference.Do("FUNCTION", "DUMP")
			if err != nil {
				t.Fatal(err)
			}
			if reply := engine.FunctionRestore([]byte(dump.Str), ""); reply.Type == redis.ErrorReply {
				t.Fatalf("seed %d step %d: restore of a Redis dump: %s", seed, step, reply.Str)
			}
			if want, got := functionListing(t, reference, engine); want != got {
				t.Fatalf("seed %d step %d: want %q, got %q", seed, step, want, got)
			}
			own := engine.FunctionDump()
			if reply, err := reference.Do("FUNCTION", "RESTORE", string(own), "FLUSH"); err != nil || reply.Type == redis.ErrorReply {
				t.Fatalf("seed %d step %d: Redis rejected our dump: %v %+v", seed, step, err, reply)
			}
			if want, got := functionListing(t, reference, engine); want != got {
				t.Fatalf("seed %d step %d after our dump: want %q, got %q", seed, step, want, got)
			}
			payload := pick(random, string(own), dump.Str)
			switch random.IntN(9) {
			case 0:
				payload = payload[:len(payload)-1]
			case 1:
				payload = payload[:len(payload)-3] + "xyz"
			case 2:
				payload = "abc"
			case 3:
				payload = ""
			case 4:
				corrupted := []byte(payload)
				corrupted[random.IntN(len(corrupted))] ^= 0xff
				payload = string(corrupted)
			}
			policy := pick(random, "", "APPEND", "append", "REPLACE", "FLUSH", "flush", "bad", "x")
			arguments := []string{"FUNCTION", "RESTORE", payload}
			if policy != "" {
				arguments = append(arguments, policy)
			}
			reply, err := reference.Do(arguments...)
			if err != nil {
				t.Fatal(err)
			}
			got := engine.FunctionRestore([]byte(payload), policy)
			if reply.Type != got.Type || maskConflict(reply.Str) != maskConflict(got.Str) {
				t.Fatalf("seed %d step %d policy %q payload %q: want %+v, got %+v", seed, step, policy, payload, reply, got)
			}
			if want, got := functionListing(t, reference, engine); want != got {
				t.Fatalf("seed %d step %d policy %q: want %q, got %q", seed, step, policy, want, got)
			}
		}
	}
}

func maskConflict(text string) string {
	for _, prefix := range []string{"ERR Library ", "ERR Function "} {
		if strings.HasPrefix(text, prefix) && strings.HasSuffix(text, " already exists") {
			return prefix
		}
	}
	return text
}

func TestShebangReadOnly(t *testing.T) {
	reference, backend := differentialClients(t)
	host := &redistest.Host{Client: backend}
	engine := redis.NewEngine(redis.Config{})
	for _, source := range []string{"#!lua\nreturn 1", "#!lua flags=no-writes\nreturn 1", "#!lua flags=no-writes\nreturn redis.call('SET','a','b')", "return 1", "#!lua flags=allow-oom\nreturn 1"} {
		compareRun(t, redistest.Case{Source: source, ReadOnly: true}, engine, host, reference, 0, 0, false)
	}
}
