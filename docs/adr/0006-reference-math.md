# 6. Reference floating-point math

Status: proposed for A2 review.

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
them on Linux, Windows, and macOS. Sixteen exact trigonometric snippets are
stored in testdata/libm.json for the external module's integration tests.

## Consequences

Correct rounding alone cannot reproduce glibc's observable output. Table
constants, polynomial grouping, and FMA boundaries are compatibility decisions.
Changes require a new reference comparison. Default trigonometry remains an
explicit compatibility difference until the host supplies Math functions.
