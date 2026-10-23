# 4. Coroutines as VM stacks

Status: proposed for A1 review.

## Decision

Give each coroutine a register stack, frame stack, open-upvalue list, status,
and saved yield destination. Resume switches State's active thread. Yield
returns control from dispatch without a goroutine or channel.

Track native-call nesting and protected/metamethod boundaries. Reject yielding
across those boundaries. Returning from a tail-position yield completes the
saved return when the coroutine resumes.

Count C reentry at protected calls, metamethod calls, and generic iterators,
rather than at every native function frame. Resume inherits the parent's count
and adds one entry. Keep Lua's overflow reserve for error handlers, which retain
the depth of the failure. Native depth remains separate from this counter.

Close unfinished coroutine stacks at the outer call boundary. Propagate aborts
and internal errors to the caller, through resume and protected calls.

## Evidence

The reference corpus covers suspended, running, normal, and dead states,
multiple yielded/resumed values, wrap errors, and prohibited boundaries.
Lifecycle tests repeat abandoned-coroutine execution, check closed frames and
upvalues, and compare runtime.NumGoroutine before and after collection.
Native-failure tests cover both first entry and a resumed stack.

## Consequences

There is no coroutine goroutine to leak or panic independently. A suspended
coroutine does not outlive the outer execution. Native Go callbacks cannot
suspend their Go stacks. Lua 5.1 prohibits the relevant cross-native yields.
