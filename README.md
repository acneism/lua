# lua

A Go library for Redis Lua scripting.

The compatibility target is Redis 7.2.16 and its Lua 5.1 interpreter. The module
uses the Go standard library only. It requires Go 1.26.1 or later.

## Status

Version 0.1.0 covers the interpreter and the Redis scripting environment.

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
It includes the bit, struct, cjson and cmsgpack libraries, FUNCTION and FCALL, DUMP and
RESTORE payloads that Redis accepts in both directions, KILL, BUSY and a memory limit.

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
time: the median of five alternating runs in a process, then the median of five
fresh processes; below 1 means faster than Redis. The limit for every scenario
is 1.5.

Measured on 2026-11-03 at v0.1.0 (3255aed) on GitHub Actions runners: 24 runs of
five fresh processes each, the median per processor. The last column is the worst
run of any processor. One value is over the limit: HashFill 1.57 on one Xeon
6973P runner, in all five of its processes, against 1.31 on another runner of that
type. Next come ArithConst 1.46 and SmallTables 1.41.

| Scenario | AMD EPYC 7763 (9 runs) | AMD EPYC 9V74 (7 runs) | AMD EPYC 9V45 (3 runs) | Intel Xeon 6973P-C (2 runs) | Intel Xeon 6975P-C (1 run) | Intel Xeon Platinum 8370C (1 run) | Intel Xeon Platinum 8573C (1 run) | Worst run |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Loop | 0.75 | 0.85 | 0.97 | 1.00 | 0.99 | 1.05 | 0.95 | 1.05 |
| Fib27 | 1.08 | 1.14 | 1.16 | 1.13 | 1.13 | 1.18 | 1.11 | 1.18 |
| LuaCalls | 1.00 | 1.14 | 1.02 | 1.10 | 1.09 | 1.16 | 1.06 | 1.16 |
| TableInsert | 0.23 | 0.17 | 0.23 | 0.22 | 0.22 | 0.23 | 0.22 | 0.24 |
| TableHash | 0.77 | 0.74 | 0.69 | 0.69 | 0.67 | 0.69 | 0.69 | 0.80 |
| Sort | 0.93 | 0.95 | 0.89 | 1.08 | 1.08 | 1.18 | 1.07 | 1.18 |
| GSub | 0.81 | 0.70 | 0.75 | 0.83 | 0.82 | 0.79 | 0.81 | 0.83 |
| StringConcat | 0.58 | 0.62 | 0.61 | 0.60 | 0.59 | 0.60 | 0.56 | 0.64 |
| Metamethod | 1.19 | 1.30 | 1.17 | 1.17 | 1.19 | 1.20 | 1.16 | 1.31 |
| ArithConst | 0.75 | 0.85 | 1.14 | 1.01 | 1.01 | 0.98 | 0.98 | 1.46 |
| TwoAccumulators | 0.74 | 0.87 | 0.99 | 0.92 | 0.92 | 0.95 | 0.92 | 1.00 |
| WhileLoop | 0.69 | 0.71 | 0.90 | 0.85 | 0.84 | 0.83 | 0.82 | 0.90 |
| Branches | 0.90 | 0.85 | 0.82 | 0.99 | 0.99 | 0.92 | 0.98 | 1.00 |
| CallTwoArgs | 0.92 | 1.01 | 1.00 | 1.00 | 0.99 | 1.11 | 0.94 | 1.11 |
| Upvalues | 0.86 | 0.95 | 0.99 | 1.07 | 1.07 | 1.10 | 1.06 | 1.10 |
| MethodCall | 1.07 | 1.17 | 1.15 | 1.12 | 1.11 | 1.21 | 1.08 | 1.22 |
| MultiReturn | 0.96 | 1.02 | 1.10 | 1.01 | 1.01 | 1.25 | 1.00 | 1.25 |
| Varargs | 1.12 | 1.16 | 1.29 | 1.10 | 1.09 | 1.27 | 1.03 | 1.30 |
| NativeCalls | 0.85 | 0.83 | 0.90 | 0.94 | 0.95 | 0.88 | 0.88 | 0.95 |
| ClosureCreate | 1.05 | 1.04 | 0.99 | 0.93 | 0.92 | 1.08 | 0.79 | 1.10 |
| ArrayReadWrite | 1.00 | 0.86 | 0.83 | 0.84 | 0.90 | 0.85 | 0.81 | 1.05 |
| FieldAccess | 0.66 | 0.70 | 0.84 | 0.83 | 0.82 | 0.82 | 0.79 | 0.84 |
| TableLibInsert | 0.47 | 0.41 | 0.43 | 0.37 | 0.37 | 0.48 | 0.38 | 0.49 |
| Ipairs | 0.33 | 0.32 | 0.34 | 0.40 | 0.39 | 0.39 | 0.36 | 0.40 |
| SmallTables | 1.17 | 1.13 | 1.14 | 1.21 | 1.15 | 1.41 | 1.13 | 1.41 |
| StringKeys | 0.71 | 0.75 | 0.83 | 0.81 | 0.80 | 0.76 | 0.83 | 0.83 |
| KeyBuild | 0.61 | 0.58 | 0.54 | 0.54 | 0.52 | 0.64 | 0.52 | 0.64 |
| StringMethods | 0.98 | 1.04 | 1.06 | 1.09 | 1.08 | 1.10 | 1.03 | 1.10 |
| StringFormat | 1.04 | 0.96 | 1.01 | 0.99 | 0.97 | 0.99 | 0.98 | 1.06 |
| Match | 1.09 | 1.02 | 1.07 | 1.11 | 1.07 | 1.26 | 1.09 | 1.26 |
| IndexInheritance | 1.01 | 1.27 | 1.19 | 1.16 | 1.16 | 1.20 | 1.09 | 1.33 |
| IndexFunction | 1.10 | 1.17 | 1.12 | 1.15 | 1.14 | 1.16 | 1.13 | 1.19 |
| NewIndexFunction | 1.03 | 1.17 | 1.07 | 1.15 | 1.15 | 1.18 | 1.08 | 1.18 |
| CompareMetamethod | 0.87 | 1.08 | 1.05 | 1.06 | 1.06 | 1.10 | 0.96 | 1.10 |
| CallMetamethod | 0.91 | 1.23 | 1.14 | 1.33 | 1.32 | 1.30 | 1.34 | 1.34 |
| PCall | 1.03 | 0.93 | 1.01 | 0.97 | 0.97 | 1.10 | 0.95 | 1.10 |
| Coroutine | 0.94 | 1.02 | 1.02 | 1.01 | 1.02 | 1.20 | 0.93 | 1.20 |
| CJSONRoundTrip | 1.06 | 1.03 | 1.10 | 1.03 | 1.02 | 1.08 | 1.08 | 1.11 |
| CMsgpackRoundTrip | 1.15 | 1.19 | 1.17 | 1.20 | 1.19 | 1.25 | 1.19 | 1.25 |
| BitOps | 0.90 | 1.06 | 1.00 | 0.99 | 0.98 | 1.18 | 0.92 | 1.18 |
| RedisCall | 0.93 | 0.89 | 1.00 | 1.02 | 0.99 | 0.98 | 0.95 | 1.03 |
| HashFill | 1.01 | 0.96 | 0.94 | 1.44 | 1.21 | 1.14 | 1.27 | 1.57 |
| StructRoundTrip | 0.97 | 0.96 | 0.90 | 1.08 | 1.05 | 1.17 | 1.10 | 1.17 |

The sources of all scenarios are in
[internal/scenario/scenario.go](internal/scenario/scenario.go).

The ratio varies with the processor and between runs on the same processor
type, because Redis and Go react differently to each machine. A single process
can also be slower as a whole, which is why the limit applies to the median of
several processes: on a725f66 Fib27 reached 1.51 and 1.58 in 2 of 15 runs, with
the same binary at 1.17 in others. The median does not remove this completely.
At v0.1.0 ArithConst was usually 0.75 on EPYC 7763, but single processes reached
1.5, and up to 2.23 on other processors. Later commits removed that slow mode. See
[benchmarks](docs/benchmarks.md) for the method, the cause and the history.

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
