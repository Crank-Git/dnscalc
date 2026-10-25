# Decisions

## tetration (M4a)

RULES (final; do not re-derive, re-trace, or compare alternatives):
- Operator token is the ASCII word `tet` (case-insensitive like the other
  aliases). Right-associative. Binds tighter than ^.
- Height (right operand) must be a non-negative integer, else return an
  error. Compute by iterated power only: result = 1, then repeat b times:
  result = a ^ result (use the existing powBig).
- Before each power step, if result > 200000, return errResultTooLarge
  immediately WITHOUT computing the power. Base-independent. Never analyze
  whether a bigger tower is "feasible".
- Existing output cap (60000 chars, maxResultDigits) is unchanged.
