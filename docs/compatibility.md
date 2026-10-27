# Compatibility

## Redis engine

EVAL runs Redis's `__redis__err__handler` verbatim, compiled as
`@err_handler_def`, through State.CallWithHandler. Its `dbg.getinfo` reads
State.Frame. The reply to a raised error is built as in `luaCallFunction`:
`err`, `source`, and `line` are read like `lua_getfield`, so `__index` applies.

The EVAL globals are built in Redis's registration order. Names refused by the
Redis allow list leave empty hash nodes, as `luaV_settable` does, so `pairs(_G)`
visits globals in Redis's order. Library tables follow their `luaL_Reg` order.

Differences from Redis 7.2.16:

- An error table without a string or number `err`, for example `error({})`,
  crashes Redis 7.2.16. As in Redis 7.4, the reply is `ERR unknown error` with
  the usual ` script: <sha>, on <source>:<line>.` suffix. The same applies when
  `__index` raises while `err` is read, and when the handler itself fails on
  `tostring` of such a table. An `__index` that raises while `source` or `line`
  is read leaves that field out.
- EVAL-only cache entries use an LRU of 500 by default; SCRIPT LOAD entries
  remain pinned until SCRIPT FLUSH. Redis 7.2 does not evict EVAL entries.
- KillWriters permits hosts with rollback support to interrupt writers; the
  default retains Redis's refusal. A killed script cannot catch the kill with
  pcall even once.
- Memory-limited runs use fresh states because Budget accounting cannot be reset.
- Redis's `COMMAND INFO` does not report `may_replicate`. Hosts set
  Command.MayReplicate themselves; read-only scripts reject such commands, as
  Redis does for PFCOUNT, PUBLISH, and SPUBLISH.
- Extension-library names are present but their implementations belong to C.

Reply conversion follows the reference Lua stack budget, including different
stack costs for array, map, and set entries. Captured deep and cyclic replies
exercise these boundaries.

## Call depth

Lua 5.1 raises `stack overflow` when its CallInfo array grows past 20,000
entries, and then leaves room for the error handler. A new coroutine overflows
at 16,381 nested Lua calls under pcall, as in Redis. The main thread behaves
like Redis's after its first overflow, at 19,997 calls: on a freshly started
Redis the first overflow happens at 16,381, and Redis's collector later shrinks
the array again, so the reference depth depends on history.

The target is Redis 7.2.16 and its Lua 5.1 variant. A2 implements the interpreter
and base, coroutine, string, table, and math libraries. EVAL, FUNCTION, host commands,
and C extensions are not implemented yet.

All 4283 main reference snippets execute; `testdata/pending.json` is empty.
Tests execute the original capture wrapper; its
helper environment is separate from the snippet environment. Reference CI
checks all recorded answers against Redis, including pending entries.

## Interpreter details

- Tables preserve Lua's hash chains, traversal order, and length boundaries.
- Constructors flush positional fields in batches of 50. Later explicit fields
  can replace values from earlier batches, as confirmed by Redis probes.
- Numbers are float64. Strings are byte sequences and may contain zero bytes.
- Unknown escape letters follow Lua 5.1.
- Read-only writes fail with Redis's error. The standalone globals start writable;
  installation of the Redis sandbox belongs to B.
- Binary chunks are parsed as text and rejected.
- Weak k, v, and kv tables clear unreachable tables, functions, and threads
  during collectgarbage(), collectgarbage("collect"), and collectgarbage("step"). Strings and scalar
  values remain. Strong values keep weak keys alive, as in Lua 5.1.
- There is no automatic or incremental Lua collection. stop/restart do not
  change the explicit collection pass. step performs that full pass for any
  argument and returns true. Redis can return false without clearing entries
  for step with argument 0; this is owner-approved difference 11. count and gcinfo
  report Go allocation figures. Collection never requests a full Go GC.
- Hosts register external Lua roots with State.Retain and release them when
  no longer needed. Native arguments are roots while the function executes.
  Opaque Go captures and userdata are not inspected.
- Table finalizers do not execute, as in Lua 5.1.
- Coroutines use VM stacks. Unfinished coroutines close after the outer call.
- Native nesting and syntax limits match the captured wrapper probes.
  loadstring inherits the current C-call depth, including pcall and coroutines.
- Pattern recursion stops at 200,000 active match calls with `pattern too complex`.
  Redis can crash beyond this depth. Patterns with depth 150,000 still execute.
- Binary and suffix chains compile iteratively with reusable registers.
- string.rep polls the hook between blocks of at most 16 KiB.
- Strings have a default 512 MiB limit. Options.MaxString can reduce it.
  Redis has no corresponding limit.

Budget provides approximate accounting for new script tables, strings, and
closures. It is not an engine memory limit or a replacement for Go's allocator.
Engine accounting, reset policy, KILL, and BUSY remain part of E.

## Permitted differences

Object tostring values use stable IDs instead of machine addresses. Type
prefixes such as `table: 0x` match. Pointer-key table order may differ.
Numeric constants merge signed zero as Redis's compiler does. The specification also permits
compatible fpconv rounding and pooled random-state differences.

Arithmetic +, -, *, and / preserve the first NaN operand's sign and payload,
quiet signaling NaNs, and create a negative quiet NaN for invalid operations.
The behavior is explicit on every architecture; normalization.json is empty.
Modulo preserves separately rounded division, floor, multiplication, and
subtraction. Power follows the ARM routine's special-case rules, including its
odd-exponent sign adjustment. log10 returns positive NaN for negative inputs
and preserves an existing NaN operand.

The reference Docker image uses glibc 2.36. fmod(-nan, +nan) preserves its first
NaN operand there; glibc 2.39 returns positive NaN for this pair. The Docker
reference decides this case. One A3 snippet is captured from that image;
all other A3 and L1 captures agree between those glibc versions.

Lua modulo explicitly rounds its intermediate product to float64. This prevents
fused multiply/subtract instructions from changing Redis's result on ARM64.

String patterns operate on bytes with the C locale's character classes.
Formatting preserves C integer conversions, byte widths, zero termination,
and Lua 5.1's quoting rules. The random generator is state-local Redis rand48.
Table sorting follows auxsort, including its comparator call order.

exp, log, pow, and the power operator port ARM optimized-routines v21.02.
log10, sinh, cosh, and tanh use fdlibm algorithms above the ARM functions.
Explicit FMA, polynomial grouping, and separately rounded products reproduce
Redis's x86-64 FMA results on all supported Go architectures. The corpus checks
100,000 inputs per function and also checks the power operator on the pow grid.

Options.Math supplies sin, cos, tan, asin, acos, atan, and atan2. Nil fields fall
back to Go math. The default acos uses asin and sqrt near -1 and 1 to avoid
Go acos's endpoint cancellation. The defaults can differ from glibc; no ULP
normalization applies. Thirty-six exact trigonometric probes live separately in
testdata/libm.json for lua-libm integration tests. Reference CI still verifies
their captured answers against Redis. lua does not import the LGPL module.

string.dump serializes the canonical Redis Lua 5.1 prototype in the x86-64
format, including instructions, constants and debug information. Output matches
Redis byte for byte on every supported architecture. Binary loading is rejected,
including this output, as in Redis.

collectgarbage and gcinfo expose plausible Go memory statistics rather than the
Redis Lua allocator's exact figures. Lua controls do not stop Go's process-wide
collector.

## Planned engine differences

Pooled FUNCTION upvalues will be documented with FUNCTION.
