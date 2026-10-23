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

The root corpus runner executes the original wrapper and reads the explicit
pending list from `testdata/pending.json`. Do not defer a core-language failure
as a later-stage library feature. Each pending entry must name its source,
stage, and missing feature. Remove entries when their implementation lands.

The table model test reads `LUA_DIFF_SEED` and reports it on failure.
Redis differential tests remain part of B and will also use `REDIS_REF_ADDR`
and `LUA_DIFF_STEPS`.

Run the current fuzz targets with bounded resources:

```sh
go test . -run '^$' -fuzz '^FuzzCompile$' -fuzztime=30s -parallel=2
go test . -run '^$' -fuzz '^FuzzRun$' -fuzztime=30s -parallel=2
```

Compile fuzzing accepts arbitrary input up to 64 KiB. Run fuzzing generates
small programs with a step hook and checks deterministic execution in fresh
states. The fuzz workflow runs each target for 30 seconds per PR and ten
minutes nightly. Heavy fuzzing and performance measurements run on the
designated Azure machine, which must be deallocated and lose its public IP
after the run.

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

Capture rejects invalid UTF-8 output because JSON would replace its bytes.
Encode binary results as hexadecimal inside the snippet before capturing them.

See [reference provenance](docs/reference.md) for the exact wrapper convention.

## Pull requests

Branch from current `main`, update README, docs, and the Unreleased changelog,
and include test results and mutation evidence in the PR report. The reviewer
merges PRs. Do not merge your own work.
