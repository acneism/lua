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

Error carries the Lua value, rendered message, innermost Lua line, abort cause,
and internal-error flag. Protected calls catch Lua errors. Abort causes and
internal failures propagate through protected calls and coroutines. The call
boundary converts Go panics into internal failures.

Instructions carry operand names and source lines. Native argument errors use
the call-site name. Go frames count toward caller levels. The hook polls after
the configured number of steps; long source-reader loops poll it too.

## Reference infrastructure

The capture command verifies the Redis version and captures or compares answers.
Its small RESP2 client is private to that command. The public reply parser is
part of B.

The root corpus runner executes the original wrapper. A separate, test-only
outer environment supplies its table.concat helper. Snippets do not inherit it.
The pending manifest lists later-stage features and rejects duplicate,
incomplete, and stale entries.

## Remaining stages

A2 adds string, table, and math libraries. B adds Redis environments, hosts,
replies, EVAL, and SCRIPT. C adds extension libraries. D adds FUNCTION, FCALL,
and DUMP/RESTORE. E completes engine resource controls and final measurements.
