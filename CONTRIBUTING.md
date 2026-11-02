# Contributing

Use Go 1.26.1 or later. Do not add module dependencies or code comments.
Use `gofmt`, `go vet ./...`, and golangci-lint v2 with its default checks.
Commit code and tests with LF line endings.

## Tests

```sh
go test ./...
go test -race ./...
LUA_STRESS=1 go test . -run 'TestLongBinaryChains|TestLongSuffixChains'
```

CI tests Linux, Windows, and macOS. Race detection runs on Linux. Tests for the
capture tool use an isolated local TCP listener and do not need Redis. A separate
reference job verifies every corpus entry against `redis:7.2.16`.
Three reviewer million-operation snippets use explicit step budgets in
testdata/limits.json. Exact trigonometric probes live in testdata/libm.json
for lua-libm integration; reference CI verifies their recorded answers too.

The root corpus runner executes the original wrapper and reads the explicit
pending list from `testdata/pending.json`. Do not defer a core-language failure
as a later-stage library feature. Each pending entry must name its source,
stage, and missing feature. Remove entries when their implementation lands.

The table model test reads `LUA_DIFF_SEED` and reports it on failure.

Differential tests require `REDIS_REF_ADDR`; `REDIS_REF_DB` selects a database
for parallel runs. `LUA_DIFF_SEED` selects a seed and `LUA_DIFF_STEPS` selects the
number of scenarios. With no seed override they run five fixed seeds; CI uses
2,000 steps and acceptance uses 50,000.

`TestKillLatency` needs `LUA_KILL_LIMIT`, which CI sets to 10ms. It turns the Go
collector off, because the limit does not count collector pauses. Do not run it
under the race detector.

Run a fuzz target with bounded resources:

```sh
go test . -run '^$' -fuzz '^FuzzCompile$' -fuzztime=30s -parallel=2
go test ./redis -run '^$' -fuzz '^FuzzRestore$' -fuzztime=30s -parallel=2
```

The root package has FuzzCompile, FuzzRun, FuzzPattern, and FuzzFormat; the redis
package has FuzzRESP, FuzzStruct, FuzzExtensions, and FuzzRestore. Compile fuzzing
accepts arbitrary input up to 64 KiB. Run fuzzing generates small programs with a
step hook and checks deterministic execution in fresh states. The fuzz workflow
runs each target for 30 seconds per PR and ten minutes nightly.

The fuzz workflow uses Go 1.27.0, not the go.mod version. Go 1.26 can report
`context deadline exceeded` when `-fuzztime` ends, with no failing input
([golang/go#75804](https://github.com/golang/go/issues/75804)). Use Go 1.27 for
fuzzing too. Move the workflow back to `go-version-file` once go.mod reaches 1.27.

## Performance

The limit is 1.5 times Redis for every scenario, on the median of five fresh
processes. Rounds inside one process are not independent, see
[benchmarks](docs/benchmarks.md).

```sh
go run ./internal/cmd/bench -addr 127.0.0.1:6390 -output work/bench.json
```

The reference must be Redis 7.2.16 with jemalloc. `-processes 1` measures one
process, and `-cpuprofile` always uses one. The Performance workflow runs this on
every PR. Decide from cloud runs: a laptop's timings vary too much to show these
effects. After changing `execute`, follow the checks in
[decision 12](docs/adr/0012-dispatch-loop.md).

## Reference corpus

Use a disposable Redis instance. Snippets execute arbitrary Lua and can change
its data or scripting state. The tool checks that the server reports version
7.2.16 before executing any snippets. Each request has a 30-second deadline;
`-timeout` overrides it. The tool does not flush or stop the server.

```sh
go run ./internal/cmd/capture -addr 127.0.0.1:6390 -verify testdata/snippets.json testdata/libm.json
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

Generate the deterministic math grid inputs with `python scripts/math-grids.py`.
It writes work/a2-math-grid-input.json without expected answers. Capture them
on the reference VM and preserve every previous source and answer.

## Engine fixtures

Engine fixtures preserve raw RESP bytes as JSON base64 byte fields. Capture
them with `go run ./internal/cmd/redis-capture -addr 127.0.0.1:6393 -out
work/engine.json work/inputs.json`. Use a dedicated reference Redis: the command
clears its selected database. Command traces are captured within transactions so
blocking commands retain their nonblocking script behavior.

The reference job verifies `engine.json`, `protocol.json`, `handler.json`, and
`appendix.json` in `redis/testdata` with `-verify`. Tests replay those and the
`resp3.json`, `libraries.json`, `extensions.json`, and `functions.json` fixtures
without a server.

## Pull requests

Branch from current `main`, update README, docs, and the Unreleased changelog,
and include test results and mutation evidence in the PR report. The reviewer
merges PRs. Do not merge your own work.

## Manual acceptance checks

The Fuzz workflow accepts a manual duration of 30s or 10m per target.
Manual runs also validate the complete corpus through the accepted lua-libm
revision and the public luatest API. This provides a remote acceptance run
when the reference VM cannot be allocated.
