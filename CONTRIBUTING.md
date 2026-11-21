# Contributing to dnscalc

## Before you open a PR

CI runs these on every push and pull request to `main` and `dev`
(`.github/workflows/ci.yml`):

```
$ gofmt -l .                       # must print nothing
$ go vet ./...
$ DNSCALC_REQUIRE_DIG=1 go test -race ./...
$ go test -run='^$' -fuzz='FuzzEvaluate' -fuzztime=30s
```

`DNSCALC_REQUIRE_DIG=1` makes the e2e test fail instead of skip when `dig` isn't
installed.

If you change a rule the evaluator or the server follows, update
[DECISIONS.md](DECISIONS.md) in the same PR.

## Project layout

```
evaluator.go   tokenizer, limits, result formatting
parser.go      recursive-descent parser, powers, tetration
bigmath.go     ln / exp over *big.Float for non-integer powers
main.go        DNS server: zone matching, answers, reply sizing, flags
DECISIONS.md   the rules the code follows, and why
evaluator_test.go   evaluator table tests + fuzz target
limits_test.go      output cap, precision ceiling, magnitude range, formatting cost
bigmath_test.go     ln / exp / fractional powers against high-precision references
tetration_test.go   towers: settling, 2-cycles, limits
main_test.go        in-process DNS tests (UDP and TCP)
e2e_test.go         built binary over real DNS, including dig
docs/reference.md   operators, aliases, limits, formatting, responses
.goreleaser.yml     release builds
```

## Releasing

Tag `main` and run goreleaser with a GitHub token:

```
$ git tag -a v0.2.0 -m v0.2.0 && git push origin v0.2.0
$ GITHUB_TOKEN=$(gh auth token) goreleaser release --clean
```

`.goreleaser.yml` builds static binaries for macOS, Linux, and Windows on amd64 and
arm64, and attaches them to the release as `dnscalc_<os>_<arch>.tar.gz`.
