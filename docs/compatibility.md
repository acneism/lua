# Compatibility

The target is byte-for-byte compatibility with Redis 7.2.16 scripting replies,
errors, and host command order. The language target is Redis's Lua 5.1 variant.

A0 has no interpreter or engine. Verifying the reference corpus proves that the
recorded answers match Redis. It does not prove that this library executes Lua.
The local interpreter corpus runner will be introduced in A1 and extended in A2.

The specification permits address normalization for objects, pointer-key table
iteration differences, initial random state differences between pooled states,
negative-zero constant folding differences, equivalent fpconv rounding, and
platform-dependent NaN signs. A0 verification does not apply normalization:
the supplied corpus has stable answers under the capture wrapper.

Planned intentional differences must be documented when implemented:

- FUNCTION library upvalues belong to each pooled state.
- EVAL cache entries use bounded LRU eviction, unlike Redis 7.2.
- String size and script memory limits protect the host.
- Hosts may opt into killing scripts that have issued writes.

These features are not implemented in A0.
