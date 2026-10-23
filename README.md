# lua

A Go library for Redis Lua scripting, under development.

The compatibility target is Redis 7.2.16 and its Lua 5.1 interpreter. The module
uses the Go standard library only. It requires Go 1.26.1 or later.

## Status

A0 provides the project scaffold, 707 reference snippets, a capture and verification
tool, and CI. The interpreter and Redis engine are not implemented yet. The module
does not expose a scripting API at this stage.

The planned packages are `github.com/acneism/lua` for the interpreter and
`github.com/acneism/lua/redis` for the scripting engine. Redis commands will run
through a host interface. The library will not include a Redis server or storage.
A host example will be added when the engine API exists.

## Development

Run the deterministic tests and static checks:

```sh
go test ./...
go vet ./...
```

Verify the complete reference corpus against a disposable Redis 7.2.16 instance:

```sh
go run ./internal/cmd/capture -addr 127.0.0.1:6390 -verify testdata/snippets.json
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for capture instructions and CI details,
[architecture](docs/architecture.md) for the implementation sequence, and
[compatibility](docs/compatibility.md) for the target and current limits.

## License

Apache-2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
