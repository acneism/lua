# lua

A Go library that runs Redis Lua scripts as Redis 7.2.16 does: the same Lua 5.1
interpreter, sandbox and libraries (`cjson`, `cmsgpack`, `struct`, `bit`), with
the same replies and error texts. It uses only the Go standard library and needs
Go 1.26.1 or later.

- Package `lua` is the interpreter. Its compiler ports Redis's Lua parser and code
  generator, and `string.dump` produces Redis's x86-64 binary chunks.
- Package `redis` gives a host EVAL, EVALSHA, SCRIPT, FUNCTION and FCALL with RESP2
  and RESP3 replies, KILL and a memory limit. The host runs `redis.call` through
  the `Host` interface.
- [lua-libm](https://github.com/acneism/lua-libm) supplies the seven trigonometric
  functions bit for bit through `Options.Math`. Without it they come from Go's
  `math`.

## Example

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
between calls and must not be called concurrently.

## Performance

Each scenario runs in Go and as EVAL in Redis 7.2.16 (jemalloc build) on the same
machine. The figure is Go time divided by Redis time: the median of five
alternating runs in a process, then the median of five fresh processes. Below 1
means faster than Redis.

Measured on 2026-11-05 at 4efd6e0 on 24 GitHub Actions runners, the median per
processor; the last column is the worst runner. The scenarios are in
[internal/scenario/scenario.go](internal/scenario/scenario.go), the method and
the history in [benchmarks](docs/benchmarks.md).
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

## Development

```sh
go test ./...
go vet ./...
go test -race ./...
go run ./internal/cmd/capture -addr 127.0.0.1:6390 -verify testdata/snippets.json
```

See [CONTRIBUTING.md](CONTRIBUTING.md), [architecture](docs/architecture.md),
[decisions](docs/adr/README.md), [compatibility](docs/compatibility.md) and
[benchmarks](docs/benchmarks.md).

## License

Apache-2.0, with upstream notices for ported Lua and Redis behavior. See
[LICENSE](LICENSE), [NOTICE](NOTICE) and [LICENSES](LICENSES).