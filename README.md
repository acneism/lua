# lua

A Go library for Redis Lua scripting, under development.

The compatibility target is Redis 7.2.16 and its Lua 5.1 interpreter. The module
uses the Go standard library only. It requires Go 1.26.1 or later.

## Status

A3 completes reference compatibility for the interpreter core, base, coroutine, string, table, and math
libraries, immutable prototypes, Lua tables, metatables, closures, varargs,
tail calls, error locations, read-only tables, and an interruption hook.

The corpus has 4395 snippets. All execute through the original capture wrapper.
`testdata/pending.json` is empty. The reference verifier checks every recorded
answer against Redis.

The exp, log, pow, log10, sinh, cosh, and tanh implementations reproduce the
x86-64 FMA reference, including the power operator. Hosts can supply the seven
trigonometric functions through `Options.Math`. Without it, Go math provides
them; acos uses asin near the endpoints. The 36 exact trigonometric probes in
`testdata/libm.json` are reserved for the separate lua-libm module.

The redis package provides host callbacks, RESP2 and RESP3 replies, `redis.setresp`,
the EVAL sandbox, EVAL/EVALSHA, and SCRIPT methods. It runs Redis's error handler
verbatim, so error replies match Redis's. Numbers in commands and double replies
use a port of Redis's fpconv, whose Grisu2 output is not always the shortest form.
It includes the bit, struct, cjson and cmsgpack libraries, FUNCTION and FCALL, and
DUMP and RESTORE payloads that Redis accepts in both directions.

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

Measured on 2026-11-02 at 8e95f84 on GitHub Actions runners: 9 runs, the median
per processor. The worst figure is 1.39 of 1.5.

| Scenario | AMD EPYC 7763 (4 runs) | AMD EPYC 9V74 (3 runs) | AMD EPYC 9V45 (1 run) | Intel Xeon Platinum 8370C (1 run) | Worst run |
| --- | ---: | ---: | ---: | ---: | ---: |
| Loop | 0.75 | 0.87 | 0.91 | 1.08 | 1.08 |
| Fib27 | 1.08 | 1.15 | 1.19 | 1.13 | 1.19 |
| LuaCalls | 1.01 | 1.13 | 1.00 | 1.15 | 1.15 |
| TableInsert | 0.22 | 0.16 | 0.22 | 0.24 | 0.26 |
| TableHash | 0.77 | 0.75 | 0.76 | 0.70 | 0.78 |
| Sort | 1.06 | 1.06 | 0.99 | 1.20 | 1.20 |
| GSub | 0.88 | 0.76 | 0.85 | 0.82 | 0.91 |
| StringConcat | 0.59 | 0.60 | 0.61 | 0.55 | 0.64 |
| Metamethod | 1.21 | 1.30 | 1.16 | 1.24 | 1.35 |
| ArithConst | 0.75 | 0.85 | 0.98 | 0.98 | 0.98 |
| TwoAccumulators | 0.72 | 0.88 | 0.98 | 0.98 | 0.98 |
| WhileLoop | 0.69 | 0.73 | 0.87 | 0.85 | 0.87 |
| Branches | 0.87 | 0.85 | 0.78 | 0.91 | 0.91 |
| CallTwoArgs | 0.95 | 1.08 | 1.01 | 1.16 | 1.16 |
| Upvalues | 0.87 | 0.95 | 0.98 | 1.11 | 1.11 |
| MethodCall | 1.11 | 1.16 | 1.18 | 1.21 | 1.21 |
| MultiReturn | 0.95 | 1.02 | 1.10 | 1.25 | 1.25 |
| Varargs | 1.12 | 1.15 | 1.31 | 1.26 | 1.31 |
| NativeCalls | 0.84 | 0.81 | 0.92 | 0.94 | 0.94 |
| ClosureCreate | 1.07 | 1.07 | 0.93 | 1.00 | 1.10 |
| ArrayReadWrite | 1.02 | 0.82 | 0.82 | 0.93 | 1.05 |
| FieldAccess | 0.66 | 0.71 | 0.84 | 0.77 | 0.84 |
| TableLibInsert | 0.50 | 0.48 | 0.42 | 0.47 | 0.51 |
| Ipairs | 0.33 | 0.32 | 0.35 | 0.38 | 0.38 |
| SmallTables | 1.16 | 1.03 | 1.20 | 1.38 | 1.38 |
| StringKeys | 0.72 | 0.76 | 0.88 | 0.81 | 0.88 |
| KeyBuild | 0.61 | 0.55 | 0.54 | 0.62 | 0.63 |
| StringMethods | 0.98 | 1.03 | 1.06 | 1.12 | 1.12 |
| StringFormat | 1.09 | 0.98 | 1.00 | 0.96 | 1.11 |
| Match | 1.07 | 1.00 | 1.07 | 1.29 | 1.29 |
| IndexInheritance | 1.03 | 1.28 | 1.17 | 1.34 | 1.34 |
| IndexFunction | 1.10 | 1.18 | 1.12 | 1.14 | 1.19 |
| NewIndexFunction | 1.02 | 1.19 | 1.07 | 1.18 | 1.20 |
| CompareMetamethod | 0.87 | 1.10 | 0.97 | 1.09 | 1.11 |
| CallMetamethod | 0.93 | 1.28 | 1.18 | 1.39 | 1.39 |
| PCall | 0.95 | 0.95 | 1.02 | 1.06 | 1.06 |
| Coroutine | 0.97 | 1.01 | 1.00 | 1.26 | 1.26 |

The sources of all scenarios are in
[internal/scenario/scenario.go](internal/scenario/scenario.go).

The ratio varies with the processor and between runs on the same processor
type, because Redis and Go react differently to each machine. Fib27 varies most:
its median is about 1.2, but on a725f66, whose call path is close to the measured
commit, it reached 1.51 and 1.58 in 2 of 15 runs, with the same binary at 1.17 in
others. See [benchmarks](docs/benchmarks.md) for the method and history.

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
