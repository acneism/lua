# 10. Explicit Lua collection and host roots

Status: implemented for B0 review.

## Decision

Run an iterative Lua object mark and weak-table clearing pass for
collectgarbage(), collectgarbage("collect"), and collectgarbage("step"). Do not call runtime.GC,
inspect the Go heap, use finalizers, or depend on Go weak pointers.
The pass follows Lua objects and table/stack slots rather than unrelated Go
allocations. Go still owns physical allocation and reclamation.

Roots include globals, the string metatable, the main/current threads, active
parent threads, and explicit host registrations. Trace stacks and frames of
every reachable coroutine, function environments, open/closed upvalues, and
active native argument slices. The coroutine registry used for outer-call
cleanup does not make unreachable suspended coroutine cycles live. Six extra
Redis captures cover this case, coroutine.wrap, and zero bytes in __mode.

Active handler registrations remain roots while pcall or Call temporarily
masks them. Their values live in reusable thread storage until the scope ends.

For a frame suspended at a call, trace its vararg span and registers through
the function slot. Arguments remain reachable through the callee stack or its
native argument span. Pending result destinations contain abandoned values and
are excluded until the call returns. Other execution points retain their full register span.

The AST compiler uses separate call-result registers. It also reserves fresh
unary-expression destinations before calling their operands. Follow the
immediately dependent unary instructions when marking this stack shape, so an
old weak key in a pending not result is not retained. Separate-call-result
metadata enables this only for that compiler. Canonical Lua lowering must leave
it disabled: its following unary instruction can write an existing named local,
which remains a root during the call. TestPendingUnaryKeepsLocals protects that
case for the future compiler rebase.
This excludes abandoned temporary registers above the Lua call top. Marking
the full backing slice would keep the reviewer's temporary weak keys alive.
Native frames reuse their otherwise unused start/base fields for argument spans
in the Lua stack. Calls from Go stage arguments above the active stack. Stack
growth records original slices only for active native frames, preserving the
Go callback's arguments across reallocation and later mutation. Collection
traces those original slices instead of stale relocated copies. Native return
removes relocation roots; public calls copy native results before stack cleanup.
Ordinary native calls do not write a separate argument-root list. The frame
layout remains 48 bytes.

Before a reentrant host hook, copy the pending callee and arguments into a
reusable per-thread root buffer. Prepared calls materialize their original
function and argument slots first. Arithmetic and concatenation continuations
also root their chosen metamethod and operand snapshots: the hook may remove
the method from its metatable or change captured variables before collecting.
Nested hooks restore the previous buffer length on return or panic. Clear
vacated slots so completed hooks do not retain values.

Read __mode directly from each metatable at collection time, using the C-string
prefix as Lua 5.1 does. Trace metatables strongly. Weak k entries trace values
even when their keys are initially unmarked: Lua 5.1 has no ephemerons. Weak v
entries trace keys. Weak kv entries trace neither side. Only unmarked tables,
functions, and threads are cleared; scalar values and host userdata remain.
Clear both hash and array entries. Update mutation versions and dense-length
tracking without invoking Lua writes or read-only guards. Preserve hash-chain
tombstones, as normal table deletion does; their keys are not marking edges.

Collection runs synchronously without invoking Lua callbacks or metamethods.
There is no automatic collection or incremental step cycle. step always performs
the full pass and returns true, including argument 0. This is owner-approved
difference 11. The original captured false,0 answer remains unchanged; its one
source has an explicit gc-step-full normalization. Both new step/weak-table
probes compare directly with freshly captured Redis answers. count and gcinfo
continue to report Go allocation figures. Compatibility notes state these
differences and the absence of table finalizers.

## Host values

State.Retain(value) returns an idempotent release function. Each registration
is independent, including repeated registrations of one value. Registrations
remain live across Call cleanup until explicitly released. State remains
single-threaded; registration and release use that same ownership rule.

```go
release := state.Retain(value)
defer release()
```

Register values held outside the Lua graph, including values captured by Go
callbacks or stored in opaque userdata. Go closure environments cannot be
inspected. Retaining a Go function does not discover its hidden captures.
Active Go arguments are rooted automatically. Internal native captures for
the base iterators and coroutine.wrap use traced upvalues.

## Evidence

Append all 20 reviewer weak-table captures and eight protected-reader captures
without changing earlier answers. Six additional sources were captured with
Redis 7.2.16. API tests cover independent host registrations, release, native
arguments, executing functions, closed upvalues, suspended coroutine stacks,
string-metatable roots, read-only tables, and opaque host values.

Algorithms follow deps/lua/src/lgc.c: traversetable, traversestack,
traverseclosure, iscleared, cleartable, and markroot. The protected reader
follows luaD_protectedparser's inherited error handler from ldo.c.

Mutations in a separate source copy produce these failures:

| Mutation | Failing test |
| --- | --- |
| Skip weak clearing | Corpus 4195 |
| Treat strings as weak | Corpus 4199 |
| Make strong values conditional on weak keys | Corpus 4200 |
| Omit host registrations | TestWeakHostRoots |
| Trace abandoned registers above the call top | Corpus 4195, 4196, 4208, 4209 |
| Omit native arguments | TestWeakNativeArguments |
| Omit masked outer handlers | TestWeakMaskedHandlerRoot |
| Omit pending call roots during hooks | TestWeakPendingHookArguments |
| Omit prepared-call slot materialization | TestWeakPreparedHookCallee |
| Omit tracing the hook root buffer | TestWeakMetamethodHookRoots |
| Omit selected metamethod/operand snapshots | TestWeakMetamethodHookRoots |
