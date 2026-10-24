# 6. Reference floating-point math

Status: accepted in A2; arithmetic and FMA audit completed in A3.

## Decision

Port exp, log, and pow from ARM optimized-routines v21.02 under MIT.
Use fdlibm's log10, sinh, cosh, tanh, and expm1 algorithms under Sun's
permissive notice. These functions call the ARM exp and log implementation.
The power operator and math.pow share the same implementation.

Store lookup tables as constant strings of IEEE-754 bits. Decode entries with
encoding/binary and math.Float64frombits. This avoids mutable package state,
unsafe pointers, and architecture-specific table initialization.

Match the reference's arithmetic order with math.FMA where its x86-64 FMA
variant contracts a multiply and add. Use explicit float64 rounding elsewhere.
In particular, log10 uses separately rounded products, the near-one log
polynomial contracts its final product with the low correction, and exp's
negative large-argument path reuses a separately rounded scale product.
The near-one logarithm contracts both uses of the shared squared-high product.
The pow logarithm contracts its final ar3 product with the accumulated low sum.
expm1 groups the same fdlibm coefficients as the reference evaluation.

Expose Math through Options.Math for sin, cos, tan, asin, acos, atan, and atan2.
Copy supplied functions into each state's native library closures. Nil fields
use Go math, with acos computed through asin near its endpoints. Exact glibc
trigonometry belongs to the separately licensed lua-libm module; lua has no
production dependency on it.

## Evidence

The main corpus includes 100,000 deterministic inputs per function with %.17g
answers captured from Redis 7.2.16 on the reference Azure VM. The pow grid also
checks the power operator. The earlier 3105 answers remain unchanged.
Reference CI replays these grids against redis:7.2.16; interpreter CI executes
them on Linux, Windows, and macOS. Thirty-six exact trigonometric snippets are
stored in testdata/libm.json for the external module's integration tests.

## Consequences

Correct rounding alone cannot reproduce glibc's observable output. Table
constants, polynomial grouping, and FMA boundaries are compatibility decisions.
Changes require a new reference comparison. Default trigonometry remains an
explicit compatibility difference until the host supplies Math functions.

## Final assembly audit

The A3 audit uses GCC 13.3.0, -O2 -mfma -ffp-contract=fast,
-fno-math-errno, and -fno-trapping-math. Inspection covers final assembly,
including products used in more than one addition or subtraction. It does not
infer machine contractions from GIMPLE use counts. The x86-64 multiarch
Makefile selects FMA variants for exp, log, pow, and expm1. log10, sinh, cosh,
and tanh use baseline variants; compiling their sources with -mfma alone does
not establish which variant the reference dispatches.

| Routine | Shared products and final instructions |
| --- | --- |
| exp and pow exponential | r2 also feeds polynomial products, so it stays rounded. The negative special-case scale*tmp remains MUL followed by separate ADDs, including its conditional correction. The positive special case and ordinary final scale addition use FMA. |
| log near one | r*2^27 contracts into both the splitting addition and subtraction. The rounded rhi*rhi feeds B[0] multiplication; that second product contracts independently into hi and lo. |
| log general | r2 also feeds r*r2 and polynomial products and stays rounded. Each single-use polynomial product contracts as in the assembly. |
| pow logarithm | ar2 also feeds ar3, the polynomial, and the explicit residual FMA, so it stays rounded. ar3*p contracts with lo1+lo2+lo3+lo4; it must not be rounded first. |
| expm1 | hfx, hxs, h2, and h4 have non-additive uses and stay rounded. The coefficient chains, range-reduction hi, denominator, and reconstruction additions use FMA. The shared lo product and invln2*x across control flow remain separate MULs in final assembly. |
| log10 | The dispatched baseline rounds all products separately. Its negative-domain wrapper returns positive NaN; NaN operands retain their sign. |
| sinh, cosh, tanh | The baseline retains separately rounded products. Power-of-two factors are exact in the relevant branches. Calls to exp and expm1 use their dispatched implementations. |

Assembly checks use the ARM v21.02 source files recorded in NOTICE and glibc
2.39's five fdlibm-derived source files. A standalone word-extraction shim
allows the latter sources to compile without glibc's internal build headers;
the numeric expressions and their control flow are unchanged. This audit does
not copy LGPL trigonometric code into lua.
