package main

import (
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// startServer launches the handler on a UDP or TCP socket on a random port
// and returns the port plus a cleanup func.
func startServer(t *testing.T, zone, network string) (port int, cleanup func()) {
	t.Helper()
	return startServerWith(t, newHandler(zone), network)
}

// startServerWith is startServer for an arbitrary handler.
func startServerWith(t *testing.T, h dns.Handler, network string) (port int, cleanup func()) {
	t.Helper()
	var srv *dns.Server
	switch network {
	case "udp":
		addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("ResolveUDPAddr: %v", err)
		}
		conn, err := net.ListenUDP("udp", addr)
		if err != nil {
			t.Fatalf("ListenUDP: %v", err)
		}
		srv = &dns.Server{PacketConn: conn, Handler: h}
		go func() {
			if err := srv.ActivateAndServe(); err != nil {
				t.Errorf("ActivateAndServe: %v", err)
			}
		}()
		port = conn.LocalAddr().(*net.UDPAddr).Port
		return port, func() { srv.Shutdown(); conn.Close() }
	case "tcp":
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("Listen tcp: %v", err)
		}
		srv = &dns.Server{Listener: ln, Handler: h}
		go func() {
			if err := srv.ActivateAndServe(); err != nil {
				t.Errorf("ActivateAndServe: %v", err)
			}
		}()
		port = ln.Addr().(*net.TCPAddr).Port
		return port, func() { srv.Shutdown() }
	default:
		t.Fatalf("unknown network %q", network)
		return 0, func() {}
	}
}

// query sends one UDP TXT (or given qtype) query and returns the reply.
func query(t *testing.T, port int, name string, qtype uint16) *dns.Msg {
	t.Helper()
	c := &dns.Client{Net: "udp", Timeout: 3 * time.Second}
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), qtype)
	r, _, err := c.Exchange(m, net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatalf("query %s: %v", name, err)
	}
	return r
}

func txt(t *testing.T, rr dns.RR) string {
	t.Helper()
	txt, ok := rr.(*dns.TXT)
	if !ok {
		t.Fatalf("expected *dns.TXT, got %T", rr)
	}
	if len(txt.Txt) == 0 {
		t.Fatalf("empty TXT body")
	}
	return txt.Txt[0]
}

func TestDNSServerResponses(t *testing.T) {
	port, cleanup := startServer(t, "calc.test.", "udp")
	defer cleanup()

	tests := []struct {
		name      string
		qname     string
		qtype     uint16
		wantRcode int
		wantTxt   string // "" means: do not check TXT body (e.g. NODATA)
		checkTxt  bool
		wantTTL   uint32
		wantAA    bool
	}{
		{"valid mult-before-add", "2+3*4.calc.test", dns.TypeTXT, dns.RcodeSuccess, "14", true, ttlValid, true},
		{"valid decimal", "1.5*2.calc.test", dns.TypeTXT, dns.RcodeSuccess, "3", true, ttlValid, true},
		{"division by zero", "1/0.calc.test", dns.TypeTXT, dns.RcodeSuccess, "error: division by zero", true, ttlError, true},
		{"word alias plus", "2.plus.3.calc.test", dns.TypeTXT, dns.RcodeSuccess, "5", true, ttlValid, true},
		{"unary minus pow", "-2^2.calc.test", dns.TypeTXT, dns.RcodeSuccess, "-4", true, ttlValid, true},
		{"other qtype NODATA", "2+3.calc.test", dns.TypeA, dns.RcodeSuccess, "", false, 0, true},
		{"outside zone refused", "2+3.example.org", dns.TypeTXT, dns.RcodeRefused, "", false, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := query(t, port, tt.qname, tt.qtype)
			if r.Rcode != tt.wantRcode {
				t.Errorf("rcode = %d, want %d", r.Rcode, tt.wantRcode)
			}
			if tt.wantAA && !r.Authoritative {
				t.Errorf("AA flag not set")
			}
			if !tt.checkTxt {
				// NODATA / REFUSED: no answer records expected.
				if len(r.Answer) != 0 {
					t.Errorf("expected empty answer, got %d records", len(r.Answer))
				}
				return
			}
			if len(r.Answer) != 1 {
				t.Fatalf("expected 1 answer record, got %d", len(r.Answer))
			}
			if got := txt(t, r.Answer[0]); got != tt.wantTxt {
				t.Errorf("TXT = %q, want %q", got, tt.wantTxt)
			}
			if ttl := r.Answer[0].Header().Ttl; ttl != tt.wantTTL {
				t.Errorf("TTL = %d, want %d", ttl, tt.wantTTL)
			}
		})
	}
}

// TestDNSServerTCP confirms TCP serving works too (decision 6).
func TestDNSServerTCP(t *testing.T) {
	port, cleanup := startServer(t, "calc.test.", "tcp")
	defer cleanup()

	c := &dns.Client{Net: "tcp", Timeout: 3 * time.Second}
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn("2+3*4.calc.test"), dns.TypeTXT)
	r, _, err := c.Exchange(m, net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatalf("TCP exchange: %v", err)
	}
	if r.Rcode != dns.RcodeSuccess {
		t.Fatalf("rcode = %d, want NOERROR", r.Rcode)
	}
	if got := txt(t, r.Answer[0]); got != "14" {
		t.Errorf("TXT = %q, want %q", got, "14")
	}
}

// TestDNSServerUndefinedResults sends every expression that used to panic
// the process (NaN / infinity in math/big) and checks each one is answered
// with an error TXT, then that the server still answers a normal query.
func TestDNSServerUndefinedResults(t *testing.T) {
	for _, network := range []string{"udp", "tcp"} {
		t.Run(network, func(t *testing.T) {
			port, cleanup := startServer(t, "calc.test.", network)
			defer cleanup()

			tests := []struct {
				qname   string
				wantTxt string
			}{
				{"lp.0-2.rp^0.5.calc.test", "error: result is not a real number"},
				{"lp.0-8.rp^lp.1/3.rp.calc.test", "error: result is not a real number"},
				{"lp.0-2.rp.tet.3.calc.test", "error: result is not a real number"},
				{"0*2^2^31.calc.test", "error: result too large"},
				{"2^2^31-2^2^31.calc.test", "error: result too large"},
				{"2^2^24.calc.test", "error: result too large"},
				{"2^2^2^30.calc.test", "error: result too large"},
				{"2^-2^24.calc.test", "error: result too small"},
				{"0^lp.0-1.rp.calc.test", "error: division by zero"},
			}
			for _, tt := range tests {
				r := queryNet(t, network, port, tt.qname, dns.TypeTXT)
				if r.Rcode != dns.RcodeSuccess {
					t.Errorf("%s: rcode = %s, want NOERROR", tt.qname, dns.RcodeToString[r.Rcode])
					continue
				}
				if len(r.Answer) != 1 {
					t.Errorf("%s: got %d answers, want 1", tt.qname, len(r.Answer))
					continue
				}
				if got := txt(t, r.Answer[0]); got != tt.wantTxt {
					t.Errorf("%s: TXT = %q, want %q", tt.qname, got, tt.wantTxt)
				}
				if ttl := r.Answer[0].Header().Ttl; ttl != ttlError {
					t.Errorf("%s: TTL = %d, want %d", tt.qname, ttl, ttlError)
				}
			}

			// The server must still be up and answering.
			r := queryNet(t, network, port, "2+3*4.calc.test", dns.TypeTXT)
			if len(r.Answer) != 1 || txt(t, r.Answer[0]) != "14" {
				t.Fatalf("server stopped answering after undefined results: %v", r)
			}
		})
	}
}

// panicHandler panics on every query.
type panicHandler struct{}

func (panicHandler) ServeDNS(dns.ResponseWriter, *dns.Msg) { panic("boom") }

// TestRecoverHandler checks that a panicking handler is answered with
// SERVFAIL and that the server keeps serving afterwards.
func TestRecoverHandler(t *testing.T) {
	for _, network := range []string{"udp", "tcp"} {
		t.Run(network, func(t *testing.T) {
			port, cleanup := startServerWith(t, recoverHandler{next: panicHandler{}}, network)
			defer cleanup()
			for i := 0; i < 3; i++ {
				r := queryNet(t, network, port, "2+3.calc.test", dns.TypeTXT)
				if r.Rcode != dns.RcodeServerFailure {
					t.Fatalf("query %d: rcode = %s, want SERVFAIL", i, dns.RcodeToString[r.Rcode])
				}
				if r.Id == 0 || len(r.Question) != 1 || r.Question[0].Name != "2+3.calc.test." {
					t.Fatalf("query %d: reply does not echo the question: %v", i, r)
				}
			}
		})
	}
}

// queryNet sends one query over network ("udp" or "tcp") and returns the reply.
func queryNet(t *testing.T, network string, port int, name string, qtype uint16) *dns.Msg {
	t.Helper()
	c := &dns.Client{Net: network, Timeout: 3 * time.Second}
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), qtype)
	r, _, err := c.Exchange(m, net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatalf("%s query %s: %v", network, name, err)
	}
	return r
}
