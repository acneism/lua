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

Measured on 2026-11-05 at 4efd6e0 on GitHub Actions runners: 24 runs of five
fresh processes each, the median per processor. The last column is the worst run
of any processor. No value is over the limit; the highest is CallMetamethod 1.34
on Xeon Platinum 8573C. Two of the 5160 single processes were over 1.5:
IndexFunction 1.60 and 1.72, each the first of five processes on an EPYC 9V74
runner. This run drew no Xeon 6973P or 6975P runners. At v0.1.0 HashFill was 1.57
on one 6973P runner.

| Scenario | AMD EPYC 7763 (8 runs) | AMD EPYC 9V74 (5 runs) | AMD EPYC 9V45 (5 runs) | Intel Xeon Platinum 8370C (2 runs) | Intel Xeon Platinum 8573C (4 runs) | Worst run |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Loop | 0.75 | 0.85 | 0.92 | 1.07 | 0.96 | 1.07 |
| Fib27 | 1.09 | 1.15 | 1.15 | 1.15 | 1.11 | 1.18 |
| LuaCalls | 1.01 | 1.15 | 1.05 | 1.18 | 1.06 | 1.22 |
| TableInsert | 0.23 | 0.17 | 0.21 | 0.23 | 0.21 | 0.24 |
| TableHash | 0.76 | 0.70 | 0.73 | 0.67 | 0.70 | 0.79 |
| Sort | 0.93 | 0.95 | 0.89 | 1.18 | 1.07 | 1.18 |
| GSub | 0.83 | 0.72 | 0.76 | 0.78 | 0.83 | 0.85 |
| StringConcat | 0.56 | 0.62 | 0.62 | 0.56 | 0.56 | 0.65 |
| Metamethod | 1.19 | 1.30 | 1.20 | 1.19 | 1.16 | 1.30 |
| ArithConst | 0.75 | 0.84 | 1.03 | 0.96 | 0.98 | 1.04 |
| TwoAccumulators | 0.75 | 0.88 | 0.99 | 0.97 | 0.91 | 0.99 |
| WhileLoop | 0.66 | 0.69 | 0.88 | 0.85 | 0.83 | 0.90 |
| Branches | 0.89 | 0.85 | 0.81 | 0.92 | 0.99 | 0.99 |
| CallTwoArgs | 0.92 | 1.01 | 0.97 | 1.09 | 0.93 | 1.09 |
| Upvalues | 0.87 | 0.95 | 1.00 | 1.10 | 1.04 | 1.11 |
| MethodCall | 1.07 | 1.14 | 1.15 | 1.18 | 1.07 | 1.19 |
| MultiReturn | 0.96 | 1.02 | 1.09 | 1.26 | 1.00 | 1.26 |
| Varargs | 1.11 | 1.16 | 1.19 | 1.30 | 1.03 | 1.30 |
| NativeCalls | 0.84 | 0.82 | 0.91 | 0.89 | 0.88 | 0.91 |
| ClosureCreate | 1.07 | 1.03 | 0.99 | 1.00 | 0.88 | 1.16 |
| ArrayReadWrite | 1.01 | 0.87 | 0.84 | 0.85 | 0.82 | 1.04 |
| FieldAccess | 0.66 | 0.70 | 0.77 | 0.79 | 0.77 | 0.80 |
| TableLibInsert | 0.48 | 0.42 | 0.44 | 0.49 | 0.38 | 0.49 |
| Ipairs | 0.33 | 0.32 | 0.34 | 0.38 | 0.36 | 0.38 |
| SmallTables | 1.15 | 1.13 | 1.12 | 1.21 | 1.16 | 1.27 |
| StringKeys | 0.72 | 0.76 | 0.83 | 0.80 | 0.83 | 0.84 |
| KeyBuild | 0.59 | 0.59 | 0.53 | 0.61 | 0.51 | 0.61 |
| StringMethods | 0.98 | 1.03 | 1.05 | 1.10 | 1.01 | 1.10 |
| StringFormat | 1.04 | 0.94 | 0.95 | 0.98 | 1.00 | 1.07 |
| Match | 1.11 | 1.03 | 1.10 | 1.25 | 1.08 | 1.26 |
| IndexInheritance | 1.02 | 1.27 | 1.19 | 1.23 | 1.08 | 1.27 |
| IndexFunction | 1.11 | 1.18 | 1.13 | 1.20 | 1.12 | 1.22 |
| NewIndexFunction | 1.03 | 1.17 | 1.08 | 1.19 | 1.07 | 1.19 |
| CompareMetamethod | 0.87 | 1.07 | 1.05 | 1.06 | 0.96 | 1.08 |
| CallMetamethod | 0.92 | 1.22 | 1.19 | 1.32 | 1.33 | 1.34 |
| PCall | 0.96 | 0.93 | 1.00 | 1.10 | 0.96 | 1.10 |
| Coroutine | 0.94 | 1.00 | 1.04 | 1.19 | 0.93 | 1.20 |
| CJSONRoundTrip | 1.05 | 1.06 | 1.06 | 1.07 | 1.07 | 1.13 |
| CMsgpackRoundTrip | 1.15 | 1.19 | 1.13 | 1.24 | 1.20 | 1.25 |
| BitOps | 0.91 | 1.06 | 0.99 | 1.17 | 0.92 | 1.18 |
| RedisCall | 0.85 | 0.87 | 0.94 | 1.00 | 0.92 | 1.02 |
| HashFill | 1.01 | 0.95 | 0.95 | 1.16 | 1.25 | 1.26 |
| StructRoundTrip | 1.10 | 0.96 | 0.90 | 1.14 | 1.09 | 1.15 |

The sources of all scenarios are in
[internal/scenario/scenario.go](internal/scenario/scenario.go).

The ratio varies with the processor and between runs on the same processor
type, because Redis and Go react differently to each machine. A single process
can also be slower as a whole, which is why the limit applies to the median of
several processes. At v0.1.0 ArithConst had such a slow mode, up to 2.23 in a
single process, because a register and a code word shared their low address bits.
Registers and code now stay apart, see
[ADR 13](docs/adr/0013-register-and-code-placement.md). See
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
