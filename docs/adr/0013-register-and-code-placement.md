# 13. Registers and code in separate halves of a page

Status: implemented.

## Context

ArithConst was bimodal between processes on one runner: 0.75 and 1.46 to 1.67 on
EPYC 7763, 0.85 and 1.88 on EPYC 9V74, 0.97 and 1.69 on Xeon 8370C. The dispatch
loop stores a register and then reads the next code word, and that read can wait
for the unrelated store. It takes two conditions:

- the register and the code word share their low 12 address bits. In the benchmark
  the stack started at page offset 776 and the code at 720, so `code[7]` met R0;
- their physical pages match in further bits. The operating system assigns the
  pages once per process, and Go cannot choose them.

In 350 benchmark processes, all 17 slow ones recovered when the Lua stack or the
code moved. Moving the constants, the frames, the thread, the `State` or the Go
stack never helped. Without the overlap, 480 processes had no slow one.

## Decision

Keep the low 12 bits apart, so the physical pages no longer matter. The placement
follows from Go's size classes and needs neither `unsafe` nor `reflect`.

- A thread's first stack has 341 values. With the 8-byte malloc header that fills
  the 8192-byte class, whose objects start at multiples of 8192. Growth doubles the
  length, which stays on such classes and then on page-aligned large objects. R0 of
  the first frame lands at page offset 8, and the first 85 registers stay in the
  lower half of a 4 KiB page.
- One compilation takes its code from 4096-byte blocks, whose objects start at
  multiples of 4096, and uses only their upper half of 256 words.

`TestRegistersAndCodeUseSeparatePageHalves` fails if a toolchain changes these
classes. Execution stays correct in that case, and only the slow mode returns.

## Measurements

df4c200 against 8ab7393 on 24 GitHub Actions runners, 12 alternating fresh processes
of each version, 43 scenarios. ArithConst, Go time divided by Redis time:

| Processor | Processes | Before: median, worst | After: median, worst |
| --- | ---: | ---: | ---: |
| EPYC 7763 | 108 | 0.75, 1.67 | 0.75, 0.82 |
| EPYC 9V74 | 84 | 0.85, 1.88 | 0.84, 0.91 |
| EPYC 9V45 | 36 | 1.18, 1.77 | 1.04, 1.06 |
| Xeon Platinum 8370C | 36 | 0.97, 1.69 | 0.94, 0.95 |
| Xeon Platinum 8573C | 24 | 0.98, 1.82 | 0.98, 1.07 |

21 of the 288 processes were over 1.3 before, none after.

The two binaries also differ in code layout. On EPYC 7763 StructRoundTrip took 1.13
times as long and RedisCall 0.91 times. A second run switched only the placement
inside one binary, on 12 runners with EPYC 7763, 9V45, 9V74 and Xeon 8573C. StructRoundTrip,
RedisCall, GSub and IndexFunction then stayed within 1% in the median per processor, so
those shifts come from the layout. Xeon 8370C was not in that run.

## Consequences

- A small compilation retains about 4 KB more: 1.3 to 5.3 KB for `return 1`, 7.8 to
  11.6 KB for a short script with three `redis.call`. 100 functions went from 256 to
  265 KB. Compilation took about 2% longer.
- Creating and finishing a coroutine went from 4.4 to 5.9 µs and from 7.1 to 8.8 KB.
  Its stack is larger and takes an allocator span of its own.
- Registers after R84 and a function of more than 256 words can still overlap, so
  deep recursion and very long functions keep the draw.
- Only this pair is covered. Other loads and stores can still conflict: excluding
  ArithConst, processes more than 15% above their runner's median went from 145 to
  125 of about 12,000, and single-process values over 1.5 stayed at 7 apart from one
  runner where IndexFunction was 1.7 in all 12 old processes.
