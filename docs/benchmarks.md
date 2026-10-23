# Benchmarks

A0 implements reference tooling only. Interpreter performance has not been
measured. No Redis compatibility or performance claim is made at this stage.

Stage A will compare loop, Fibonacci, table insertion, sort, pattern substitution,
and string concatenation with Redis 7.2.16 on the same machine. Later stages add
JSON and host command workloads. Record the median of three interleaved runs,
allocations, machine details, seeds, and the exact commits.
