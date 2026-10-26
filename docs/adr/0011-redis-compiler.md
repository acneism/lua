# 11. Redis Lua compiler and binary dumps

Status: implemented; P2a validation is in progress.

## Decision

Port llex.c, lparser.c and lcode.c from Redis 7.2.16. The parser generates the
canonical Lua 5.1 register program directly, without an intermediate syntax
tree. Preserve the exact constant insertion order, instruction words, nested
functions, vararg flags, stack size and debug records. The canonical program
is immutable after compilation.

Translate the canonical program once into the existing uint64 VM words. Keep
the reverse VM-PC to Lua-PC map and resolve runtime names through ldebug.c's
getobjname and symbexec when an error needs them. A register reference in cold
metadata defers this lookup. Eager symbolic scans make long suffix chains
quadratic. Synthetic boolean constants belong only to the VM pool.

The translation consumes CLOSURE binding words and SETLIST block words as
data. Raw instructions and debug ranges retain those words for dumping.
Conditional operations preserve Lua's companion-JMP behavior. The existing
call/return paths retain their ABI and protected-call semantics.

The VM initializes the complete register window on function entry because
Lua's compiler can omit an initial LOADNIL. Both direct call paths perform the
same initialization. A canonical numeric-for offset must not enter the packed
word's otherwise unused C field: the accumulator fusion uses that field.

Port ldump.c using a fixed Redis x86-64 layout on every architecture. The
header is 1b4c75615100010408040800. Integers and instructions use four bytes,
size_t and binary64 use eight bytes, and every field is little-endian. Dumps
include source, lines, local live ranges and upvalue names. Binary loading
continues through the text lexer and rejects the initial escape byte.

Cache jump-list tails while compiling. This changes the cost of luaK_concat
without changing any raw instruction. Lua's signed 18-bit jump limit remains
131071. Long and/or stress cases therefore use 60000 operands; larger control
structures return Redis's compiler error. The million-equality corpus case
uses an eight-million-step harness budget because Lua materializes comparison
booleans with jumps and LOADBOOL instructions.

## Validation

The twelve reviewer dump probes are captured from Redis and run in the corpus.
Capture's -dump mode records the full prototype of every corpus source up to
4 KiB as hex. The Compiler workflow captures those answers on Redis 7.2.16,
compares canonical dumps and runs the existing nine-scenario measurement with
five alternating samples. No recorded answer is constructed by the compiler.

Measurements, mutation failures and final CI references will be recorded here
before submission. P2's separate 37-scenario work is outside this change.
