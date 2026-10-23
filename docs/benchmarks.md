# Benchmarks

## A1 interpreter

Machine: Azure Standard_B4as_v2, AMD EPYC 7763, four vCPUs, 16 GiB RAM.
Linux amd64; Go 1.27.0; Redis 7.2.16 built with libc. CPU-credit samples with
count greater than zero remained above zero during measurement.

Each value is the median of three runs. Each round measured Redis EVAL over
local TCP, then the Go benchmarks in process. Both implementations ran on
the same VM. Scripts were warmed first. Go used -benchtime=1x and -benchmem.
Redis time used Python's monotonic clock. Source compilation is excluded from
the Go execution benchmark.

| Workload | Redis | Go | Ratio | Go allocations per outer call |
| --- | ---: | ---: | ---: | ---: |
| Sum loop, 10 million iterations | 84.265 ms | 235.629 ms | 2.80 | 1 |
| Fibonacci 27 | 22.125 ms | 86.919 ms | 3.93 | 5 |
| 10,000 ordinary Lua calls | 0.383 ms | 1.046 ms | 2.73 | 3 |
| Sequential table insertion, 1 million entries | 122.725 ms | 287.782 ms | 2.34 | 23 |
| Fill and traverse 100,000 string keys | 60.411 ms | 73.325 ms | 1.21 | 400,015 |

The loop's 48 bytes belong to its outer result slice, not iterations. Lua
calls reuse frames and registers. The call-allocation regression compares
different iteration counts after warmup. Hash workloads allocate strings for
keys; table resizes allocate capacity in batches.

The number-value microbenchmark measured a median 3.748 ns/op, zero bytes,
and zero allocations. It used three 100 ms runs.

The initial dispatch measured 1,181.210 ms for the sum loop and 175.406 ms for
Fibonacci. Register operands, direct numeric dispatch, instruction references,
and omission of unnecessary close instructions explain the chosen VM design.

Run the Go workloads with:

```sh
go test . -run '^$' -bench '^Benchmark(Loop|Fib27|LuaCalls|TableInsert|TableHash)$' -benchtime=1x -count=3 -benchmem
```

The complete alternating comparison is implemented in scripts/bench-reference.py.
Run it from the repository root on the benchmark VM with Redis 7.2.16 on port
6390. It verifies the reference version and performs all three rounds.

A2 will add sort, pattern substitution, and string concatenation. C adds JSON.
B and E add host workloads and final engine measurements. These interpreter
figures do not claim performance for the unimplemented engine or libraries.
