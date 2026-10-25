package main

import (
	"bytes"
	"net"
	"os/exec"
	"path/filepath"
	"strconv"
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
	cmd := exec.Command(bin, "-listen", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), "-zone", "calc.test.")
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
		{"mult-before-add", "2+3*4.calc.test", "14"},
		{"decimal", "1.5*2.calc.test", "3"},
		{"division-by-zero", "1/0.calc.test", "error: division by zero"},
		{"pow-alias", "2.pow.3.calc.test", "8"},
		{"tetration", "2.tet.3.calc.test", "16"},
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
