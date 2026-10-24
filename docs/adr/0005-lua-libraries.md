# 5. Lua libraries

Status: proposed for A2 review.

## Decision

Port the byte-pattern control flow from Redis 7.2.16's lstrlib.c: bracket
classes, frontier and balanced patterns, backtracking, capture rollback,
position captures, and substitutions. Poll the execution hook during matching
and substitution, including failed matches. Pattern matching does not use Go
regular expressions or Unicode classes.

Preserve str_format's scanning rules and C coercions around Go's numeric
formatting. Handle quoting, byte widths, zero-terminated output, unsigned
conversion, and octal zero precision explicitly. Integer conversions use the
Redis Linux reference's 64-bit rules on every Go architecture.

Port table sorting from auxsort rather than using sort.Slice. Its pivot choices,
comparator order, and invalid-comparator behavior are observable. Lua callbacks
with one result use a reusable VM result slot and inherit the C-call depth.
Protected execution remains at the enclosing call boundary.

Store Redis rand48's 48-bit state in State. Library registration sorts names
before creating native functions, keeping state identities deterministic.

## Evidence

The 3105-entry reference corpus includes every supplied and reviewer probe,
per-function argument errors, all byte classes, formatting flag/precision
combinations, replacement functions and tables, and invalid comparators.
All execute; no entry remains pending A2. FuzzPattern and FuzzFormat check
bounded deterministic execution, arbitrary bytes, and internal panic isolation.

Allocation regression tests compare ordinary Lua calls and metamethod calls
after warmup at different loop counts. Measurements and profiles are summarized
in [benchmarks](../benchmarks.md).

## Consequences

Patterns retain Lua's potentially expensive backtracking, with hooks available
to bound it. The interpreter owns its bytecode, so string.dump serializes its
own prototypes with a LuaQ signature; binary loading remains disabled.
Future Redis engine states can configure budgets and hooks without changing
library APIs.
