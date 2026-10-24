# Reference corpus

The initial `testdata/snippets.json` was copied byte for byte from the supplied
`tz/lua/corpus/snippets.json`. It contains 707 entries captured on 2026-10-24
with Redis 7.2.16. No expected answer was rewritten during import.

The imported file's SHA-256 is
`b442925e7abca89031bbb849df933695eb9910b44cf6aa26ea1fa53b21ac9910`.

A1, A2, and A3 add 3451 entries captured by the same tool from Redis 7.2.16, for a total
of 4158. The original 707 sources and answers remain unchanged. The new cases
cover coercion, arithmetic, table traversal, constructor batches, closures,
varargs, error frames, coroutine boundaries, and parser and call limits.

P adds 27 further Redis captures covering compact operand modes, direct calls,
metamethod continuation and cache invalidation, source locations, and dense and
sparse sequence lengths. All 4185 main entries execute;
`testdata/pending.json` is empty.
Reference verification always checks the complete file. Each execution has a
one-million-instruction hook budget, so a broken limit fails its own subtest.
Three million-operation reviewer snippets have explicit two-million-step
budgets in testdata/limits.json. Four larger L1 grids have explicit
four-million-step budgets in testdata/libm.limits.json. Every metadata entry
must be used by a requested snippet; unused entries, duplicates and invalid
limits fail validation.

The A2 review adds 324 main snippets and 16 exact trigonometric snippets in
testdata/libm.json. All 340 reviewer sources and answers remain represented.
The trigonometric file is validated separately and reserved for lua-libm tests;
TestCorpus executes the main file. Reference CI verifies both against Redis.

Seven deterministic LCG grids add 700 main snippets with 1000 inputs each:
100,000 inputs per exp, log, log10, pow, sinh, cosh, and tanh. Each answer uses
%.17g. The pow grid checks both math.pow and the power operator. Inputs cover
near-one reduction, broad exponent ranges, subnormals, overflow, and signed
integer powers. The generator is scripts/math-grids.py; every answer comes
from capture running against Redis on the Azure reference VM.

NaN results are now explicit on every architecture. normalization.json is
empty. Earlier recorded answers are unchanged. A3 adds 29 reviewer probes;
L1 adds 20 exact trigonometric probes. They were captured with Redis 7.2.16.
One new A3 fmod answer was recaptured from the CI Docker image using glibc 2.36,
which takes precedence over glibc 2.39 for its two-NaN case. The reference job
publishes its actual capture as a seven-day artifact before verification.

The test-only package github.com/acneism/lua/luatest exposes:

```go
func RunCorpus(t *testing.T, files []string, options lua.Options)
```

It accepts relative or absolute JSON fixture paths. Each fixture directory must
contain limits.json and normalization.json. Files in one directory share one
copy of that metadata. A fixture can also supply a sibling file named
<fixture>.limits.json for budgets specific to that fixture. These budgets load
only when that fixture is requested, so exact libm budgets do not become stale
entries in a main-only run. All shared metadata must match the requested files.
The supplied Options.Math functions, memory budget,
string limit, and host hook remain active. A bounded hook also limits each
snippet. The runner creates the same read-only snippet environment and separate
outer capture helper as the root corpus tests.

TestCorpus uses RunCorpus for snippets.json. The separate lua-libm integration
uses it for both snippets.json and libm.json with Options.Math. No production
module dependency is needed in either repository.

The command uses the specification's wrapper unchanged:

```lua
local function __capture__(ok, ...)
  if not ok then return "ERR " .. tostring((...)) end
  local n = select('#', ...)
  local out = {}
  for i = 1, n do out[i] = tostring((select(i, ...))) end
  return table.concat(out, ",")
end
local f, e = loadstring(%s, "@user_script")
if not f then return "ERR " .. tostring(e) end
return __capture__(pcall(f))
```

`%s` is replaced with a Lua 5.1 quoted source string. Quotes, backslashes,
newlines, carriage returns, and control bytes are escaped. Decimal byte escapes
have three digits so a following digit cannot change their meaning.

The tool sends `EVAL wrapper 0` and removes one leading `ERR ` from the bulk
reply, as the original capture tool does. It preserves all other bytes.
`-verify` compares the result to `want` without modifying the corpus.

JSON cannot preserve an invalid UTF-8 result. Capture rejects such output rather
than replacing its bytes. Binary probes encode results as hexadecimal inside
Lua. The binary syntax-token tests compare raw Go results to those captured
hexadecimal answers. Their string-library encoders also execute in the corpus.

The interpreter corpus runner executes this same wrapper. The future EVAL
environment must preserve it: calling snippets directly changes protected-call
and error-level semantics and does not satisfy the corpus contract.
