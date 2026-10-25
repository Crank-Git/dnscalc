# dnscalc

dnscalc is a DNS server that evaluates math. Query a TXT record whose name
is an expression; the answer is a TXT record holding the result.

```
$ dig @127.0.0.1 -p 8053 +short TXT '2+3*4.calc.test'
"14"
$ dig @127.0.0.1 -p 8053 +short TXT '1.5*2.calc.test'
"3"
$ dig @127.0.0.1 -p 8053 +short TXT '1/0.calc.test'
"error: division by zero"
```

## Build & run

```
$ go build -o dnscalc .
$ ./dnscalc            # serves 127.0.0.1:8053 under zone calc.test.
```

Flags:

| flag    | default         | meaning                                   |
|---------|-----------------|-------------------------------------------|
| `-listen` | `127.0.0.1:8053` | address to serve on (`host:port`)        |
| `-zone`   | `calc.test.`    | zone suffix expressions hang under        |

Serves both UDP and TCP. Every query logs one line:

```
2026/01/01 12:00:00 qname=2+3*4.calc.test. qtype=TXT rcode=NOERROR result=14
```

## How names map to expressions

The expression is every label before the zone suffix, joined with `.`. That
puts decimals back together: labels `1` and `5*2` become `1.5*2`.

```
2.plus.3.calc.test   ->   2.plus.3
1.5*2.calc.test      ->   1.5*2
```

The `.` is a separator only when it sits between two *words*; between two
digits it is a decimal point. This rule is documented and tested in
`evaluator.go`.

## Operators

Hand-written recursive-descent parser over `*big.Float` at 1024-bit precision
(no eval, no shelling out, no expression library):

```
+  -  *  /  ^  tet  ( )
```

- Unary minus is supported.
- `^` is right-associative and binds **tighter** than unary minus, so
  `-2^2 == -4` (i.e. `-(2^2)`) and `2^3^2 == 512` (i.e. `2^(3^2)`).
- `tet` (tetration, `a tet b` = a↑↑b) binds **tighter** than `^`, so
  `2 tet 3 ^ 2 == 256` (i.e. `(2 tet 3)^2`) and `2 ^ 3 tet 2 == 134217728`
  (i.e. `2^(3 tet 2)`).
- Precedence, low to high: `+ -`, then `* /`, then unary `-`, then `^`, then
  `tet`, then parentheses/atoms.

## Word aliases

Case-insensitive, hostname-safe aliases (resolvers may randomise case with
`0x20`, so matching is case-insensitive):

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
- An `inf`/`NaN` result is reported as an error.

- **Output cap.** The result string is capped at **60000 characters**
  (`maxResultDigits`); a longer one returns `error: result too large` rather
  than overflowing the packet. This keeps the whole DNS message under the
  65535-byte limit: a 60000-character answer comes back as a ~60063-byte
  message (~63 bytes of header/question/answer overhead for a typical query,
  which grows with the query name). 65535 is a hard structural ceiling, not a
  policy choice — it is the 16-bit length field on both the UDP datagram and
  the TCP frame.
- **Precision ceiling.** The core is a single `*big.Float` at 1024-bit
  precision, so results are exact only while the true value needs ≤ 1024
  significant bits (~309 decimal digits for a general integer). Larger
  integers are rounded to 1024-bit precision and still printed in full — the
  leading ~309 digits are right and the rest are not. Powers of two stay
  exact at any magnitude (their mantissa is a single bit), so `2^199313`
  (60000 digits) is exact but `2^199316` is rejected only by the output cap.

## Output formatting

Integral results print with no decimal point (`14`, not `14.000000`);
everything else uses the shortest round-tripping representation
(`strconv` `%g`, precision `-1`).

## Response behaviour

| query                                   | rcode     | answer                                   |
|-----------------------------------------|-----------|------------------------------------------|
| TXT, valid expression under the zone    | NOERROR   | TXT `<result>`, TTL 3600                 |
| TXT, invalid expression under the zone  | NOERROR   | TXT `error: <reason>`, TTL 60            |
| other qtype under the zone              | NOERROR   | empty answer (NODATA)                    |
| name outside the zone                   | REFUSED   | —                                        |

Responses set the AA flag.

## Testing

```
$ go test ./...            # unit + in-process DNS integration tests
$ go test -race ./...      # with the race detector
```

The evaluator has table-driven tests (precedence, unary minus, right-
associative `^`, tetration, decimals, aliases, every error path) and a fuzz
target (`FTestEvaluate`) that must never panic.

`limits_test.go` pins the two ceilings that bound how large a number we can
output: the 60000-character output cap (`TestOutputCapBoundary`) and the
1024-bit precision limit (`TestPrecisionBitLimit`).

## Project layout

```
evaluator.go   tokenizer + result formatting (decisions 2 & 5)
parser.go      recursive-descent parser (decision 3)
main.go        DNS server (decision 6 + flags)
evaluator_test.go   M1 tests + fuzz target
limits_test.go      M4c output-cap and precision-ceiling tests
main_test.go        M2 in-process integration tests
```

Milestones ship as separate PRs: M1 evaluator, M2 server, M3 README +
CI, M4a tetration operator, M4c big.Float precision and output cap.
