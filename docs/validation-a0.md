# A0 validation

## Local checks

The initial validation used Windows amd64 and Go 1.27.0:

- `go test ./...`: passed.
- `go vet ./...`: passed.
- `gofmt`: no unformatted files.
- golangci-lint 2.15.0 with default checks: zero issues.
- Capture command coverage: 94.0% of statements.
- `go list -m all`: the project module only.

The interpreter and Redis engine packages do not exist in A0. Their coverage
thresholds apply when those packages are added. CI already enforces 85% for
their implemented statements. Linux race detection and tests on all three
operating systems run in the PR workflow.

## Reference checks

All 707 snippets passed `capture -verify` against the local WSL Redis 7.2.16
build. A separate capture into an ignored work file reproduced all 707 sources
and answers exactly. The imported corpus retained the SHA-256 recorded in
[reference.md](reference.md).

Unit tests use synthetic RESP frames to exercise transport and error paths.
They use the supplied corpus for successful answer fixtures. Transport fixtures
are not Lua compatibility evidence. The real Redis run and CI reference job
provide that evidence for the recorded answers.

## Mutation checks

Each mutation was applied alone, the named test failed, and the implementation
was restored. The complete test suite passed after restoration.

| Mutation | Test that failed |
| --- | --- |
| Replace `if got != cases[i].Want` with `if false` | `TestVerifyRejectsEveryMismatch` |
| Stop stripping the leading `ERR ` from captured replies | `TestCaptureNormalizesAndDeduplicates` |
| Change decimal byte escapes from `%03d` to `%d` | `TestQuote` |
| Save output before returning a failed EVAL request | `TestCaptureDoesNotOverwriteOnFailure` |
| Replace `if version != referenceVersion` with `if false` | `TestRejectsWrongReferenceVersion` |

Run a named test with `go test ./internal/cmd/capture -run '^TestName$' -count=1`.
The version mutation initially survived because the test accepted a later
transport error. The test now requires the exact version rejection message;
the repeated mutation fails it.

The interpreter mutations, differential tests, fuzz targets, and runtime
benchmarks belong to A1 and later stages. A0 does not claim those checks.
