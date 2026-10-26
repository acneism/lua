# 11. Redis Lua compiler and binary dumps

Status: implemented; P2a validation is in progress.

## Decision

Port llex.c, lparser.c and lcode.c from Redis 7.2.16. The parser generates the
canonical Lua 5.1 register program directly, without an intermediate syntax
tree. Preserve the exact constant insertion order, instruction words, nested
functions, vararg flags, stack size and debug records. The canonical program
is immutable after compilation.

Reuse the existing lexer after auditing it against this Redis source, rather
than maintaining a second scanner. ASCII character classes match the C locale.
The numeral scanner consumes digits/dots, an optional E exponent and sign,
then alphanumerics/underscore before luaO_str2d-compatible conversion. Quoted
strings use decimal escapes, normalized escaped newlines and literal unknown
escapes. Long strings match separator levels and normalize CR/LF pairs while
discarding the first newline. Lookahead retains the scanner's current line;
next records that line before consuming a token. Token text in diagnostics
stops at NUL, as txtToken passed through C string formatting. Chunk source
names stop at NUL before entering the parser.

Match check_next's strchr behavior for embedded NUL bytes in source: a zero
byte matches the set terminator in exponent, sign and dot lookahead. Numeral
conversion reads only the buffer prefix before NUL. A standalone zero token
suppresses the near-token suffix in syntax errors; other control tokens retain
their char(n) diagnostics. Captured probes cover execution, errors and complete
raw prototypes for these cases.

Preserve Lua's vector limits: 32767 local debug records and 262143 constants or
child prototypes per function. Overflow raises the same unlocated error as
luaM_growaux_, distinct from syntax-limit diagnostics. The code, line and
constructor counters retain C's MAX_INT value of 2147483645. The Compiler
workflow runs the two large generated-source probes separately from normal
race tests and verifies that disabling vector limits breaks all three cases.

The source SHA-256 values are:

| Redis file | SHA-256 |
| --- | --- |
| llex.c | 3eb3be8bf8808469a8825ecfc23319ebbd68b0762d12f272599b0076604b3664 |
| lparser.c | abd12d9c8a707b48b4eea42257edb466a72c9b8792c9cfc69a1203a0ef40364a |
| lcode.c | 027030bbef34115588bd4a64cf4c8c18236a56936835475a154277478767bac5 |
| ldump.c | 80a322fa7528be6cc7f598f5b9049b89acda206b1a54a33dc3af85f298093c93 |
| ldebug.c | 62af4ed93404345bcbf2c3fefc3677d85492d848c217ad58f53cb1ea5ae00c1c |

Translate the canonical program once into the existing uint64 VM words. Keep
the reverse VM-PC to Lua-PC map and resolve runtime names through ldebug.c's
getobjname and symbexec when an error needs them. A register reference in cold
metadata defers this lookup. Eager symbolic scans make long suffix chains
quadratic. Synthetic boolean constants belong only to the VM pool.

The translation consumes CLOSURE binding words and SETLIST block words as
data. Raw instructions and debug ranges retain those words for dumping.
Conditional operations preserve Lua's companion-JMP behavior. The existing
call/return paths retain their ABI and protected-call semantics.

Comparisons encode three operations, two RK flags and the accepted boolean in
opcodes 192 through 215. Operands occupy bytes one and two of the packed word.
The following mapped JMP supplies the accepted target; rejection skips that
word. This keeps the original JMP entry available to other branches. Poll
insertion counts emitted work and defers a checkpoint across a companion
word, rather than testing PC divisibility. Long chains of empty conditionals
therefore still poll. Low opcodes bypass the numerical families with one
range check.

Extend the existing prepared-call fusion to a numerical argument expression.
A local/upvalue function load followed by one numeric binary instruction and
a one-argument CALL can share a dispatch. The original words remain available
as fallback entries. Strings needing coercion, metamethod operands, native and
callable objects, and vararg callees execute those original instructions.
Numeric execution preserves the original CALL's saved PC and destination.
The six operations and both RK modes use the same arithmetic helpers as the
ordinary instruction path.

Fixed-argument arithmetic metamethods enter through the direct frame setup.
Vararg functions and stack growth retain the general push path. Both paths
clear the entire remaining register window, keep C-depth/yield restrictions,
and preserve the metamethod continuation's stack top.

Profiles after direct entry attributed about six percent of call-heavy runs
to clearing small register windows. One to three slots use direct nil writes;
larger windows retain clear. The helper inlines with compiler cost 57. Every
slot is still initialized, including unused temporary registers.

After a fixed Lua return, execute a following MOVE while restoring the caller
register view. This applies to any register copy after a return, preserves its
source and destination, and introduces no new polling point. Prepared calls
materialize their original callee and argument slots before a reentrant hook.

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
Additional captured cases cover signed-jump overflow and SETLIST's extra block
word with 26000 constructor entries. The latter also executes a nested closure
after the data word and compares the entire dump in hex.

Measurements, mutation failures and final CI references will be recorded here
before submission. P2's separate 37-scenario work is outside this change.
