package redis_test

import (
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/acneism/lua/redis"
)

var longScripts = map[string]string{
	"loop":      `while true do end`,
	"calls":     `local function f() return 1 end while true do f() end`,
	"pcall":     `while true do pcall(function() while true do end end) end`,
	"coroutine": `local c = coroutine.wrap(function() while true do coroutine.yield() end end) while true do c() end`,
	"rep":       `while true do local s = string.rep('ab', 2e7) end`,
	"gsub":      `local s = string.rep('ab ', 2e6) while true do string.gsub(s, '%a+', 'x') end`,
	"pattern":   `local s = string.rep('a', 5000) while true do string.find(s, string.rep('a?', 5000) .. 'b') end`,
	"sort":      `local t = {} for i = 1, 2e5 do t[i] = 2e5 - i end while true do table.sort(t, function(a, b) return a > b end) end`,
	"concat":    `local t = {} for i = 1, 1e6 do t[i] = 'x' end while true do table.concat(t) end`,
	"load":      `local function reader() return 'x = 1 ' end while true do loadstring(string.rep('local a = 1 ', 1e5)) end`,
	"cjson":     `local t = {} for i = 1, 1e6 do t[i] = i end while true do cjson.encode(t) end`,
	"decode":    `local s = '[' .. string.rep('1,', 1e6) .. '1]' while true do cjson.decode(s) end`,
	"msgpack":   `local t = {} for i = 1, 1e6 do t[i] = i end while true do cmsgpack.pack(t) end`,
	"unpack":    `local t = {} for i = 1, 1e6 do t[i] = i end local s = cmsgpack.pack(t) while true do cmsgpack.unpack(s) end`,
}

func TestKillInterruptsEveryLongOperation(t *testing.T) {
	for name, source := range longScripts {
		t.Run(name, func(t *testing.T) {
			before := runtime.NumGoroutine()
			engine := redis.NewEngine(redis.Config{})
			finished := make(chan redis.Reply, 1)
			go func() { finished <- engine.Eval([]byte(source), nil, nil, redis.Run{}) }()
			for len(engine.Running()) == 0 {
				time.Sleep(time.Millisecond)
			}
			time.Sleep(100 * time.Millisecond)
			if reply := engine.Kill(redis.Script); reply.Type != redis.StatusReply {
				t.Fatal(reply)
			}
			started := time.Now()
			select {
			case reply := <-finished:
				if !strings.HasPrefix(reply.Str, "ERR Script killed by user with SCRIPT KILL...") {
					t.Fatal(reply)
				}
				t.Log(time.Since(started))
			case <-time.After(5 * time.Second):
				t.Fatal("not interrupted")
			}
			if reply := engine.Eval([]byte("return 7"), nil, nil, redis.Run{}); reply.Int != 7 {
				t.Fatal(reply)
			}
			if len(engine.Running()) != 0 {
				t.Fatal("a run was left behind")
			}
			for deadline := time.Now().Add(5 * time.Second); runtime.NumGoroutine() > before; time.Sleep(time.Millisecond) {
				if time.Now().After(deadline) {
					t.Fatalf("goroutines %d, before %d", runtime.NumGoroutine(), before)
				}
			}
		})
	}
}

func TestKillKinds(t *testing.T) {
	engine := redis.NewEngine(redis.Config{})
	if reply := engine.Kill(redis.Script); reply.Str != "NOTBUSY No scripts in execution right now." {
		t.Fatal(reply)
	}
	if reply := engine.FunctionLoad([]byte("#!lua name=l\nredis.register_function('spin', function() while true do end end)"), false); reply.Type == redis.ErrorReply {
		t.Fatal(reply)
	}
	finished := make(chan redis.Reply, 1)
	go func() { finished <- engine.FCall("spin", nil, nil, redis.Run{}) }()
	for len(engine.Running()) == 0 {
		time.Sleep(time.Millisecond)
	}
	if reply := engine.Kill(redis.Script); reply.Str != redis.Busy(redis.Function).Str {
		t.Fatal(reply)
	}
	stats := engine.FunctionStats()
	if stats.Pairs[0].Value.Type != redis.MapReply || stats.Pairs[0].Value.Pairs[0].Value.Str != "spin" {
		t.Fatal(stats)
	}
	if reply := engine.Kill(redis.Function); reply.Type != redis.StatusReply {
		t.Fatal(reply)
	}
	if reply := <-finished; !strings.HasPrefix(reply.Str, "ERR Script killed by user with SCRIPT KILL... script: spin, on @user_function:2.") {
		t.Fatal(reply)
	}
}

func TestMemoryLimitInterrupts(t *testing.T) {
	for name, source := range map[string]string{
		"strings": `local t = {} local i = 0 while true do i = i + 1 t[i] = string.rep('x', 1000) .. i end`,
		"tables":  `local t = {} while true do t = {t} end`,
		"cjson":   `local s = '"' .. string.rep('x', 1e5) .. '"' local t = {} while true do t[#t + 1] = cjson.decode(s) end`,
		"pack":    `local s = cmsgpack.pack(string.rep('x', 1e5)) local t = {} while true do t[#t + 1] = cmsgpack.unpack(s) end`,
		"pcall":   `while true do pcall(function() local t = {} while true do t[#t + 1] = string.rep('x', 1000) .. #t end end) end`,
	} {
		t.Run(name, func(t *testing.T) {
			engine := redis.NewEngine(redis.Config{MemoryLimit: 16 << 20})
			reply := engine.Eval([]byte(source), nil, nil, redis.Run{})
			if reply.Type != redis.ErrorReply || !strings.HasPrefix(reply.Str, "ERR script exceeded the memory limit script: ") {
				t.Fatal(reply)
			}
			if reply := engine.Eval([]byte("return 7"), nil, nil, redis.Run{}); reply.Int != 7 {
				t.Fatal(reply)
			}
		})
	}
}

func TestKillDifferential(t *testing.T) {
	reference, control := differentialClients(t)
	defer func() { _, _ = control.Do("CONFIG", "SET", "busy-reply-threshold", "5000") }()
	if reply, err := control.Do("CONFIG", "SET", "busy-reply-threshold", "50"); err != nil || reply.Type == redis.ErrorReply {
		t.Fatal(err, reply)
	}
	code := "#!lua name=spinlib\n\nredis.register_function('spin', function()\n  while true do end\nend)"
	if _, err := control.Do("FUNCTION", "FLUSH"); err != nil {
		t.Fatal(err)
	}
	if reply, err := control.Do("FUNCTION", "LOAD", code); err != nil || reply.Type == redis.ErrorReply {
		t.Fatal(err, reply)
	}
	engine := redis.NewEngine(redis.Config{})
	if reply := engine.FunctionLoad([]byte(code), false); reply.Type == redis.ErrorReply {
		t.Fatal(reply)
	}
	source := "local x = 1\nwhile true do end"
	for _, item := range []struct {
		name    string
		kind    redis.Kind
		command []string
		kill    []string
		wrong   []string
	}{
		{"eval", redis.Script, []string{"EVAL", source, "0"}, []string{"SCRIPT", "KILL"}, []string{"FUNCTION", "KILL"}},
		{"fcall", redis.Function, []string{"FCALL", "spin", "0"}, []string{"FUNCTION", "KILL"}, []string{"SCRIPT", "KILL"}},
	} {
		t.Run(item.name, func(t *testing.T) {
			done := make(chan redis.Reply, 1)
			go func() {
				reply, err := reference.Do(item.command...)
				if err != nil {
					t.Error(err)
				}
				done <- reply
			}()
			var busy redis.Reply
			for {
				reply, err := control.Do("PING")
				if err != nil {
					t.Fatal(err)
				}
				if reply.Type == redis.ErrorReply {
					busy = reply
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			wrong, err := control.Do(item.wrong...)
			if err != nil {
				t.Fatal(err)
			}
			stats, err := control.Do("FUNCTION", "STATS")
			if err != nil {
				t.Fatal(err)
			}
			if reply, err := control.Do(item.kill...); err != nil || reply.Type != redis.StatusReply {
				t.Fatal(err, reply)
			}
			want := <-done
			argv := make([][]byte, len(item.command))
			for index, arg := range item.command {
				argv[index] = []byte(arg)
			}
			finished := make(chan redis.Reply, 1)
			go func() { finished <- engine.Command(argv, redis.Run{}) }()
			for len(engine.Running()) == 0 {
				time.Sleep(time.Millisecond)
			}
			if got := redis.Busy(item.kind); got.Str != busy.Str {
				t.Errorf("busy: want %q, got %q", busy.Str, got.Str)
			}
			wrongKind := redis.Function
			if item.kind == redis.Function {
				wrongKind = redis.Script
			}
			if got := engine.Kill(wrongKind); got.Str != wrong.Str {
				t.Errorf("wrong kill: want %q, got %q", wrong.Str, got.Str)
			}
			if got := engine.FunctionStats(); item.kind == redis.Script && got.Str != stats.Str {
				t.Errorf("stats: want %q, got %q", stats.Str, got.Str)
			}
			time.Sleep(20 * time.Millisecond)
			if got := engine.Kill(item.kind); got.Type != redis.StatusReply {
				t.Fatal(got)
			}
			got := <-finished
			if want.Str != got.Str {
				t.Errorf("killed: want %q, got %q", want.Str, got.Str)
			}
			t.Log(strconv.Quote(want.Str))
		})
	}
}
