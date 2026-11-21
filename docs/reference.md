# dnscalc reference

Operators, aliases, limits, output formatting, and what the server answers. The
reasons behind each rule are in [DECISIONS.md](../DECISIONS.md).

## Operators

Hand-written recursive-descent parser over `*big.Float` at 1024-bit precision
(no eval, no shelling out, no expression library):

```
+  -  *  /  ^  tet  ( )
```

- Unary minus is supported.
- Non-integer powers are computed as `e^(exp·ln base)` at full precision
  (`2^0.5` is √2 to 300 digits, `(10^400)^0.5` is exactly 10^200). A
  negative base with a non-integer exponent is an error.
- `^` is right-associative and binds **tighter** than unary minus, so
  `-2^2 == -4` (i.e. `-(2^2)`) and `2^3^2 == 512` (i.e. `2^(3^2)`).
- `tet` (tetration, `a tet b` = a↑↑b) binds **tighter** than `^`, so
  `2 tet 3 ^ 2 == 256` (i.e. `(2 tet 3)^2`) and `2 ^ 3 tet 2 == 134217728`
  (i.e. `2^(3 tet 2)`). The height must be a non-negative integer. Towers
  that settle return for any height: `1 tet (10^300)` is `1`,
  `(2^0.5) tet (10^300)` is `2`, `0.5 tet (10^300)` is `0.641185744…`,
  and `0 tet n` alternates `1, 0`. A tower is limited to 10000 steps; see
  `DECISIONS.md` for the full rules.
- Precedence, low to high: `+ -`, then `* /`, then unary `-`, then `^`, then
  `tet`, then parentheses/atoms.

## Word aliases

Case-insensitive, hostname-safe aliases. Resolvers may randomize case with
`0x20`, so both the aliases and the zone match in any case
(`2.PlUs.3.CaLc.TeSt` works, and `-zone` may be given in any case); the
answer echoes the name in the case it was asked.

| alias   | operator |
|---------|----------|
| `plus`  | `+`      |
| `minus` | `-`      |
| `times` | `*`      |
| `over`  | `/`      |
| `pow`   | `^`      |
| `tet`   | `tet`    |
| `lp`    | `(`      |
| `rp`    | `)`      |

```
$ dig @127.0.0.1 -p 8053 +short TXT '2.plus.3.calc.test'
"5"
```

## Limits

- Name length is bounded by DNS itself (255 bytes).
- At most **64 tokens** per expression.
- Parenthesis depth at most **32**.
- **Magnitude range.** Every value, intermediate or final, must lie within
  2^±1048576 (about 315,000 digits either side of the point); outside that
  the answer is `error: result too large` / `error: result too small`. The
  check runs as soon as an operation produces a value, and powers are
  estimated before they are computed, so `2^2^2^30` or `0*2^2^31` is
  rejected in microseconds instead of being worked out. Intermediates may
  exceed the output cap: `2^1000000/2^999990` is `1024`. Bases `0` and `±1`
  are answered directly for any exponent (`1^(10^300)` is `1`).
- A negative base with a non-integer exponent (`(0-2)^0.5`) has no real
  result and returns `error: result is not a real number`; `0^-1` is a
  division by zero.
- **Tetration** is limited to 10000 power steps (about a quarter second);
  see `DECISIONS.md`.
- **Output cap.** The result string is capped at **60000 characters**
  (`maxResultDigits`); a longer one returns `error: result too large` rather
  than overflowing the packet. This keeps the whole DNS message under the
  65535-byte limit, the 16-bit length field on both the UDP datagram and the
  TCP frame.
- **Long answers.** A TXT character-string holds at most 255 bytes, so a
  longer result is split into consecutive strings of one TXT record
  (`dig +short` prints them as `"…" "…"`; join them to get the number). Over
  UDP the reply is sized to the client's buffer, 512 bytes or its EDNS0
  size; an answer that does not fit comes back empty with TC set and clients
  such as `dig` retry over TCP automatically. A 60000-digit answer with a
  200-byte query name is about 60.5 KB over TCP.
- **Precision ceiling.** The core is a single `*big.Float` at 1024-bit
  precision, so results are exact only while the true value needs ≤ 1024
  significant bits (~309 decimal digits for a general integer). Larger
  integers are rounded to 1024-bit precision and still printed in full — the
  leading ~309 digits are right and the rest are not. Powers of two stay
  exact at any magnitude (their mantissa is a single bit), so `2^199313`
  (60000 digits) is exact but `2^199316` is rejected only by the output cap.

## Output formatting

Integral results print with no decimal point (`14`, not `14.000000`);
everything else prints **300 significant digits** with trailing zeros
trimmed (`%g`, precision 300). The 1024-bit mantissa holds ~308 digits, so
the last few, where rounding noise lives, are dropped: `2^0.5*2^0.5` is `2`,
`1/3*3` is `1`, `0.1+0.2` is `0.3`. Numbers smaller than 2^-1100 print in
scientific notation via a scaled conversion (`2^-200000` →
`1.0019988054061873...e-60206`), so formatting cost does not grow with how
small the number is.

## Response behavior

| query                                   | rcode     | answer                                   |
|-----------------------------------------|-----------|------------------------------------------|
| TXT, valid expression under the zone    | NOERROR   | TXT `<result>`, TTL 3600                 |
| TXT, invalid expression under the zone  | NOERROR   | TXT `error: <reason>`, TTL 60            |
| other qtype under the zone              | NOERROR   | empty answer (NODATA), zone SOA in authority |
| SOA at the zone apex                    | NOERROR   | the zone SOA                             |
| name outside the zone                   | REFUSED   | —                                        |
| internal error (handler panic)          | SERVFAIL  | —                                        |

Responses set the AA flag. A UDP reply too large for the client's buffer
has TC set and no answer; retry over TCP. A query with EDNS0 gets an OPT
record back.
