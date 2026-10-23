# Reference corpus

The initial `testdata/snippets.json` is copied byte for byte from the supplied
`tz/lua/corpus/snippets.json`. It contains 707 entries captured on 2026-10-24
with Redis 7.2.16. No expected answer was rewritten during import.

The imported file's SHA-256 is
`b442925e7abca89031bbb849df933695eb9910b44cf6aa26ea1fa53b21ac9910`.

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

The future interpreter corpus runner must execute this same wrapper in the EVAL
environment. Calling snippets directly changes protected-call and error-level
semantics and does not satisfy the corpus contract.
