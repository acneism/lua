# Changelog

All notable changes to this project are recorded here.
The format follows Keep a Changelog. Releases will use Semantic Versioning.

## [Unreleased]

### Added

- Go module, Apache-2.0 license, upstream notices, and repository scaffold.
- A standard-library tool to capture and verify Redis 7.2.16 reference answers.
- An immutable register program, lexer, parser, compiler, and Lua VM.
- Tagged values and Lua array/hash tables, metatables, and read-only support.
- Closures, shared upvalues, varargs, multiple results, loops, and tail calls.
- Base and coroutine libraries, native callbacks, hooks, and panic isolation.
- A 3105-entry reference corpus with no pending A2 entries.
- Lua string patterns and formatting, table manipulation and auxsort, math and rand48.
- Pattern and format fuzz targets, interruption and callback-allocation regressions.
- Model, lifecycle, allocation, and semantic regression tests.
- Compile/run fuzz targets, PR fuzz jobs, and ten-minute nightly fuzz jobs.
- CI on Linux, Windows, and macOS, lint, race, and reference checks.
- Architecture decisions, compatibility notes, and interpreter benchmarks.

### Changed

- Corpus validation accepts growth beyond the original 707 entries.
- Coverage CI requires the interpreter package and enforces its 85% threshold.

### Removed

- The A0 validation report from docs; reports belong in PRs and the journal.

### Fixed

- C-call depth for metamethods, generic iterators, protected calls, and coroutines.
- Dynamic field names in runtime errors and token locations in register errors.
- Temporary-register growth in long left-associated binary expressions.
- Separate syntax/runtime chunk-name limits and fractional-power NaN signs.
- Parameter, for-loop, and break diagnostics; bounded individual corpus runs.
- Per-call allocations and recover boundaries for Lua metamethod callbacks.
