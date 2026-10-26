package lua

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func FuzzCompile(f *testing.F) {
	for _, link := range []string{" + x", " and x", " == x", ".a", "()", ":m()"} {
		f.Add("local x=1 return x" + strings.Repeat(link, 1000))
	}
	f.Add("local x=1 return x" + strings.Repeat(" + (x*x)", 300))
	f.Add("local x=1 return f(" + strings.Repeat("x,", 300) + "x)")
	for _, seed := range []string{"", "return +", "return '\\300'", "local x=x+1", "while true do end", "return 1..2", "return [[unterminated", "return \x1bLuaQ", strings.Repeat("(", 201), "local t={1,x=2} for k in pairs(t) do t[k]=nil end", "return load(function() return {} end)", "local co=coroutine.create(function() coroutine.yield(1) end) coroutine.resume(co)"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, source string) {
		if len(source) > 65536 {
			t.Skip()
		}
		_, err := Compile([]byte(source), "@fuzz")
		var failure *Error
		if errors.As(err, &failure) && failure.Internal {
			t.Fatalf("compiler panic for %q: %v", source, err)
		}
	})
}

func FuzzRun(f *testing.F) {
	for _, seed := range [][]byte{{}, {0}, {1, 7, 9}, {2}, {3}, {255, 0, 31}, {4, 3, 2, 1}, {5}, {6}, {7}, {8}, {9}} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 64 {
			t.Skip()
		}
		var source strings.Builder
		if len(data) > 0 {
			switch data[0] % 10 {
			case 1:
				source.WriteString("do local t={1,2,x=3} for k in pairs(t) do t[k]=nil end end ")
			case 2:
				source.WriteString("do local f,e=load(function() return {} end) if type(e)~='string' then error('bad reader') end end ")
			case 3:
				source.WriteString("do local co=coroutine.create(function() coroutine.yield(1) end) coroutine.resume(co) end ")
			case 4:
				source.WriteString("do local t={} for i=1,64 do t['k'..i]=i end for k in pairs(t) do t[k]=nil end end ")
			case 5:
				source.WriteString("do local t=setmetatable({}, {__index=function(t,k) return t[k] end}) pcall(function() return t.x end) end ")
			case 6:
				source.WriteString("do local function f(n) if n==0 then return 1 end return coroutine.wrap(f)(n-1) end pcall(f,200) end ")
			case 7:
				source.WriteString("do local function f() return xpcall(f,function(e) return e end) end f() end ")
			case 8:
				mode := []string{"k", "v", "kv"}[data[0]/10%3]
				fmt.Fprintf(&source, "do local w=setmetatable({}, {__mode='%s'}) for i=1,%d do w[{}]={} end collectgarbage() assert(next(w)==nil) end ", mode, 1+int(data[0]%16))
			case 9:
				source.WriteString("do local w=setmetatable({}, {__mode='k'}) do local k={} w[k]={k} end collectgarbage() assert(next(w)~=nil) for k in pairs(w) do w[k]=nil end collectgarbage() assert(next(w)==nil) end ")
			}
		}
		source.WriteString("local n=0 local t={} local function f(x) return x*2+1 end ")
		for index, value := range data {
			number := int(value) - 128
			switch value % 6 {
			case 0:
				fmt.Fprintf(&source, "n=n+(%d) ", number)
			case 1:
				fmt.Fprintf(&source, "n=n-(%d) ", number)
			case 2:
				fmt.Fprintf(&source, "t[%d]=n n=f(n) ", index+1)
			case 3:
				fmt.Fprintf(&source, "do local n=n+(%d) t[%d]=n end ", number, index+1)
			case 4:
				fmt.Fprintf(&source, "for i=1,%d do n=n+i end ", int(value)%7)
			case 5:
				fmt.Fprintf(&source, "n=n%%(%d) ", number)
			}
		}
		source.WriteString("return tostring(n),#t")
		proto, err := Compile([]byte(source.String()), "@fuzz")
		if err != nil {
			t.Fatal(err)
		}
		run := func() string {
			ticks := 0
			s := NewState(Options{HookEvery: 100, MaxString: 4096, Hook: func() error {
				ticks++
				if ticks > 100 {
					return errors.New("step limit")
				}
				return nil
			}})
			values, err := s.Call(s.Load(proto, nil))
			if err != nil {
				t.Fatal(err)
			}
			if len(s.threads) != 0 || len(s.main.frames) != 0 {
				t.Fatal("execution retained a coroutine or frame")
			}
			var output strings.Builder
			for _, value := range values {
				output.WriteString(value.String())
				output.WriteByte(',')
			}
			return output.String()
		}
		if left, right := run(), run(); left != right {
			t.Fatalf("non-deterministic run for %v: %q != %q", data, left, right)
		}
	})
}
