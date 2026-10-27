# Changelog

All notable changes to this project are recorded here.
The format follows Keep a Changelog. Releases will use Semantic Versioning.

## [Unreleased]

### Added

- Observable Lua, native, and virtual tail-call frames through State.Frame.
- State.CallWithHandler with an error handler before stack unwinding.
- Explicit Lua weak-table collection and State.Retain for external host roots.
- Full weak-table collection with a true result for every collectgarbage("step").
- Redis Engine, Host, Reply, script cache, sandbox, EVAL/EVALSHA, and SCRIPT methods.
- Redis's EVAL error handler, run verbatim through State.CallWithHandler, and replies built as in luaCallFunction.
- State.Index, which reads a field with metamethods, and State.SetGlobals.
- Command.MayReplicate for commands that read-only scripts reject.
- Captured fixtures for the handler probes, appendices B and C, and owner decisions on reference crashes.
- RESP wire parsing and rendering, actual-wire capture, and offline command traces.
- Engine lifecycle, concurrent execution, abort, memory, and kill-policy tests.
- Script and Lua differential tests with Redis snapshots.
- Go module, Apache-2.0 license, upstream notices, and repository scaffold.
- A standard-library tool to capture and verify Redis 7.2.16 reference answers.
- An immutable register program, lexer, parser, compiler, and Lua VM.
- Tagged values and Lua array/hash tables, metatables, and read-only support.
- Closures, shared upvalues, varargs, multiple results, loops, and tail calls.
- Base and coroutine libraries, native callbacks, hooks, and panic isolation.
- A 4230-entry main corpus, 36 separate libm probes, and no pending A2 entries.
- Lua string patterns and formatting, table manipulation and auxsort, math and rand48.
- Pattern and format fuzz targets, interruption and callback-allocation regressions.
- Model, lifecycle, allocation, and semantic regression tests.
- Compile/run fuzz targets, PR fuzz jobs, and ten-minute nightly fuzz jobs.
- CI on Linux, Windows, and macOS, lint, race, and reference checks.
- Architecture decisions, compatibility notes, and interpreter benchmarks.

- A reusable test-only luatest.RunCorpus API for external math integrations.
- A Go command to compare nine core workloads against Redis with alternating samples.
- Hook reentry, straight-block interruption, and loop allocation regressions.

### Changed

- Compact 24-byte values, packed opcodes, cached register windows, and reusable frames.
- A 48-byte frame retains the observable count of collapsed Lua tail calls.
- Direct Lua calls and arithmetic metamethod continuations with reusable registers.
- Fused loop and call sequences, versioned metamethod caches, and direct array writes.
- Corpus validation accepts growth beyond the original 707 entries.
- Coverage CI requires the interpreter package and enforces its 85% threshold.

### Removed

- The A0 validation report from docs; reports belong in PRs and the journal.

### Fixed

- Reader failures returned by load preserve the active error handler.
- Chunk names stop at the first zero byte, including implicit source names.
- pcall reads err of a caught table through __index, accepts a number, and returns a non-string err as the third value, as Redis's pcall does.
- string, math, and table register their functions in Lua's order, so pairs visits them as Redis does.
- loadstring and load give new chunks the current globals.
- Stack overflow follows Lua's call-array growth: new coroutines overflow at Redis's depth, and the error handler keeps room to run.
- Strings convert to numbers up to the first zero byte, and assert messages stop there, as in C.

- xpcall invokes its handler at the original error point, including recursive failures.
- Error levels include lost Lua tail calls and preserve native callback frames.
- Unused corpus metadata is rejected, with per-fixture instruction-limit sidecars.
- Negative log10 NaN normalization has one authoritative implementation.
- Architecture-independent NaN propagation and inherited loadstring syntax depth.
- Shared-product FMA contractions in near-one log and the pow low correction.

- C-call depth for metamethods, generic iterators, protected calls, and coroutines.
- Dynamic field names in runtime errors and token locations in register errors.
- Temporary-register growth in long left-associated binary expressions.
- Separate syntax/runtime chunk-name limits and fractional-power NaN signs.
- Parameter, for-loop, and break diagnostics; bounded individual corpus runs.
- Per-call allocations and recover boundaries for Lua metamethod callbacks.
- Iterative binary/suffix compilation, bounded pattern recursion, and rep hook polling.
- Redis FMA math algorithms, injectable trigonometry, and NUL-free format diagnostics.
