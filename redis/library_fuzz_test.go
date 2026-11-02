package redis_test

import (
	"strings"
	"testing"

	"github.com/acneism/lua/redis"
)

const fuzzStructSource = `local f, d = ARGV[1], ARGV[2]
local n = tonumber(ARGV[3]) or 1
local a = {pcall(struct.size, f)}
local b = {pcall(struct.unpack, f, d)}
local c = {pcall(struct.pack, f, n, d, n, d, n)}
local e = {pcall(struct.unpack, f, d, n)}
local g = {pcall(bit.tohex, n, n)}
local h = {pcall(bit.lshift, n, n)}
return {tostring(a[1]), tostring(b[1]), tostring(c[1]), tostring(e[1]), tostring(g[1]), tostring(h[1])}`

func FuzzStruct(f *testing.F) {
	for _, seed := range []struct{ format, data, number string }{
		{">I2", "\x01\x02", "258"},
		{"<h", "\x01\x02", "513"},
		{"!4 b i4", "abcdefgh", "1"},
		{"c0", "", "2"},
		{"bc0", "\x02ab", "1"},
		{"s", "ab\x00", "0"},
		{"i33", "", "1e300"},
		{"!3", "", "nan"},
		{"99999999999", "", "-1"},
	} {
		f.Add(seed.format, seed.data, seed.number)
	}
	engine := redis.NewEngine(redis.Config{})
	f.Fuzz(func(t *testing.T, format, data, number string) {
		reply := engine.Eval([]byte(fuzzStructSource), nil, [][]byte{[]byte(format), []byte(data), []byte(number)}, redis.Run{})
		if reply.Type == redis.ErrorReply && strings.Contains(reply.Str, "internal") {
			t.Fatalf("internal error: %s", reply.Str)
		}
	})
}
