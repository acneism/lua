# 9. Error handlers and observable frames

Status: implemented for B0 review.

## Decision

Expose State.Frame with Lua 5.1's observable stack levels. Native Go functions
occupy C levels, and collapsed Lua tail calls occupy virtual tail levels.
Frame source names retain the original chunk string; C and tail frames have
their reference source names and line -1. A main prototype is distinct from
a nested Lua function. Only the currently running coroutine is inspected.

Store a collapsed-tail count in each Lua frame. Tail replacement transfers
and increments this count; native tail calls retain their Lua caller until
the native function returns. Error's level lookup uses the same observable
frame sequence. Virtual tail frames contribute a level without adding a
source prefix. The counter increases the P1 frame from 40 to 48 bytes; future
layout optimizations must retain its observable behavior.

CallWithHandler and xpcall install a scoped, thread-local handler. Public Call
and pcall mask it. Coroutine entry starts without a handler. Internal Lua
metamethod calls inherit it. Native and interpreter panic boundaries invoke
the handler before frame cleanup, so native callbacks and open upvalues
remain visible. Handler failures re-enter the same handler until the existing
C-depth reserve produces error in error handling.

Each handler scope receives a monotonically increasing identity within a
state-local owner. Errors retain that small owner and identity, preventing a
second handler invocation as the same error crosses internal recovery layers.
The owner is allocated once on first use; successful handler scopes do not
allocate bookkeeping records. Distinct states never share owners. Abort and
internal failures bypass handlers. Cleanup releases thread-local handler values.

The existing Call C-depth convention is unchanged. CallWithHandler introduces
no extra C level and returns the handler's first value in Error.Value.

## Evidence

Nine reviewer tail-call probes are appended to the main reference corpus;
the original 4185 sources and answers remain unchanged. All nine were verified
again through the Redis 7.2.16 capture command.

API tests cover native-to-native frames, virtual tails, raw loadstring source
names, handler scope masking, coroutine entry, argument forwarding, C depth,
abort/internal failures, and recursive handler errors. Tests compile the exact
EVAL handler from Redis as @err_handler_def with a minimal getinfo adapter.
They verify the original Lua error point, =[C]:-1 for gsub calling error,
and @err_handler_def:10 when the handler writes a read-only table.

B1 will compile the same Lua handler in its environment and construct errors
from the resulting table. It must not synthesize source names from Error.Line.

Mutations run in separate source copies:

| Mutation | Failing tests |
| --- | --- |
| Invoke handlers after protected-call cleanup | TestXpcallHandlerSeesLiveFrames and TestHandlerSeesErrorPoint |
| Discard the collapsed-tail count | TestFrameLevels, TestDirectHandlerTailFrame, and corpus entries 4186, 4187, 4193, 4194 |
| Skip native frames in Frame | TestFrameGoCallsGo and TestHandlerSeesErrorPoint/Go_calls_Go |
