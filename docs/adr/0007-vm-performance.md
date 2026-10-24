# 7. Compact values and interpreter execution

Status: implementation in progress for stage P.

## Decision

Keep the public API and the float64 numeric representation. A Value occupies
24 bytes: a float64, an object pointer, and a pointer to an immutable string
header. Two private read-only object records identify numbers and booleans.
The zero Value remains nil. Numbers preserve every binary64 bit without
interface boxing, unsafe conversions, reflection, or per-operation allocation.
String equality still compares bytes through Value.Equal. String headers can
be allocated independently of numeric slots. Tables, functions, threads, and
userdata retain their original state-local identities.

Compile the existing builder instructions into uint64 words. Register operands
use byte indexes, and numerical operations have distinct register/constant
modes. Constants beyond the compact operand range keep the general path.
Consecutive comparisons and branches share a dispatch. Error names and extended
operands live in pooled cold metadata, indexed by a separate uint32 site table.
A separate int32 line table maps the saved PC to a source line only when needed.
The builder is released after compilation. Prototypes remain immutable.

Keep the active PC, base, code, constants, and a *[256]Value register window in
local variables. Reload them after frame changes and host callbacks. A Lua
frame occupies 40 bytes. Its vararg span derives from start and base. Only
missing parameters and the legacy arg slot require initialization on entry;
compiler instructions initialize all other registers before reading them.
Outer-call cleanup clears retained register storage. Open upvalues are sorted
by descending register index, so returning a frame does not scan lower frames.

Use a direct Lua call/return path in the dispatch loop. Arithmetic Lua
metamethods also push a continuation frame instead of recursively entering Go.
Their frame remembers the previous stack top, and return restores the C-depth
and non-yieldable boundary. Tail calls preserve that continuation. General
native and callable-object paths remain available.

Cache arithmetic metamethods in each State, keyed by the metatable pointer and
its mutation version. Hash writes advance that version; replacing a metatable
changes the pointer. Cleanup clears cached references. Reading a shared
prototype or read-only table never writes through either object.

Poll the hook at calls, backward branches, numeric loop steps, and compiler
checkpoints in long straight blocks. Checkpoints occur every 32 words. A block
accounts for 32 work units; calls and backedges account for four. This bounds
long acyclic code as well as short loops without calling tick on every opcode.
The counter remains local until a host or library boundary. Hook reentry reloads
the frame and register pointers, including after nested stack growth.

## Measurements

The comparison command is internal/cmd/bench. It requires Redis 7.2.16 with
jemalloc and takes five alternating Redis/Go samples on the same Azure
Standard_B4as_v2 machine. It checks matching return values and records every
sample, Go allocations and bytes, and the median ratio. The separate reference
build is in work/redis-p-jemalloc; the earlier libc build is not used for P.
Function and metamethod scenarios perform one million iterations.

The original aggregate CPU profile spends 87.4% in execute, including its
callees. The baseline and isolated 24-byte value measurements are:

| Scenario | A3 baseline / Redis | 24-byte Value / Redis |
| --- | ---: | ---: |
| Loop | 3.02 | 2.03 |
| Fib27 | 3.94 | 3.31 |
| LuaCalls | 3.61 | 3.47 |
| TableInsert | 2.54 | 1.87 |
| TableHash | 1.16 | 1.00 |
| Sort | 2.94 | 1.14 |
| GSub | 1.35 | 1.35 |
| StringConcat | 1.14 | 0.82 |
| Metamethod | 5.21 | 3.22 |

These are intermediate measurements, not acceptance results. Further dispatch,
call and cache changes are being measured separately. The final gate remains
at most 1.5 times Redis for every scenario. Final raw samples, CPU profiles,
allocation checks and validation results will be recorded before submission.
