package lua

import (
	"strings"
	"testing"
)

func benchmarkScript(b *testing.B, source string) {
	proto, err := Compile([]byte(source), "@benchmark")
	if err != nil {
		b.Fatal(err)
	}
	s := NewState(Options{})
	fn := s.Load(proto, nil)
	if _, err := s.Call(fn); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := s.Call(fn); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLoop(b *testing.B) {
	benchmarkScript(b, "local s=0 for i=1,10000000 do s=s+i end return s")
}

func BenchmarkFib27(b *testing.B) {
	benchmarkScript(b, "local function fib(n) if n<2 then return n end return fib(n-1)+fib(n-2) end return fib(27)")
}

func BenchmarkLuaCalls(b *testing.B) {
	benchmarkScript(b, "local function f(x) return x+1 end local n=0 for i=1,1000000 do n=f(n) end return n")
}

func BenchmarkTableInsert(b *testing.B) {
	benchmarkScript(b, "local t={} for i=1,1000000 do t[#t+1]=i end return #t")
}

func BenchmarkTableHash(b *testing.B) {
	benchmarkScript(b, "local t={} for i=1,100000 do t['k'..i]=i end local n=0 for k,v in pairs(t) do n=n+v end return n")
}

func BenchmarkNumberValue(b *testing.B) {
	value := Number(0)
	b.ReportAllocs()
	for b.Loop() {
		value = Number(value.Float() + 1)
	}
	if value.Float() == 0 {
		b.Fatal("number was not updated")
	}
}

func BenchmarkSort(b *testing.B) {
	benchmarkScript(b, "local t={} for i=1,100000 do t[i]=100001-i end table.sort(t) return t[1]+t[100000]")
}

func BenchmarkGSub(b *testing.B) {
	benchmarkScript(b, "return #string.gsub(string.rep('abc ',262144),'%a+','x')")
}

func BenchmarkStringConcat(b *testing.B) {
	benchmarkScript(b, "local t={} for i=1,100000 do t[i]=tostring(i) end return #table.concat(t)")
}

func BenchmarkMetamethod(b *testing.B) {
	benchmarkScript(b, "local t=setmetatable({}, {__add=function(a,b) return b+1 end}) local n=0 for i=1,1000000 do n=t+n end return n")
}

func BenchmarkGoCall(b *testing.B) {
	benchmarkScript(b, "local f, n = type, 0 for i = 1, 100000 do if f(i) == 'number' then n = n + 1 end end return n")
}

func BenchmarkPCall(b *testing.B) {
	benchmarkScript(b, "local f = function(x) return x end for i = 1, 100000 do pcall(f, i) end")
}

func BenchmarkGoIndex(b *testing.B) {
	benchmarkScript(b, "local t = setmetatable({}, {__index = rawget}) local n = 0 for i = 1, 100000 do if t[i] == nil then n = n + 1 end end return n")
}

func BenchmarkCoroutine(b *testing.B) {
	benchmarkScript(b, "local co = coroutine.wrap(function() while true do coroutine.yield(1) end end) for i = 1, 100000 do co() end")
}

func TestSteadyStateDoesNotAllocate(t *testing.T) {
	for _, item := range []struct{ name, source string }{
		{"loop", "local s=0 for i=1,N do s=s+i end return s"},
		{"lua call", "local function f(x) return x+1 end local n=0 for i=1,N do n=f(n) end return n"},
		{"go call", "local n=0 for i=1,N do if type(i)=='number' then n=n+math.max(i%7,3) end end return n"},
		{"pcall", "local n=0 for i=1,N do local ok,v=pcall(tonumber,i) if ok then n=n+v end end return n"},
		{"ipairs", "local t={1,2,3,4,5,6,7,8} local s=0 for r=1,N do for _,v in ipairs(t) do s=s+v end end return s"},
		{"pairs", "local t={a=1,b=2,c=3} local s=0 for r=1,N do for k,v in pairs(t) do s=s+v end end return s"},
		{"next", "local t={a=1,b=2} local s=0 for r=1,N do local k,v=next(t) s=s+v end return s"},
		{"resume and yield", "local co=coroutine.create(function() while true do coroutine.yield(1) end end) local s=0 for i=1,N do local ok,v=coroutine.resume(co) s=s+v end return s"},
		{"wrap", "local co=coroutine.wrap(function() while true do coroutine.yield(1) end end) local s=0 for i=1,N do s=s+co() end return s"},
	} {
		t.Run(item.name, func(t *testing.T) {
			s := NewState(Options{})
			measure := func(iterations string) float64 {
				fn := s.Load(compileTest(t, strings.ReplaceAll(item.source, "N", iterations)), nil)
				var callError error
				allocations := testing.AllocsPerRun(5, func() { _, callError = s.Call(fn) })
				if callError != nil {
					t.Fatal(callError)
				}
				return allocations
			}
			small, large := measure("10"), measure("1000")
			if large > small {
				t.Fatalf("allocations grew with iterations: %g -> %g", small, large)
			}
		})
	}
}
