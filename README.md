# lua

A Go library for Redis Lua scripting, under development.

The compatibility target is Redis 7.2.16 and its Lua 5.1 interpreter. The module
uses the Go standard library only. It requires Go 1.26.1 or later.

## Status

A3 completes reference compatibility for the interpreter core, base, coroutine, string, table, and math
libraries, immutable prototypes, Lua tables, metatables, closures, varargs,
tail calls, error locations, read-only tables, and an interruption hook.

The corpus has 4228 snippets. All execute through the original capture wrapper.
`testdata/pending.json` is empty. The reference verifier checks every recorded
answer against Redis.

The exp, log, pow, log10, sinh, cosh, and tanh implementations reproduce the
x86-64 FMA reference, including the power operator. Hosts can supply the seven
trigonometric functions through `Options.Math`. Without it, Go math provides
them; acos uses asin near the endpoints. The 36 exact trigonometric probes in
`testdata/libm.json` are reserved for the separate lua-libm module.

The Redis engine, host interface, EVAL, and FUNCTION API belong to later stages.

`State.Frame` exposes Lua, native, and lost tail-call levels. `CallWithHandler`
runs an error handler at the original failure point before unwinding; xpcall
uses the same behavior. See [error handling](docs/adr/0009-error-handlers.md).

Explicit collectgarbage calls clear weak tables using Lua object reachability.
Hosts register external roots with `State.Retain` and call its release function
when finished. See [weak tables](docs/adr/0010-weak-tables.md).

The interpreter uses 24-byte values, packed instructions, cached register
windows, and reusable Lua call frames. See the [performance decision](docs/adr/0007-vm-performance.md)
for the implementation and its Redis comparisons.

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
