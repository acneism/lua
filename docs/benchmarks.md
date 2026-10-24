# Benchmarks

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
| Sum loop, 10 million iterations | 84.314 ms | 308.812 ms | 3.66 | 1 |
| Fibonacci 27 | 24.082 ms | 98.701 ms | 4.10 | 5 |
| 10,000 ordinary Lua calls | 0.370 ms | 1.063 ms | 2.88 | 3 |
| Sequential table insertion, 1 million entries | 132.789 ms | 318.550 ms | 2.40 | 23 |
| Fill and traverse 100,000 string keys | 90.485 ms | 86.434 ms | 0.96 | 400,015 |
| Sort 100,000 descending numbers | 44.714 ms | 134.898 ms | 3.02 | 20 |
| gsub of 1 MiB, %a+ to x | 29.542 ms | 20.771 ms | 0.70 | 34 |
| Convert and concatenate 100,000 numbers | 46.546 ms | 49.353 ms | 1.06 | 200,042 |
| 10,000 Lua arithmetic metamethod calls | 0.442 ms | 2.551 ms | 5.78 | 7 |

All scenarios satisfy the acceptance limit of six times Redis. Fibonacci and
metamethod calls exceed the four-times target and remain optimization opportunities.

The loop's 48 bytes are its outer result slice. Neither iterations nor ordinary
Lua calls allocate after warmup. Lua metamethod calls also reuse result slots:
their benchmark fell from 20,007 allocations and 1,280,560 bytes per outer run
to seven allocations and 560 bytes. The seven allocations create the script's
table, metatable, closure, and outer result; they do not grow with loop count.
String-key and concatenation workloads allocate their generated strings.

The number-value microbenchmark measured a median 4.006 ns/op, zero bytes,
and zero allocations over three 100 ms runs.

## Profiles

The two-second Fibonacci CPU profile attributed 65.12% flat samples to VM
dispatch, 14.88% to frame setup, and 7.91% to memory clearing. It did not execute
the protected metamethod path. The separate metamethod profile showed repeated
protected-call recovery and argument/result allocations. A single-result VM
call path removed those costs while preserving the captured C-depth boundaries.

An additional three-round alternating comparison of earlier A1 source 73da2d8
and A2 on the same VM measured Fibonacci medians of 86.623 ms and 90.790 ms:
A2 was 4.81% slower. The individual ranges were 84.027–95.615 ms and
89.405–94.966 ms. These short runs overlap and cannot isolate a compiler
regression from scheduling noise. Both versions allocated five objects and
232 bytes. The profile identifies dispatch and frame setup as the main costs;
the metamethod optimization does not address them.

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
