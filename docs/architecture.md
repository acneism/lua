# Architecture

## Redis engine

The redis package owns the scripting environment and Host boundary. Engine
serializes cache changes and lends exclusive Lua states to concurrent runs.
Pinned SCRIPT LOAD entries and evictable EVAL entries have separate lifetimes.
Running metadata is copied under the engine mutex; kill flags are atomic.
Uncatchable host aborts retain their original Reply. The environment uses the
existing interpreter API and read-only library tables. See
[decision 8](adr/0008-redis-engine.md).

## Interpreter

The Redis Lua 5.1 parser emits the canonical register program directly. A second
pass translates it to packed uint64 VM words. Immutable prototypes retain raw
instruction words, constant order, debug records, child prototypes, VM-to-Lua
PC maps, pooled operand metadata, and closure binding descriptors. Execution
never modifies them. Runtime error names use ldebug.c's symbolic execution at
the original Lua PC. The binary dumper serializes the canonical program in
Redis's fixed x86-64 layout. See ADR 0011.

A `State` owns globals, identities, hook counters, and execution stacks.
Values occupy 24 bytes: a float64, an object pointer, and an immutable string
header pointer. Private read-only objects identify numbers and booleans.
Numbers are not boxed. Numeric opcodes encode register/constant modes. The
dispatch loop caches its PC, code, constants, frame, and 256-register window;
host callbacks and frame changes reload that state. Slow paths handle coercion,
metatables, and errors.

Lua calls push 48-byte frames into reusable slices and return in the same
dispatch loop. Function entry clears registers beyond supplied parameters.
Varargs precede registers
in the value stack so expanding argument lists cannot overwrite saved varargs.
Tail calls close open upvalues and replace the current frame.
Left-associated binary chains reuse their result register, so temporary stack
capacity does not grow with the number of operands.
Binary and suffix chains are traversed iteratively. The remaining recursive
syntax and compiler paths follow parser nesting, bounded at 200 levels.
Prototype dumping follows the same bounded function tree. Table sorting
recurses only into the smaller partition; table rehash recursion is bounded
by the finite array/hash sizing ranges.

Open upvalues refer to a thread and register index, sorted by descending index
so frame return closes only its captures. Closures that capture one
local share its upvalue. Scope exit copies the value into the upvalue object.
Loop variables close on each iteration. The compiler omits close instructions
when no local in that scope was captured.

## Tables

Tables port the array/hash layout, rehash sizing, collision chains, backward
free-slot search, traversal, and length boundary search from Lua 5.1.5 in Redis.
String and number hashes follow the source. Object keys use stable state IDs.
Deleted hash keys remain available to `next`. A proven dense sequence retains
its exact length across sequential writes. Gaps, interior deletion, positive
integral hash entries, and bulk writes invalidate this shortcut; sparse tables
keep Lua's original boundary search.

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
each Lua metamethod and sort comparator. Arithmetic Lua metamethods run as
continuation frames in the dispatch loop. State-local caches use the metatable
pointer and mutation version to invalidate methods after writes. The enclosing protected call performs
unwinding. Errors record the C depth where they originate.

Error carries the Lua value, rendered message, innermost Lua line, abort cause,
and internal-error flag. Protected calls catch Lua errors. Abort causes and
internal failures propagate through protected calls and coroutines. The call
boundary converts Go panics into internal failures.

Frame exposes all active levels, including native functions and virtual levels
for collapsed Lua tail calls. Error levels use the same sequence. CallWithHandler
and xpcall run handlers before native/interpreter recovery removes the failing
frames; Call, pcall, and new coroutines mask surrounding handlers. Handler
results replace Error.Value. The existing C-depth reserve bounds recursive
handler failures. See [decision 9](adr/0009-error-handlers.md).

Cold instruction metadata carries operand names; errors look up the source
line by saved PC. Native argument errors use the call-site name. Go frames
count toward caller levels. Calls and backward branches account for four hook
work units; straight blocks have checkpoints every 32 instructions. Hook reentry
can grow the stack, so execution reloads register pointers afterward. Long
source-reader and library loops poll the hook too.
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

Explicit Lua collection traces objects from globals, the string metatable,
active/reachable thread stacks and frames, upvalues, handler scopes, native
arguments, and State.Retain registrations. It clears weak table entries after
marking and leaves physical reclamation to Go. It never requests Go GC.
See [decision 10](adr/0010-weak-tables.md).

## Remaining stages

L supplies exact glibc trigonometry through Options.Math in the separate
lua-libm module. No production dependency is added to lua.
B adds Redis environments, hosts,
replies, EVAL, and SCRIPT. C adds extension libraries. D adds FUNCTION, FCALL,
and DUMP/RESTORE. E completes engine resource controls and final measurements.
