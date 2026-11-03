package main

import (
	"bytes"
	"math/big"
	"net"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// freePort reserves a free TCP port on loopback and returns it. The socket is
// closed immediately after; the tiny race window is acceptable for a test.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatalf("close port: %v", err)
	}
	return port
}

// waitReady polls the UDP port until the server answers or the timeout elapses.
func waitReady(t *testing.T, port int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		m := new(dns.Msg)
		m.SetQuestion(dns.Fqdn("1+1.calc.test"), dns.TypeTXT)
		c := dns.Client{Net: "udp", Timeout: 500 * time.Millisecond}
		if _, _, err := c.Exchange(m, net.JoinHostPort("127.0.0.1", strconv.Itoa(port))); err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("server did not become ready on port %d within %s", port, timeout)
}

// TestE2EBinary is the end-to-end check: it builds the dnscalc binary from the
// current commit, spawns it as a real subprocess on a random port, and queries
// it with a DNS client. This exercises the compiled artifact and the OS network
// stack, not the in-process handler.
func TestE2EBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e in -short mode")
	}

	dir := t.TempDir()
	bin := filepath.Join(dir, "dnscalc")

	// Build from the current commit in the same step (a stale binary does not count).
	var buildErr bytes.Buffer
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Stderr = &buildErr
	if err := build.Run(); err != nil {
		t.Fatalf("build binary: %v", err)
	}

	port := freePort(t)
	// Zone flag in mixed case without the trailing dot: queries below use
	// another case and must still match.
	cmd := exec.Command(bin, "-listen", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), "-zone", "Calc.Test")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start binary: %v", err)
	}
	defer cmd.Process.Kill()

	waitReady(t, port, 5*time.Second)

	query := func(name string) (*dns.Msg, error) {
		m := new(dns.Msg)
		m.SetQuestion(dns.Fqdn(name), dns.TypeTXT)
		c := dns.Client{Net: "udp", Timeout: 2 * time.Second}
		r, _, err := c.Exchange(m, net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		return r, err
	}

	tests := []struct {
		name    string
		qname   string
		wantTxt string
	}{
		// Undefined results first: each used to panic and kill the process,
		// so the cases after them also prove the binary survived.
		{"not-real", "lp.0-2.rp^0.5.calc.test", "error: result is not a real number"},
		{"zero-times-overflow", "0*2^2^31.calc.test", "error: result too large"},
		{"huge-power", "2^2^2^30.calc.test", "error: result too large"},
		{"tiny-power", "2^-2^24.calc.test", "error: result too small"},
		{"mult-before-add", "2+3*4.calc.test", "14"},
		{"zone-0x20", "2+3*4.cAlC.TeSt", "14"},
		{"decimal", "1.5*2.calc.test", "3"},
		{"division-by-zero", "1/0.calc.test", "error: division by zero"},
		{"pow-alias", "2.pow.3.calc.test", "8"},
		{"tetration", "2.tet.3.calc.test", "16"},
		{"tetration-settles", "1.tet.lp.10.pow.300.rp.calc.test", "1"},
		{"tetration-two-cycle", "0.tet.lp.10.pow.300.plus.1.rp.calc.test", "0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := query(tt.qname)
			if err != nil {
				t.Fatalf("query %s: %v", tt.qname, err)
			}
			if r.Rcode != dns.RcodeSuccess {
				t.Errorf("query %s: rcode = %d, want NOERROR", tt.qname, r.Rcode)
			}
			if len(r.Answer) != 1 {
				t.Fatalf("query %s: got %d answer records, want 1", tt.qname, len(r.Answer))
			}
			if got := txt(t, r.Answer[0]); got != tt.wantTxt {
				t.Errorf("query %s: TXT = %q, want %q", tt.qname, got, tt.wantTxt)
			}
		})
	}
}

// TestE2EDig checks long results with the real dig against the built binary:
// dig retries a truncated UDP reply over TCP on its own, and prints the TXT
// strings separately, which must rejoin to the full number.
func TestE2EDig(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e in -short mode")
	}
	dig, err := exec.LookPath("dig")
	if err != nil {
		t.Skip("dig not installed")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "dnscalc")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build binary: %v\n%s", err, out)
	}
	port := freePort(t)
	cmd := exec.Command(bin, "-listen", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err := cmd.Start(); err != nil {
		t.Fatalf("start binary: %v", err)
	}
	defer cmd.Process.Kill()
	waitReady(t, port, 5*time.Second)

	pow2 := func(n uint) string { return new(big.Int).Lsh(big.NewInt(1), n).String() }
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"TXT", "2+3*4.calc.test"}, "14"},
		{[]string{"TXT", "1/3.calc.test"}, "0." + strings.Repeat("3", 300)},
		{[]string{"TXT", "2^5000.calc.test"}, pow2(5000)},                  // UDP truncated, dig retries TCP
		{[]string{"+bufsize=4096", "TXT", "2^5000.calc.test"}, pow2(5000)}, // fits EDNS0 UDP
		{[]string{"+tcp", "TXT", "2^199313.calc.test"}, pow2(199313)},
		{[]string{"TXT", "2^199313.calc.test"}, pow2(199313)},
	}
	for _, tt := range tests {
		args := append([]string{"@127.0.0.1", "-p", strconv.Itoa(port), "+short", "+tries=1", "+time=3"}, tt.args...)
		out, err := exec.Command(dig, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("dig %v: %v\n%s", tt.args, err, out)
		}
		// dig prints each TXT string quoted, separated by spaces.
		got := strings.NewReplacer("\"", "", " ", "", "\n", "").Replace(string(out))
		if got != tt.want {
			t.Errorf("dig %v: got %d chars (%.40q), want %d", tt.args, len(got), got, len(tt.want))
		}
	}
}
