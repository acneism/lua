# 12. Dispatch loop, opcode encoding and P2 execution paths

Status: implemented in PR P2.

## Opcode encoding

The compiler keeps the canonical Redis program; `freeze` translates every
instruction into one uint64 word, so VM PCs equal Lua PCs. Byte zero holds the
opcode and byte one register A. The remaining bytes depend on the opcode:

| Opcodes | Layout |
| --- | --- |
| generic | B at bits 16–47 (`uint32`, RK constants as `-1-k`), C at bits 48–63 (`int16`) |
| `opCall`, `opReturn` | B at 16, C at 32, D (result base) at 48 |
| arithmetic and numeric comparison | left operand at 16, right operand at 32 (`uint16` each) |
| `opCompare` family | left operand at 8, right operand at 16 |

Named constants describe the numbering in `bytecode.go`. The plain opcodes run
from zero to `opLength`. Arithmetic and numeric comparison opcodes start at
`numericBase`, which is aligned to `operandModes`. Each operation (`operationAdd`
… `operationNotEqual`) occupies four opcodes, one per combination of
`leftConstant` and `rightConstant`. `numericOpcode`, `numericOperation`,
`arithmetic` and `numericCompare` encode and decode them. A numeric comparison
that writes the register tested by the next conditional jump becomes the same
comparison at `numericJumpBase` (`withJump`/`withoutJump`) and consumes that
jump. The `opCompare` family at `comparisonBase` packs `compareEqual`,
`compareLess` or `compareLessEqual`, the two constant flags and `expectTrue`
(the A operand of EQ/LT/LE) through `comparisonOpcode` and `comparisonKind`.

The numbering is dense: the largest opcode is 139. Go builds a jump table for a
switch only when the case range is at most four times the number of merged
case ranges. The earlier layout reached 215 and compiled to a binary search of
five to eight compares per instruction.

## Dispatch loop

`execute` keeps the frame state (`frameIndex`, `f`, `base`, `code`, `constants`,
`registers`) constant inside the inner loop. Every path that enters or leaves a
frame, polls the hook or may move the stack writes the frame PC and `s.steps`
and continues the outer loop, which reloads the state from the top frame.
Go has no callee-saved registers. A value that changes inside the loop and is
needed after a call in more than one case is spilled at the dispatch point on
every instruction. Before this change each instruction stored about thirteen
values. Now only the PC and the step budget change per instruction. On the
EPYC 7763 runner ArithConst fell from 1.52× to 1.13× Redis, WhileLoop from
1.47× to 1.06× and Loop to 0.63×.

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
| Loop | 2 | 1 |
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
| WhileLoop | 5 | 4 |
| Branches | 12 | 10 |
| CallTwoArgs | 6 | 6 |
| Upvalues | 4 | 2 |
| MethodCall | 4 | 4 |
| MultiReturn | 6 | 4 |
| Varargs | 7 | 7 |
| NativeCalls | 12 | 11 |
| ClosureCreate | 6 | 6 |
| ArrayReadWrite | 3 | 3 |
| FieldAccess | 5 | 5 |
| TableLibInsert | 6 | 6 |
| Ipairs | 2 | 2 |
| SmallTables | 9 | 9 |
| StringKeys | 3 | 3 |
| KeyBuild | 7 | 7 |
| StringMethods | 11 | 11 |
| StringFormat | 10 | 10 |
| Match | 10 | 10 |
| IndexInheritance | 4 | 4 |
| IndexFunction | 3 | 3 |
| NewIndexFunction | 2 | 2 |
| CompareMetamethod | 4 | 3 |
| CallMetamethod | 5 | 3 |
| PCall | 8 | 8 |
| Coroutine | 4 | 4 |
