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
| Sum loop, 10 million iterations | 84.321 ms | 237.407 ms | 2.82 | 1 |
| Fibonacci 27 | 22.155 ms | 87.493 ms | 3.95 | 5 |
| 10,000 ordinary Lua calls | 0.432 ms | 1.085 ms | 2.51 | 3 |
| Sequential table insertion, 1 million entries | 119.530 ms | 262.995 ms | 2.20 | 23 |
| Fill and traverse 100,000 string keys | 67.423 ms | 69.744 ms | 1.03 | 400,014 |

The loop's 48 bytes belong to its outer result slice, not iterations. Lua
calls reuse frames and registers. The call-allocation regression compares
different iteration counts after warmup. Hash workloads allocate strings for
keys; table resizes allocate capacity in batches.

The number-value microbenchmark measured a median 3.744 ns/op, zero bytes,
and zero allocations. It used three 100 ms runs.

The initial dispatch measured 1,181.030 ms for the sum loop and 175.406 ms for
Fibonacci. Register operands, direct numeric dispatch, instruction references,
and omission of unnecessary close instructions explain the chosen VM design.

Run the Go workloads with:

```sh
go test . -run '^$' -bench '^Benchmark(Loop|Fib27|LuaCalls|TableInsert|TableHash)$' -benchtime=1x -count=3 -benchmem
```

A2 will add sort, pattern substitution, and string concatenation. C adds JSON.
B and E add host workloads and final engine measurements. These interpreter
figures do not claim performance for the unimplemented engine or libraries.
