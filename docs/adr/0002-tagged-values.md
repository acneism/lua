# 2. Tagged values

Status: proposed for A1 review.

## Decision

Represent a value with a type tag, a float64 field, a string field, and a pointer
to an object record. Tables, closures, threads, and userdata have stable
state-local identities. Numbers never enter an interface.

Strings remain byte sequences and do not require a heap object for every
temporary key lookup. Equality compares tags and scalar values or object
identity. Metamethod equality is a separate operation.

## Evidence

The number-value benchmark measured a median 3.744 ns/op and zero allocations.
The ten-million-iteration Lua loop allocated only its outer return slice.
The Lua-call allocation test compares ten and one thousand calls after warmup
and rejects allocation growth. Measurements are in [benchmarks](../benchmarks.md).

## Consequences

Each scalar slot reserves space for the other representations. This trades
some table and stack memory for direct numeric access without unsafe code,
reflection, interface boxing, or a dependency. Host objects should be allocated
through their State to obtain stable identity numbers.
