# 1. Register VM

Status: proposed for A1 review.

## Context

The interpreter must support Lua 5.1 names and source locations, tail calls,
coroutines, hooks, and allocation-free ordinary Lua calls. The specification
requires a standard-library implementation and a target within four times
Redis's execution time, with six times as the acceptance limit.

## Decision

Parse into a temporary syntax tree, compile it to an immutable register program,
and execute explicit frames. Reuse register and frame slices between calls.
Tail calls replace frames. Keep debug names and source lines in instructions.

Use direct registers and constants for operands when evaluation order permits.
Keep a copy when the right operand could change the left operand through a call.
Specialize the numeric dispatch path and emit scope-close instructions only
for scopes with captured locals.

## Evidence

On Azure Standard_B4as_v2, AMD EPYC 7763, Linux amd64, Go 1.27.0, the median of
three interleaved runs was 234.776 ms for a ten-million-iteration loop versus
84.294 ms in Redis 7.2.16: 2.79 times. Fibonacci 27 took 89.944 ms versus
22.235 ms: 4.05 times. See [benchmarks](../benchmarks.md).

The loop allocates one 48-byte result slice per outer call and no memory per
iteration. The 10,000-call workload allocates three objects per outer execution;
the allocation test confirms that increasing ordinary Lua calls does not
increase allocation count after warmup.

## Consequences

VM stacks can be suspended without Go goroutines. Native callbacks, protected
calls, and metamethods still need bounded Go reentry. The compiler must preserve
multiple-result and operand-evaluation rules when optimizing registers.
No AST execution backend is maintained.
