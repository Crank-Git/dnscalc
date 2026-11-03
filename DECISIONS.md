# Decisions

Rules the code follows, with the reason for each. Change a rule here in the
same PR that changes the code.

## Undefined results

- An operation with no real answer is an error, never a NaN and never a
  panic: a negative base with a non-integer exponent is
  `result is not a real number`; zero to a negative power and `x/0` are
  `division by zero`.
- The DNS handler is wrapped so that any panic that still slips through is
  logged and answered with SERVFAIL instead of killing the process.

Why: `math/big` panics on undefined operations and miekg/dns does not recover
handler panics, so one query could take the server down (#9).

## Value range

- Every value, intermediate or final, must satisfy
  2^-1048576 <= |v| < 2^1048576 (`maxMagnitudeBits`), or be zero. Outside
  that it is `result too large` / `result too small`, checked as soon as an
  operation produces the value.
- A power is estimated (`exp·log2|base|`) before it is computed, and a result
  far outside the range is rejected without computing it. Bases 0 and ±1 are
  answered directly for any exponent.
- The range is deliberately wider than the output cap (60000 characters), so
  intermediates such as `2^1000000/2^999990` still work.

Why: without a bound a single query could compute millions of digits or
allocate hundreds of MB before any other limit applied (#13). The bound keeps
every operation and every format cheap.

## Non-integer powers

- `base^exp` with a non-integer `exp` is `e^(exp·ln base)`, computed with
  `ln`/`exp` over `*big.Float` at 1024 + 64 guard bits and rounded to 1024.
  There is no float64 path.

Why: float64 gave 17 correct digits, overflowed for large bases and silently
returned 0 for small ones (#15).

## Output digits

- Integers print in full (up to the output cap).
- Everything else prints 300 significant digits, trailing zeros trimmed. A
  1024-bit mantissa holds ~308 digits; dropping the last few hides last-bit
  rounding noise, so `8^(1/3)` is `2` and `0.1+0.2` is `0.3`.
- Numbers below 2^-1100 are formatted by scaling with a power of ten, because
  `big.Float`'s exact conversion costs roughly the square of the exponent.

## Tetration

- Operator token is the ASCII word `tet` (case-insensitive like the other
  aliases). Right-associative. Binds tighter than `^`.
- Height (right operand) must be a non-negative integer, else an error.
- Computed by iterated power: x = 1, then repeat `height` times: x = a ^ x,
  using `powBig`. Every step is therefore subject to the value range: a step
  whose result would leave it is rejected before it is computed. There is no
  separate size threshold.
- A tower may stop early, for any height:
  - **Fixed point:** a step that leaves x unchanged means every later step
    does too, so the answer is x (`1 tet (10^300)` = 1,
    `(2^0.5) tet (10^300)` = 2).
  - **2-cycle:** a step that returns x to its value two steps earlier means
    the tower alternates between two values; the parity of the remaining
    height picks one (`0 tet n` alternates 1, 0, 1, ...; bases below
    e^-e ≈ 0.066 settle into a genuine 2-cycle).
- At most 10000 steps (`maxTetSteps`, about a quarter second). A tower that
  has not finished, settled or left the value range by then is
  `tetration does not settle within 10000 steps`. Only bases very close to
  e^(1/e) ≈ 1.4447 or e^-e ≈ 0.066 (e.g. 1.444, 0.07) get there.

Why: the original rule ("if result > 200000 return too large, base-
independent; never analyze feasibility") only stopped towers that grew. Every
base between e^-e and e^(1/e), plus 0 and -1, stays bounded, so
`1 tet 99999999` or `0.5 tet (10^300)` looped for minutes or forever (#17).
Settled towers now return in milliseconds with the right value.
