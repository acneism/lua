# Benchmarks

These are historical A2 measurements, accepted under the former 6x limit.
The current requirement is at most 1.5x Redis on every scenario. Stage P will
measure Redis 7.2.16 with jemalloc on the same VM, using medians of five
alternating rounds and Redis times of at least 20 ms. Function and metamethod
workloads use one million repetitions. A3 makes no performance acceptance claim.

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
86.623 ms and 90.790 ms: that A2 version was 4.81% slower. The individual ranges were 84.027–95.615 ms and
89.405–94.966 ms. These short runs overlap and cannot isolate a compiler
regression from scheduling noise. Both versions allocated five objects and
232 bytes. The profile identifies dispatch and frame setup as the main costs;
the metamethod optimization does not address them.

The final measurements above use production source 3ab696b after the review
fixes. No other fuzzing or race workload ran during benchmarks. The gsub
benchmark also constructs its input with the new interruptible string.rep;
its chunked builder allocates more than the earlier single Repeat call.

## Reproduction

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
