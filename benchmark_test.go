package lua

import "testing"

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
	benchmarkScript(b, "local function f(x) return x+1 end local n=0 for i=1,10000 do n=f(n) end return n")
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
	benchmarkScript(b, "local t=setmetatable({}, {__add=function(a,b) return b+1 end}) local n=0 for i=1,10000 do n=t+n end return n")
}

func TestLuaCallsDoNotAllocatePerCall(t *testing.T) {
	s := NewState(Options{})
	measure := func(iterations string) float64 {
		fn := s.Load(compileTest(t, "local function f(x) return x+1 end local n=0 for i=1,"+iterations+" do n=f(n) end return n"), nil)
		var callError error
		allocations := testing.AllocsPerRun(5, func() { _, callError = s.Call(fn) })
		if callError != nil {
			t.Fatal(callError)
		}
		return allocations
	}
	small, large := measure("10"), measure("1000")
	if large > small {
		t.Fatalf("allocations grew with Lua calls: %g -> %g", small, large)
	}
}
