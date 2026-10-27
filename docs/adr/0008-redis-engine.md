# 8. Redis execution environment

Status: accepted with B1.

## Decision

Keep the interpreter independent of Redis. The redis package owns Engine,
Host, Reply, the EVAL environment, script cache, and running executions.
It uses the published lua API. B1 added State.Index, which reads a field with
metamethods like `lua_getfield`, and State.SetGlobals, which makes the sandbox
the table that loadstring and load give to new chunks.

Build the EVAL globals as Redis does. Insert names in the order of Lua's base
library and Redis's `luaRegisterRedisAPI`, including the names that Redis's
allow list refuses: `luaV_settable` creates their hash nodes before calling
`__newindex`, so they shape the table's chains. Run Redis's error-handler chunk
verbatim as `@err_handler_def`; its `dbg.getinfo` reads State.Frame. Then set
the error metatable and mark every reachable table read-only.

Run scripts through State.CallWithHandler. Convert a raised error as
`luaCallFunction` does: a non-table value becomes `Error running script`, and a
table supplies `err`, `source`, and `line` through State.Index. Redis 7.2.16
crashes on a table without a string or number `err`; the owner chose Redis
7.4's `ERR unknown error`, kept in testdata/owner-decisions.json.

Serialize cache operations with an engine mutex. Compile each resident source
once and share its immutable prototype between states. SCRIPT LOAD pins an
entry. EVAL entries participate in a separate LRU with a default capacity of
500. Loading an existing EVAL entry removes it from the eviction list.
Evicting or flushing a cache entry cannot invalidate a running prototype.

Run each script in an exclusive Lua state. Reuse free states after the outer
Call has unwound its frames and coroutines. Replace KEYS and ARGV and reset
the host, read-only flag, command protocol, replication setting, and abort
record for every execution. The host briefly unlocks only the globals table
when replacing KEYS and ARGV.

Keep memory-limited states outside the reuse pool. The existing Budget API
does not reset its accounting, so each limited run receives a fresh state and
budget.

Track active executions separately from pooled states. Running returns copied
metadata. Kill sets an atomic flag checked by the Lua hook. A write attempt
that reaches Host.Call marks the execution as a writer. The default kill
policy refuses writers; KillWriters permits hosts that can roll them back.
An abort reply from Host.Check or Host.Call retains the complete original
reply and escapes Lua protected calls and coroutines.

Command errors raised or returned by redis.call and redis.pcall carry
`ignore_error_stats_update = true` when Redis's `scriptCall` produced them,
as in Redis; argument errors do not. Read-only runs reject commands with
Command.Write or Command.MayReplicate.

Reply preserves binary bulk strings, null bulk/array distinctions, aggregates,
and RESP3 scalar types. Its parser consumes one frame and reports the byte
count so callers can retain subsequent pipeline data. It accepts fragmented
input, bounds lengths, and rejects malformed frames.

## Reference evidence

The redis-capture command records the actual wire bytes from Redis 7.2.16.
The JSON byte fields use base64, including command-response traces. Offline
tests replay captured host metadata and responses. Verification checks both
the script result and every recorded command trace without changing files.

- testdata/engine.json: the initial engine cases;
- testdata/handler.json: the error-handler probes, pcall and redis.pcall error
  tables, library traversal order, number parsing, and call depth;
- testdata/appendix.json: one case per row of the specification's appendices
  B and C that belongs to B1;
- testdata/owner-decisions.json: inputs that crash Redis 7.2.16, with replies
  built by the owner's rule.

ScriptDifferential generates scripts of one to five redis.call, redis.pcall,
and pcall statements over random keys, types, and arguments, including
errors, read-only runs, and no-writes shebangs, and compares replies and DUMP
snapshots. LuaDifferential generates programs of arithmetic and coercion,
tostring and tonumber, string.format, patterns from a random grammar, string,
table, and math functions. Generated arithmetic mixes literal numbers, which
exercise constant folding and signed-zero constants, with numbers passed through
a function and evaluated at run time.

Reply conversion follows Lua's 8,000-slot C API limit. The EVAL reply starts
with two stack entries and checks room for four. Arrays add one live slot per
child; maps add four for key conversion and three for value conversion; sets
add three for a key. The RESP reader and writer accept up to 10,000 nesting
levels, above all replies this Lua stack can produce.
