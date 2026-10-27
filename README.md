# lua

A Go library for Redis Lua scripting, under development.

The compatibility target is Redis 7.2.16 and its Lua 5.1 interpreter. The module
uses the Go standard library only. It requires Go 1.26.1 or later.

## Status

A3 completes reference compatibility for the interpreter core, base, coroutine, string, table, and math
libraries, immutable prototypes, Lua tables, metatables, closures, varargs,
tail calls, error locations, read-only tables, and an interruption hook.

The corpus has 4283 snippets. All execute through the original capture wrapper.
`testdata/pending.json` is empty. The reference verifier checks every recorded
answer against Redis.

The exp, log, pow, log10, sinh, cosh, and tanh implementations reproduce the
x86-64 FMA reference, including the power operator. Hosts can supply the seven
trigonometric functions through `Options.Math`. Without it, Go math provides
them; acos uses asin near the endpoints. The 36 exact trigonometric probes in
`testdata/libm.json` are reserved for the separate lua-libm module.

The redis package provides host callbacks, RESP2 replies, the EVAL sandbox,
EVAL/EVALSHA, and SCRIPT methods. It runs Redis's error handler verbatim, so
error replies match Redis's. RESP3 replies, FUNCTION, and the Redis extension
libraries belong to later stages.

`State.Frame` exposes Lua, native, and lost tail-call levels. `CallWithHandler`
runs an error handler at the original failure point before unwinding; xpcall
uses the same behavior. See [error handling](docs/adr/0009-error-handlers.md).

Explicit collectgarbage calls clear weak tables using Lua object reachability.
Hosts register external roots with `State.Retain` and call its release function
when finished. See [weak tables](docs/adr/0010-weak-tables.md).

The interpreter uses 24-byte values, packed instructions, cached register
windows, and reusable Lua call frames. See the [performance decision](docs/adr/0007-vm-performance.md)
for the implementation and its Redis comparisons.

The compiler ports Redis's Lua 5.1 parser and code generator. `string.dump`
reproduces its x86-64 binary format, including debug information, on every
platform. Binary chunks remain unsupported as input. See the
[compiler decision](docs/adr/0011-redis-compiler.md).

## Performance

Each scenario runs as a Go benchmark in process and as EVAL in Redis 7.2.16
(jemalloc build) on the same machine. The figure is Go time divided by Redis
time, the median of five alternating runs; below 1 means faster than Redis.
The limit for every scenario is 1.5.

Measured on 2026-10-27 at 4643bb2 on GitHub Actions runners: 12 runs, the
median per processor.

| Scenario | AMD EPYC 7763 (7 runs) | AMD EPYC 9V45 (4 runs) | Intel Xeon Platinum 8573C (1 run) | Worst run |
| --- | ---: | ---: | ---: | ---: |
| Loop | 0.92 | 1.19 | 1.00 | 1.31 |
| Fib27 | 1.18 | 1.19 | 1.36 | 1.36 |
| LuaCalls | 1.29 | 1.23 | 1.33 | 1.33 |
| TableInsert | 0.70 | 0.63 | 0.95 | 0.95 |
| TableHash | 0.93 | 0.92 | 0.86 | 0.99 |
| Sort | 1.03 | 0.97 | 1.06 | 1.09 |
| GSub | 0.85 | 0.83 | 0.85 | 0.90 |
| StringConcat | 0.74 | 0.72 | 0.65 | 0.83 |
| Metamethod | 1.30 | 1.19 | 1.23 | 1.32 |

The scenarios:

- Loop: a numeric for loop of 10 million additions.
- Fib27: recursive Fibonacci of 27.
- LuaCalls: one million calls of a one-line Lua function.
- TableInsert: one million appends with `t[#t+1]`.
- TableHash: 100,000 string keys, then a pairs traversal.
- Sort: table.sort of 100,000 numbers in reverse order.
- GSub: string.gsub over 1 MiB of words.
- StringConcat: tostring of 100,000 numbers and table.concat.
- Metamethod: one million `__add` calls to a Lua function.

The ratio varies with the processor and between runs on the same processor
type, because Redis and Go react differently to each machine. Loop's 9V45
median comes from runs where two measurements in the same run differed by up
to 25%. See [benchmarks](docs/benchmarks.md) for the method and history.

## Interpreter example

```go
package main

import (
    "fmt"

    "github.com/acneism/lua"
)

func main() {
    proto, err := lua.Compile([]byte("return ... + 1"), "@example")
    if err != nil {
        panic(err)
    }
    state := lua.NewState(lua.Options{})
    values, err := state.Call(state.Load(proto, nil), lua.Number(41))
    if err != nil {
        panic(err)
    }
    fmt.Println(values[0].Float())
}
```

Compile once and load the prototype into separate states. A state can be reused
between calls and must not be called concurrently. Allocate host tables and
functions through the state to give objects stable identities.

## Development

```sh
go test ./...
go vet ./...
go test -race ./...
go run ./internal/cmd/capture -addr 127.0.0.1:6390 -verify testdata/snippets.json
```

External math modules can run this corpus through the test-only
`github.com/acneism/lua/luatest` package. Pass fixture paths and host options to
`luatest.RunCorpus(t, files, options)`. See [reference tests](docs/reference.md).

See [CONTRIBUTING.md](CONTRIBUTING.md), [architecture](docs/architecture.md),
[decisions](docs/adr/README.md), [compatibility](docs/compatibility.md), and
[benchmarks](docs/benchmarks.md).

## License

Apache-2.0, with upstream notices for ported Lua and Redis behavior.
See [LICENSE](LICENSE), [NOTICE](NOTICE), and [LICENSES](LICENSES).
