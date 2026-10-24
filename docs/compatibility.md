# Compatibility

The target is Redis 7.2.16 and its Lua 5.1 variant. A2 implements the interpreter
and base, coroutine, string, table, and math libraries. EVAL, FUNCTION, host commands,
and C extensions are not implemented yet.

All 3105 reference snippets execute; `testdata/pending.json` is empty.
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
- Weak-table and finalizer fields are accepted without weak references or
  Lua finalizer execution.
- Coroutines use VM stacks. Unfinished coroutines close after the outer call.
- Native nesting and syntax limits match the captured wrapper probes.
- Strings have a default 512 MiB limit. Options.MaxString can reduce it.
  Redis has no corresponding limit.

Budget provides approximate accounting for new script tables, strings, and
closures. It is not an engine memory limit or a replacement for Go's allocator.
Engine accounting, reset policy, KILL, and BUSY remain part of E.

## Permitted differences

Object tostring values use stable IDs instead of machine addresses. Type
prefixes such as `table: 0x` match. Pointer-key table order may differ.
Numeric constants may merge signed zero. The specification also permits
equivalent fpconv rounding, pooled random-state differences, and
platform-dependent NaN signs. Five sources are explicitly listed for NaN-sign
normalization on arm64 in testdata/normalization.json. All other fields still compare
exactly. A separate test requires formatting to preserve the actual NaN sign.

Lua modulo explicitly rounds its intermediate product to float64. This prevents
fused multiply/subtract instructions from changing Redis's result on ARM64.

String patterns operate on bytes with the C locale's character classes.
Formatting preserves C integer conversions, byte widths, zero termination,
and Lua 5.1's quoting rules. The random generator is state-local Redis rand48.
Table sorting follows auxsort, including its comparator call order.

string.dump serializes this VM's immutable register program after the LuaQ
signature. Its payload is specific to this VM rather than Redis's instruction
format. Binary loading is rejected, including this output, as in Redis.

collectgarbage and gcinfo expose plausible Go memory statistics rather than the
Redis Lua allocator's exact figures. Lua controls do not stop Go's process-wide
collector.

## Planned engine differences

Pooled FUNCTION upvalues, bounded EVAL cache eviction, script memory limits,
and opt-in killing of writers will be documented when implemented.
