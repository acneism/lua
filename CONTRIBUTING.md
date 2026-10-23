# Contributing

Use Go 1.26.1 or later. Do not add module dependencies or code comments.
Use `gofmt`, `go vet ./...`, and golangci-lint v2 with its default checks.
Commit code and tests with LF line endings.

## Tests

```sh
go test ./...
go test -race ./...
```

CI tests Linux, Windows, and macOS. Race detection runs on Linux. Tests for the
capture tool use an isolated local TCP listener and do not need Redis. A separate
reference job verifies every corpus entry against `redis:7.2.16`.

The interpreter, Redis engine, differential tests, and fuzz targets are pending.
They must be added with their implementation stages. Future randomized tests use
`LUA_DIFF_SEED` and report it on failure. Differential tests will also use
`REDIS_REF_ADDR` and `LUA_DIFF_STEPS`.

## Reference corpus

Use a disposable Redis instance. Snippets execute arbitrary Lua and can change
its data or scripting state. The tool checks that the server reports version
7.2.16 before executing any snippets. Each request has a 30-second deadline;
`-timeout` overrides it. The tool does not flush or stop the server.

```sh
go run ./internal/cmd/capture -addr 127.0.0.1:6390 -verify testdata/snippets.json
```

Verification compares every answer byte for byte, reports mismatches, and exits
with an error if any answer differs. It does not write files or adjust answers.
Each verification input must contain objects with both `src` and `want`.

To capture new inputs, create a JSON array of source strings or objects with `src`.
Capture deduplicates identical sources in input order:

```sh
go run ./internal/cmd/capture -addr 127.0.0.1:6390 -out work/new.json work/sources.json
```

Capture can combine existing snippets with new sources. It executes all of them
against Redis and writes the output only after every request succeeds. It writes
a temporary file beside the destination, then renames it into place. Existing
answers must never be edited by hand. Review new output and run `-verify` before
committing it. `-verify` and `-out` cannot be combined.

See [reference provenance](docs/reference.md) for the exact wrapper convention.

## Pull requests

Branch from current `main`, update README, docs, and the Unreleased changelog,
and include test results and mutation evidence in the PR report. The reviewer
merges PRs. Do not merge your own work.
