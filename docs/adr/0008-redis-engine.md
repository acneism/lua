# 8. Redis execution environment

Status: B1 implementation in progress.

## Decision

Keep the interpreter independent of Redis. The redis package owns Engine,
Host, Reply, the EVAL environment, script cache, and running executions.
Use the published lua API; any extension needs prior agreement in the journal.

Serialize cache operations with an engine mutex. Compile each resident source
once and share its immutable prototype between states. SCRIPT LOAD pins an
entry. EVAL entries participate in a separate LRU with a default capacity of
500. Loading an existing EVAL entry removes it from the eviction list.
Evicting or flushing a cache entry cannot invalidate a running prototype.

Run each script in an exclusive Lua state. Reuse free states after the outer
Call has unwound its frames and coroutines. Replace KEYS and ARGV and reset
the host, read-only flag, command protocol, replication setting, and abort
record for every execution. Library tables and globals are read-only. The host
briefly unlocks only the globals table when replacing KEYS and ARGV.

Keep memory-limited states outside the reuse pool. The existing Budget API
does not reset its accounting, so each limited run receives a fresh state and
budget. This avoids changing the interpreter API or accumulating charges
across independent requests.

Track active executions separately from pooled states. Running returns copied
metadata. Kill sets an atomic flag checked by the Lua hook. A write attempt
that reaches Host.Call marks the execution as a writer. The default kill
policy refuses writers; KillWriters permits hosts that can roll them back.
An abort reply from Host.Check or Host.Call retains the complete original
reply and escapes Lua protected calls and coroutines.

Reply preserves binary bulk strings, null bulk/array distinctions, aggregates,
and RESP3 scalar types. Its parser consumes one frame and reports the byte
count so callers can retain subsequent pipeline data. It accepts fragmented
input, bounds lengths, and rejects malformed frames. Command arrays and maps
retain their observed ordering.

## Reference evidence

The redis-capture command records the actual wire bytes from Redis 7.2.16.
The JSON byte fields use base64, including command-response traces, to avoid
UTF-8 replacement. Offline tests replay captured host metadata and responses;
they do not implement a second Redis server. Verification checks both the
script result and every recorded command trace without changing files.

The initial reference set contains 152 engine cases and 30 protocol frames.
It covers error locations, redis.call validation order, sandbox names,
read-only writes, shebang parsing, numeric command arguments, and SCRIPT APIs.
The independent protocol fixtures exercise both RESP versions; full RESP3
execution acceptance is the separate B2 review.

The reference resolves unknown EVALSHA before validating numkeys. This differs
from the proposed ordering in the specification and is recorded in the journal.
Redis 7.2.16 crashes on an uncaught error table without an err string; no
invented expected response is stored for that input. Its safe behavior and
the visible error-handler frame metadata remain under discussion.

## Validation still required

Run five seeds with 50,000 steps each for ScriptDifferential and
LuaDifferential on the reference VM, then the final race, coverage, fuzz,
and mutation checks. Do not treat this implementation checkpoint as acceptance.
