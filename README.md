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

See the per-milestone PRs for implementation details.
