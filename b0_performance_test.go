package lua

import "testing"

func BenchmarkZZNative(b *testing.B) {
	benchmarkScript(b, "local f, n = type, 0 for i = 1, 100000 do if f(i) == 'number' then n = n + 1 end end return n")
}

func BenchmarkZZPcall(b *testing.B) {
	benchmarkScript(b, "local f = function(x) return x end for i = 1, 100000 do pcall(f, i) end")
}

func BenchmarkZZIndexMeta(b *testing.B) {
	benchmarkScript(b, "local t = setmetatable({}, {__index = rawget}) local n = 0 for i = 1, 100000 do if t[i] == nil then n = n + 1 end end return n")
}

func BenchmarkZZCoroutine(b *testing.B) {
	benchmarkScript(b, "local co = coroutine.wrap(function() while true do coroutine.yield(1) end end) for i = 1, 100000 do co() end")
}
