# 12. Dispatch loop, opcode encoding and P2 execution paths

Status: implemented in PR P2.

## Opcode encoding

The compiler keeps the canonical Redis program; `translateRedis` and `freeze`
turn every instruction into one uint64 word. VM PCs differ from Lua PCs:
translation inserts `opPoll` and expands `LOADBOOL`, and `rawPC` maps back. Byte
zero holds the opcode and byte one register A. The remaining bytes depend on the
opcode:

| Opcodes | Layout |
| --- | --- |
| generic | B at bits 16–47 (`uint32`, RK constants as `-1-k`), C at bits 48–63 (`int16`) |
| `opCall`, `opReturn` | B at 16, C at 32, D (result base) at 48 |
| arithmetic | left operand at 16, right operand at 32 (`uint16` each) |
| `opCompare` family | left operand at 8, right operand at 16 |
| `opForIterator` | exit target at 16, result count at 48 |
| `opSelf` | key at 48, number of fused arguments at 24 |
| `opCallFused`, `opCallFusedUpvalue` | function at 16, argument count at 24, one byte per argument from 32 |

A fused call replaces the `MOVE` or `GETUPVAL` that loads the function. An
argument byte below `fusedConstant` is a register, a byte up to
`fusedArithmetic` is a constant, and `fusedArithmetic` itself means that the
argument is the add, subtract or multiply word at its place in the stream. The
loads and the `CALL` stay in the code: when the fast path does not apply (a
callee that needs more stack, operands that are not numbers), the word acts as
the load it replaced and execution continues through them, so metamethods and
errors stay where Redis has them.

Named constants describe the numbering in `bytecode.go`. The plain opcodes run
from zero to `opLength`. Arithmetic opcodes start at `numericBase`, which is
aligned to `operandModes`. Each operation (`operationAdd` … `operationPower`)
occupies four opcodes, one per combination of `leftConstant` and `rightConstant`.
`numericOpcode` and `numericOperation` encode and decode them. The `opCompare`
family at `comparisonBase` packs `compareEqual`, `compareLess` or
`compareLessEqual`, the two constant flags and `expectTrue` (the A operand of
EQ/LT/LE) through `comparisonOpcode` and `comparisonKind`.

The numbering is dense: the largest opcode is below 100. Go builds a jump table
for a switch only when the case range is at most four times the number of merged
case ranges. The earlier layout reached 215 and compiled to a binary search of
five to eight compares per instruction.

## Dispatch loop

Go has no callee-saved registers, and its register allocator spills a value at
the closest block that dominates all its restores. A value defined by the
dispatch (`word`, `pc`) and restored in two switch cases is therefore stored on
every instruction, and a restore can come from a call or from plain register
pressure: with no free register a shift clobbers its input. Thirteen such stores
per instruction were cut to four, and this design removes the rest. The dispatch
block now only checks the bound, loads the word, increments the PC and jumps.

Three rules keep it that way:

- Rare frame state lives in the `activation` struct. It has five fields, so the
  compiler keeps it in stack memory and does not assign it registers. A case
  loads what it needs, at the cost of the restore it replaces. The registers of
  the loop hold only `s`, `pc`, `code`, `constants` and `registers`, which leaves
  room for temporaries.
- Nothing decoded from `word` and no `pc` is used after a call. Before a call a
  case stores the PC in the frame. Afterwards it reloads the PC from the frame or
  continues the outer loop. Helpers read their operands from the frame through
  `proto.code[pc-1]`.
- In heavy cases, late uses read the instruction stream again (`code[pc+count]`,
  `code[pc-1]` after a store) instead of keeping `word` alive.

Any edit can bring a spill back, so check `go tool objdump` of `execute` after
changing it. The first block after `word := code[pc]` must contain no store to
the stack.

Calls and returns do not maintain `t.top` on fast paths. It is meaningful only
between an instruction with open results and its consumer, and calls with a fixed
argument count start the new frame at `t.used`. A return with `want >= 0` needs
no check for the bottom frame of `execute`, because the outer loop exits by
itself when the frame count falls below `stop`.

`execute` must stay under the inliner's 5000-node limit for big functions.
Above it Go inlines only callees of cost 20 or less, and helpers such as
`luaNaN` become calls in every arithmetic case. CI fails when the compiler
reports any function as big.

The hook budget is polled in one place per kind of instruction. Backward jumps
call `pollJump`, which records the jump PC for the hook and then the target.
Calls and numeric for loops call `pollRetry`, which polls and re-executes the
instruction with the budget pre-charged. Both preserve the line seen by the hook
and the step count.

## Metamethods and calls

Metamethods are found through a per-state cache keyed by metatable and version.
Lua metamethods for arithmetic, `__index`, `__newindex`, `__call`, `__eq`,
`__lt` and `__le` run as frames of the same loop. A comparison frame carries its
continuation in `want` (`compareWant` minus the expected result and the swap
flag of `__le` through `__lt`); its return picks the branch with `jumpOn`.
`enterPair` builds a two-argument metamethod frame directly; rare cases (hook,
C depth, vararg callee, stack growth) go through `enterMethod`.

Go functions return through the state's result buffer, so string, math and base
functions do not allocate result slices. `pcall` calls a Go function on its
arguments in place when they end the current frame. The generic `for` steps
the `ipairs` iterator inline. Vararg functions without an `arg` table take the
short call path.

## Tables

The array part of a table holds plain `float64` values while it contains only
numbers and nil. Nil is a signaling NaN with a fixed payload that arithmetic
never produces. Storing another type, or a number with exactly that payload,
converts the array to `[]Value` once. The array then has no pointers for the
collector to scan and no write barriers. Memory reservations use the sizes of
`float64`, `Value` and `tableNode`, and a test compares them with the allocator.

## Instructions in the hot loop

The table counts the innermost loop of each scenario, from the backward jump
target to the jump, in the canonical program (identical to `luac -l` of Redis
7.2.16) and in the VM. A superinstruction counts once; words it consumes are not
counted. Fib27 and GSub have no loop in the main chunk.

| Scenario | luac | VM |
| --- | --- | --- |
| Loop | 2 | 2 |
| Fib27 | - | - |
| LuaCalls | 5 | 3 |
| TableInsert | 4 | 4 |
| TableHash | 3 | 3 |
| Sort | 3 | 3 |
| GSub | - | - |
| StringConcat | 5 | 5 |
| Metamethod | 2 | 2 |
| ArithConst | 3 | 3 |
| TwoAccumulators | 3 | 3 |
| WhileLoop | 5 | 5 |
| Branches | 12 | 12 |
| CallTwoArgs | 6 | 3 |
| Upvalues | 4 | 2 |
| MethodCall | 4 | 2 |
| MultiReturn | 6 | 4 |
| Varargs | 7 | 3 |
| NativeCalls | 12 | 12 |
| ClosureCreate | 7 | 5 |
| ArrayReadWrite | 3 | 3 |
| FieldAccess | 5 | 5 |
| TableLibInsert | 6 | 6 |
| Ipairs | 2 | 2 |
| SmallTables | 9 | 9 |
| StringKeys | 3 | 3 |
| KeyBuild | 7 | 7 |
| StringMethods | 11 | 6 |
| StringFormat | 10 | 10 |
| Match | 10 | 8 |
| IndexInheritance | 4 | 3 |
| IndexFunction | 3 | 3 |
| NewIndexFunction | 2 | 2 |
| CompareMetamethod | 4 | 4 |
| CallMetamethod | 5 | 3 |
| PCall | 8 | 8 |
| Coroutine | 4 | 3 |
