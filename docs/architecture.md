# Architecture

## Interpreter

The lexer and parser build a temporary syntax tree. The compiler resolves locals
and upvalues and emits register instructions into an immutable `Proto`.
Prototypes own constants, child prototypes, source lines, and upvalue descriptors.
Execution never modifies them.

A `State` owns globals, identities, hook counters, and execution stacks.
Values carry a tag, a float64, a string, and an object pointer. Numbers are not
boxed. Common numeric operations run directly in the dispatch loop. Slow paths
handle coercion, metatables, and errors.

Lua calls push explicit frames into reusable slices. Varargs precede registers
in the value stack so expanding argument lists cannot overwrite saved varargs.
Tail calls close open upvalues and replace the current frame.
Left-associated binary chains reuse their result register, so temporary stack
capacity does not grow with the number of operands.
Binary and suffix chains are traversed iteratively. The remaining recursive
syntax and compiler paths follow parser nesting, bounded at 200 levels.
Prototype dumping follows the same bounded function tree. Table sorting
recurses only into the smaller partition; table rehash recursion is bounded
by the finite array/hash sizing ranges.

Open upvalues refer to a thread and register index. Closures that capture one
local share its upvalue. Scope exit copies the value into the upvalue object.
Loop variables close on each iteration. The compiler omits close instructions
when no local in that scope was captured.

## Tables

Tables port the array/hash layout, rehash sizing, collision chains, backward
free-slot search, traversal, and length boundary search from Lua 5.1.5 in Redis.
String and number hashes follow the source. Object keys use stable state IDs.
Deleted hash keys remain available to `next`.

Constructors use Lua's floating-byte capacities and `SETLIST` batches of 50.
The final call or vararg expression can expand the last batch.

## Coroutines and errors

Coroutines own VM stacks. Resume switches the active stack. Yield saves its
result destination and suspends dispatch. No goroutine is created. Native,
metamethod, and generic-iterator boundaries prohibit yielding. The outer Call
closes unfinished coroutines, including after an abort.

C-call depth follows Lua's luaD_call entries, including protected calls,
metamethods, and generic iterators. Resumed coroutines inherit the caller's
depth. Error handlers retain the failing depth and Lua's extra handling reserve.
Native frame depth is tracked separately for yield boundaries.

Lua callbacks needing one result write into a temporary VM result slot. This
avoids allocating argument/result slices or installing a recover boundary for
each Lua metamethod and sort comparator. The enclosing protected call performs
unwinding. Errors record the C depth where they originate.

Error carries the Lua value, rendered message, innermost Lua line, abort cause,
and internal-error flag. Protected calls catch Lua errors. Abort causes and
internal failures propagate through protected calls and coroutines. The call
boundary converts Go panics into internal failures.

Instructions carry operand names and source lines. Native argument errors use
the call-site name. Go frames count toward caller levels. The hook polls after
the configured number of steps; long source-reader loops poll it too.
Syntax errors render chunk names with llex.c's 80-byte buffer; runtime errors
use LUA_IDSIZE, 60 bytes. Both apply Redis's luaO_chunkid rules.

## Reference infrastructure

The capture command verifies the Redis version and captures or compares answers.
Its small RESP2 client is private to that command. The public reply parser is
part of B.

The test-only luatest.RunCorpus runner executes the original wrapper. A separate, test-only
outer environment supplies its table.concat helper. Snippets do not inherit it.
The pending manifest is empty after A2. RunCorpus accepts JSON fixture paths,
reads budgets and normalization rules from their directories, and preserves
host options. The root test and the external lua-libm integration share it.

## Remaining stages

L supplies exact glibc trigonometry through Options.Math in the separate
lua-libm module. No production dependency is added to lua.
B adds Redis environments, hosts,
replies, EVAL, and SCRIPT. C adds extension libraries. D adds FUNCTION, FCALL,
and DUMP/RESTORE. E completes engine resource controls and final measurements.
