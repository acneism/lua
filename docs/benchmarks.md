# Benchmarks

## B0 regression checks

The B0 performance workflow builds main a34960b, the returned efad817 version,
and the candidate with the same Go version. It copies the four reviewer
benchmarks unchanged into all three test binaries. Eight alternating rounds
compare the candidate with main for those four workloads and the nine core
workloads. Each measurement uses three benchmark iterations after warmup.
The median candidate/main ratio must not exceed 1.05 for any workload.

CPU profiles are separate two-second runs for native calls, pcall, a Go
__index metamethod, and coroutine.wrap/yield. Artifacts retain the raw samples,
machine details, compiled binaries, profiles, source listings, and inlining
diagnostics. The cold hook helpers must not inline. Results will be recorded
after the remote comparison completes.

## P interpreter

Machine: Azure Standard_B4as_v2, AMD EPYC 7763, four vCPUs, 16 GiB RAM.
Linux amd64; Go 1.27.0; Redis 7.2.16 with jemalloc. CPU credits remained
positive. Redis and Go run on the same VM, with no concurrent fuzzing,
race checking, or CPU profiling during this comparison.

Each value is the median of five alternating Redis/Go samples. Go executes
in process after compilation and warmup; Redis EVAL uses a local TCP connection.
Garbage collection before each sample is outside its measured interval on both
sides. The command checks equal return values, requires jemalloc, and rejects
Redis medians below 20 ms. Every raw timing and Go allocation count is retained
in the JSON report.

| Scenario | Redis median ms | Go median ms | Go / Redis |
| --- | ---: | ---: | ---: |
| Loop | 78.135 | 80.744 | 1.033 |
| Fib27 | 21.886 | 31.964 | 1.460 |
| LuaCalls | 29.458 | 40.133 | 1.362 |
| TableInsert | 112.407 | 124.712 | 1.109 |
| TableHash | 62.066 | 69.164 | 1.114 |
| Sort | 39.930 | 42.250 | 1.058 |
| GSub | 26.470 | 37.078 | 1.401 |
| StringConcat | 45.712 | 29.783 | 0.652 |
| Metamethod | 39.244 | 53.859 | 1.372 |

The loop allocates one 24-byte outer result slice after warmup; its allocation
count does not grow with iterations. LuaCalls allocates three objects and 152
bytes for its whole million-call script. Metamethod allocates seven objects and
560 bytes, including the script's tables, closure, and outer result. Loop and
call allocation regressions compare short and long scripts. Generated strings
and table growth still allocate according to their sizes.

Reproduce the comparison on an otherwise idle VM:

```sh
mkdir -p work
go run ./internal/cmd/bench -addr 127.0.0.1:6390 -output work/bench.json
```

Use `go test . -run '^$' -bench . -benchmem` for Go microbenchmarks and
`-cpuprofile` on the comparison command for an exploratory aggregate profile.
Profiling runs are separate from the acceptance comparison. See the
[performance decision](adr/0007-vm-performance.md) for design and baseline
measurements.

The remaining measurements below are historical A2 results, accepted under the
former six-times limit. They are not the P acceptance comparison.

## A2 interpreter and libraries

Machine: Azure Standard_B4as_v2, AMD EPYC 7763, four vCPUs, 16 GiB RAM.
Linux amd64; Go 1.27.0; Redis 7.2.16 built with libc. CPU-credit measurements
were positive during the comparison.

Each value is the median of three alternating Redis/Go rounds on the same VM.
Redis EVAL runs over local TCP and uses Python's monotonic clock. Go execution
runs in process with -benchtime=1x and -benchmem. Scripts are warmed first;
compilation is excluded from the Go execution benchmark.

| Workload | Redis | Go | Ratio | Go allocations per outer call |
| --- | ---: | ---: | ---: | ---: |
| Sum loop, 10 million iterations | 84.294 ms | 234.776 ms | 2.79 | 1 |
| Fibonacci 27 | 22.235 ms | 89.944 ms | 4.05 | 5 |
| 10,000 ordinary Lua calls | 0.375 ms | 1.067 ms | 2.84 | 3 |
| Sequential table insertion, 1 million entries | 121.426 ms | 272.981 ms | 2.25 | 23 |
| Fill and traverse 100,000 string keys | 56.738 ms | 71.842 ms | 1.27 | 400,014 |
| Sort 100,000 descending numbers | 37.896 ms | 113.482 ms | 2.99 | 20 |
| gsub of 1 MiB, %a+ to x | 26.836 ms | 36.878 ms | 1.37 | 48 |
| Convert and concatenate 100,000 numbers | 39.417 ms | 50.799 ms | 1.29 | 200,042 |
| 10,000 Lua arithmetic metamethod calls | 0.453 ms | 2.089 ms | 4.61 | 7 |

All scenarios satisfied the former A2 acceptance limit of six times Redis.
These results do not satisfy the current stage P requirement.

The loop's 48 bytes are its outer result slice. Neither iterations nor ordinary
Lua calls allocate after warmup. Lua metamethod calls also reuse result slots:
their benchmark fell from 20,007 allocations and 1,280,560 bytes per outer run
to seven allocations and 560 bytes. The seven allocations create the script's
table, metatable, closure, and outer result; they do not grow with loop count.
String-key and concatenation workloads allocate their generated strings.

The number-value microbenchmark measured a median 3.742 ns/op, zero bytes,
and zero allocations over three 100 ms runs.

## Profiles

The final two-second Fibonacci CPU profile attributed 72.57% flat samples to VM
dispatch, 10.55% to frame setup, and 6.33% to memory clearing. It did not execute
the protected metamethod path. The final metamethod profile attributed 36.82%
to dispatch, 15.48% to binary dispatch, and 7.11% to frame setup. Before the
single-result optimization, a separate metamethod profile showed repeated
protected-call recovery and argument/result allocations. Reusable result slots
removed those costs while preserving the captured C-depth boundaries.

Before the A2 review fixes, a three-round alternating comparison of earlier A1
source 73da2d8 and A2 dacf508 on the same VM measured Fibonacci medians of
86.623 ms and 90.790 ms: that A2 version was 4.81% slower. The individual ranges were 84.027вЂ“95.615 ms and
89.405вЂ“94.966 ms. These short runs overlap and cannot isolate a compiler
regression from scheduling noise. Both versions allocated five objects and
232 bytes. The profile identifies dispatch and frame setup as the main costs;
the metamethod optimization does not address them.

The final measurements above use production source 3ab696b after the review
fixes. No other fuzzing or race workload ran during benchmarks. The gsub
benchmark also constructs its input with the new interruptible string.rep;
its chunked builder allocates more than the earlier single Repeat call.

## Historical A2 reproduction

Run scripts/bench-reference.py from the repository root on the benchmark VM
with Redis 7.2.16 on port 6390. It verifies the version, warms each script,
and performs all three alternating rounds.

Run a focused Go profile with:

```sh
go test . -run '^$' -bench '^BenchmarkFib27$' -benchtime=2s -cpuprofile=work/fib.cpu
go tool pprof -top work/fib.cpu
```

The initial A1 dispatch measured 1,181.030 ms for the loop and 175.406 ms for
Fibonacci. Register operands, numeric dispatch, instruction references, and
omission of unnecessary close instructions explain the chosen VM design.

C adds JSON workloads. B and E add host workloads and final engine measurements.
