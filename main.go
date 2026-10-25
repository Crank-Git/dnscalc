package main

import (
	"flag"
	"log"
	"strings"

	"github.com/miekg/dns"
)

// TTLs per decision 6: valid answers live for an hour; errors are short so a
// misconfigured resolver cache does not hide a fix for long.
const (
	ttlValid = 3600
	ttlError = 60
)

// handler evaluates math expressions served under a configurable zone.
type handler struct {
	// zone is the rooted zone suffix (e.g. "calc.test."). A query name is
	// "under the zone" when it equals the zone or ends with "."+zone.
	zone string
}

// ServeDNS implements dns.Handler. It logs exactly one line per query
// (qname, qtype, rcode, result) and applies the response matrix in
// decision 6.
func (h *handler) ServeDNS(w dns.ResponseWriter, req *dns.Msg) {
	m := new(dns.Msg)
	m.SetReply(req)
	m.Authoritative = true // AA, per decision 6

	if len(req.Question) == 0 {
		m.Rcode = dns.RcodeFormatError
		w.WriteMsg(m)
		return
	}
	q := req.Question[0]
	qname := q.Name

	// Name outside the zone -> REFUSED.
	if qname != h.zone && !strings.HasSuffix(qname, "."+h.zone) {
		m.Rcode = dns.RcodeRefused
		log.Printf("qname=%s qtype=%s rcode=%s result=-\n", qname, dns.TypeToString[q.Qtype], dns.RcodeToString[m.Rcode])
		w.WriteMsg(m)
		return
	}

	// Non-TXT under the zone -> NOERROR, empty answer (NODATA).
	if q.Qtype != dns.TypeTXT {
		log.Printf("qname=%s qtype=%s rcode=%s result=-\n", qname, dns.TypeToString[q.Qtype], dns.RcodeToString[m.Rcode])
		w.WriteMsg(m)
		return
	}

	// Expression = every label before the suffix, joined with ".".
	expr := strings.TrimSuffix(strings.TrimSuffix(qname, h.zone), ".")

	result, err := evaluate(expr)
	line := func(res string) {
		log.Printf("qname=%s qtype=%s rcode=%s result=%s\n", qname, dns.TypeToString[q.Qtype], dns.RcodeToString[m.Rcode], res)
	}

	if err != nil {
		msg := "error: " + err.Error()
		m.Answer = []dns.RR{&dns.TXT{
			Hdr: dns.RR_Header{Name: qname, Rrtype: dns.TypeTXT, Class: dns.ClassINET, Ttl: ttlError},
			Txt: []string{msg},
		}}
		line(msg)
	} else {
		msg := formatResult(result)
		m.Answer = []dns.RR{&dns.TXT{
			Hdr: dns.RR_Header{Name: qname, Rrtype: dns.TypeTXT, Class: dns.ClassINET, Ttl: ttlValid},
			Txt: []string{msg},
		}}
		line(msg)
	}
	w.WriteMsg(m)
}

func main() {
	listen := flag.String("listen", "127.0.0.1:8053", "address to serve on (host:port)")
	zone := flag.String("zone", "calc.test.", "zone suffix that expressions hang under")
	flag.Parse()

	// Normalise: a rooted zone keeps the suffix math in main simple.
	if !strings.HasSuffix(*zone, ".") {
		*zone += "."
	}

	h := &handler{zone: *zone}

	udp := &dns.Server{Addr: *listen, Net: "udp", Handler: h}
	tcp := &dns.Server{Addr: *listen, Net: "tcp", Handler: h}

	go func() {
		if err := udp.ListenAndServe(); err != nil {
			log.Fatalf("UDP server failed: %v", err)
		}
	}()
	go func() {
		if err := tcp.ListenAndServe(); err != nil {
			log.Fatalf("TCP server failed: %v", err)
		}
	}()

	log.Printf("dnscalc serving %s under zone %q (UDP+TCP)", *listen, *zone)
	select {} // block forever
}
