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

const fuzzJSONSource = `local a = {pcall(cjson.decode, ARGV[1])}
local b = a[1] and {pcall(cjson.encode, a[2])} or a
local c = {pcall(cmsgpack.unpack, ARGV[1])}
local d = c[1] and {pcall(cmsgpack.pack, c[2])} or c
local e = {pcall(cmsgpack.unpack_one, ARGV[1], 1)}
local f = {pcall(cmsgpack.unpack_limit, ARGV[1], 2, 1)}
return {tostring(a[1]), tostring(b[1]), tostring(c[1]), tostring(d[1]), tostring(e[1]), tostring(f[1])}`

func FuzzExtensions(f *testing.F) {
	for _, seed := range []string{
		`{"a":[1,2.5,null,true,"x\u00e9\ud83d\ude00"]}`,
		`[[[[1]]]]`,
		`nan`,
		`-0x1p4`,
		`1e400`,
		`"\ud83d"`,
		"\x93\x01\x02\x03",
		"\x81\xa1a\x01",
		"\xcb\x3f\xf8\x00\x00\x00\x00\x00\x00",
		"\x81\xc0\x01",
		"\xdd\xff\xff\xff\xff",
		"\xdb\xff\xff\xff\xff",
	} {
		f.Add(seed)
	}
	engine := redis.NewEngine(redis.Config{})
	f.Fuzz(func(t *testing.T, input string) {
		reply := engine.Eval([]byte(fuzzJSONSource), nil, [][]byte{[]byte(input)}, redis.Run{})
		if reply.Type == redis.ErrorReply && strings.Contains(reply.Str, "internal") {
			t.Fatalf("internal error: %s", reply.Str)
		}
	})
}
