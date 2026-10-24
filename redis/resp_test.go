package redis_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/acneism/lua/internal/redistest"
	"github.com/acneism/lua/redis"
)

func TestCapturedRESPFrames(t *testing.T) {
	data, err := os.ReadFile("testdata/protocol.json")
	if err != nil {
		t.Fatal(err)
	}
	var entries []redistest.Case
	if err = json.Unmarshal(data, &entries); err != nil {
		t.Fatal(err)
	}
	for _, item := range entries {
		t.Run(item.Name, func(t *testing.T) {
			reader := bufio.NewReader(iotest.OneByteReader(bytes.NewReader(item.Want)))
			reply, err := redis.ReadReply(reader)
			if err != nil {
				t.Fatal(err)
			}
			if actual := reply.AppendRESP(nil, item.Proto); !bytes.Equal(actual, item.Want) {
				t.Fatalf("round trip changed wire: %q != %q", actual, item.Want)
			}
			input := append(append([]byte(nil), item.Want...), item.Want...)
			reply, n, err := redis.ParseRESP(input)
			if err != nil || n != len(item.Want) {
				t.Fatalf("consumed %d: %v", n, err)
			}
			if !bytes.Equal(reply.AppendRESP(nil, item.Proto), item.Want) {
				t.Fatal("pipeline frame changed")
			}
		})
	}
}

func TestMalformedRESP(t *testing.T) {
	for _, input := range []string{"", "\r\n", "?bad\r\n", "+x\n", ":no\r\n", ",no\r\n", "#yes\r\n", "_x\r\n", "(\r\n", "(-\r\n", "(1-2\r\n", "(a\r\n", "$-2\r\n", "$536870913\r\n", "$x\r\n", "$3\r\nhi", "$1\r\nx!!", "=-1\r\n", "=2\r\nab\r\n", "=4\r\nabcx\r\n", "*1048577\r\n", "*-2\r\n", "%-1\r\n", "*1\r\n", "%1\r\n+a\r\n", strings.Repeat("*1\r\n", 10002) + "+end\r\n", "+" + strings.Repeat("x", 1<<20) + "\r\n"} {
		if _, _, err := redis.ParseRESP([]byte(input)); err == nil {
			t.Fatalf("malformed frame accepted: %.100q", input)
		}
	}
}

func FuzzRESP(f *testing.F) {
	for _, input := range []string{"$3\r\na\x00b\r\n", "*-1\r\n", "%1\r\n+a\r\n:3\r\n", "!7\r\nERR bad\r\n", "=7\r\ntxt:abc\r\n", ":9223372036854775807\r\n", "(123456789012345678901\r\n"} {
		f.Add([]byte(input))
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > 65536 {
			t.Skip()
		}
		reply, n, err := redis.ParseRESP(input)
		if err != nil {
			return
		}
		if n < 1 || n > len(input) {
			t.Fatalf("invalid consumption: %d", n)
		}
		wire := reply.AppendRESP(nil, 3)
		again, used, err := redis.ParseRESP(wire)
		if err != nil || used != len(wire) {
			t.Fatalf("rendered reply cannot be read: %v", err)
		}
		if !bytes.Equal(wire, again.AppendRESP(nil, 3)) {
			t.Fatalf("unstable reply encoding")
		}
	})
}
