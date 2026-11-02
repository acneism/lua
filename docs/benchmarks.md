# Benchmarks

## P2 interpreter

Measured on 2026-11-02 at 8e95f84 on GitHub Actions runners by the Performance
workflow: Go benchmark in process against EVAL in Redis 7.2.16 with jemalloc on
the same machine, median of five alternating samples in each run. Nine runs:
four on EPYC 7763, three on EPYC 9V74, one on EPYC 9V45, one on Xeon Platinum
8370C. All 37 scenarios stay at or below 1.39; the table is in the
[README](../README.md#performance).

Later measurements take the median of five fresh processes for each scenario,
because rounds in one process are not independent. A store and a load conflict
when the low bits of their physical addresses match: 16 bits on the EPYC
processors measured, where a page pair conflicts in about 1 case of 16, and 20
bits on Xeon 8370C. The operating system assigns the pages once, so every round
of an unlucky process is slow while Redis stays the same. Moving the Lua stack to
another physical page at the same virtual address removed the slowdown on EPYC
7763, EPYC 9V74 and Xeon 8573C (28.62 to 23.46 ms on 7763), and the same physical
page at another virtual address kept it. Go cannot choose physical pages, so this
is a property of the measurement and not of one commit. Not every outlier was
traced to this: CallMetamethod 1.57 and SmallTables 1.52 were not examined, and
Fib27 1.51 on EPYC 9V45 does not fit, because the effect there stayed under 5%.

The previous layout of the dispatch loop stored the instruction word and the PC
on the stack for every instruction. Removing those stores changed these scenarios
in one run on EPYC 9V74 against 3ba27ac: Ipairs 0.64, TableInsert 0.65, WhileLoop
0.73, FieldAccess 0.77, ArrayReadWrite 0.77, MethodCall 0.79. See
[ADR 12](adr/0012-dispatch-loop.md).

Compare commits only on the same runner. Timings differ between runs by up to
20%, and small code changes move unrelated scenarios by shifting code layout: a
restored superinstruction sped up Fib27 by 10–18% and slowed MultiReturn by up to
43% on the same runner type.

## Redis libraries and redis.call

The same command measures these scenarios through the engine, at 658b3d1. RedisCall and
HashFill use a host that answers from memory without a network, so they give the lower
bound of the engine's overhead. Each value is the median of five alternating samples in
one run.

| Scenario | EPYC 9V45 | EPYC 9V74 | EPYC 7763 |
| --- | ---: | ---: | ---: |
| CJSONRoundTrip | 1.03 | 1.03 | 1.02 |
| CMsgpackRoundTrip | 1.10 | 1.15 | 1.14 |
| BitOps | 1.00 | 1.12 | 0.92 |
| StructRoundTrip | 0.87 | 0.96 | 1.03 |
| RedisCall | 0.92 | 0.90 | 0.84 |
| HashFill | 0.98 | 1.02 | 0.94 |

The three script runs measured every Appendix G scenario at or below 1.35.

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

The command starts five fresh processes and reports the median one for each
scenario, with every ratio in `Processes`; `-processes 1` measures one process.

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
