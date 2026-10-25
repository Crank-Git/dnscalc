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

Hand-written recursive-descent parser over `float64` (no eval, no shelling
out, no expression library):

```
+  -  *  /  ^  ( )
```

- Unary minus is supported.
- `^` is right-associative and binds **tighter** than unary minus, so
  `-2^2 == -4` (i.e. `-(2^2)`) and `2^3^2 == 512` (i.e. `2^(3^2)`).
- Precedence, low to high: `+ -`, then `* /`, then `^`, then unary `-`, then
  parentheses/atoms.

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
associative `^`, decimals, aliases, every error path) and a fuzz target
(`FTestEvaluate`) that must never panic.

## Project layout

```
evaluator.go   tokenizer + result formatting (decisions 2 & 5)
parser.go      recursive-descent parser (decision 3)
main.go        DNS server (decision 6 + flags)
evaluator_test.go   M1 tests + fuzz target
main_test.go        M2 in-process integration tests
```

Milestones ship as separate PRs: M1 evaluator, M2 server, M3 README +
CI, M4a tetration operator, M4c big.Float precision and output cap.
