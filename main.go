package main

import (
	"flag"
	"log"
	"net"
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
	// zone is the rooted, lowercase zone suffix (e.g. "calc.test."). A query
	// name is "under the zone" when, compared case-insensitively, it equals
	// the zone or ends with "."+zone.
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
		writeReply(w, req, m)
		return
	}
	q := req.Question[0]
	qname := q.Name

	// Name outside the zone -> REFUSED. Names compare case-insensitively:
	// resolvers using 0x20 randomize the case of every query.
	lname := strings.ToLower(qname)
	if lname != h.zone && !strings.HasSuffix(lname, "."+h.zone) {
		m.Rcode = dns.RcodeRefused
		log.Printf("qname=%s qtype=%s rcode=%s result=-\n", qname, dns.TypeToString[q.Qtype], dns.RcodeToString[m.Rcode])
		writeReply(w, req, m)
		return
	}

	// SOA at the apex -> the zone's SOA.
	if q.Qtype == dns.TypeSOA && lname == h.zone {
		m.Answer = []dns.RR{h.soa()}
		log.Printf("qname=%s qtype=%s rcode=%s result=-\n", qname, dns.TypeToString[q.Qtype], dns.RcodeToString[m.Rcode])
		writeReply(w, req, m)
		return
	}

	// Non-TXT under the zone -> NOERROR, empty answer (NODATA), with the
	// SOA in the authority section so resolvers can cache the negative
	// answer.
	if q.Qtype != dns.TypeTXT {
		m.Ns = []dns.RR{h.soa()}
		log.Printf("qname=%s qtype=%s rcode=%s result=-\n", qname, dns.TypeToString[q.Qtype], dns.RcodeToString[m.Rcode])
		writeReply(w, req, m)
		return
	}

	// Expression = every label before the suffix, joined with ".". The
	// suffix is cut by length, since its case may differ from h.zone's.
	// The name is in presentation format, so "(" arrives as "\(" and a
	// space as "\ "; unescape before evaluating.
	expr := unescapeName(strings.TrimSuffix(qname[:len(qname)-len(h.zone)], "."))

	msg, ttl := answer(expr)
	m.Answer = []dns.RR{&dns.TXT{
		Hdr: dns.RR_Header{Name: qname, Rrtype: dns.TypeTXT, Class: dns.ClassINET, Ttl: ttl},
		Txt: splitTXT(msg),
	}}
	log.Printf("qname=%s qtype=%s rcode=%s result=%s\n", qname, dns.TypeToString[q.Qtype], dns.RcodeToString[m.Rcode], msg)
	writeReply(w, req, m)
}

// answer evaluates expr and returns the TXT text with its TTL: the formatted
// result with ttlValid, or "error: <reason>" with ttlError when evaluating
// or formatting (the output cap) fails.
func answer(expr string) (string, uint32) {
	v, err := evaluate(expr)
	if err == nil {
		var s string
		if s, err = formatResult(v); err == nil {
			return s, ttlValid
		}
	}
	return "error: " + err.Error(), ttlError
}

// soa is the zone's SOA record. Its minimum TTL, ttlError, is how long
// resolvers cache a NODATA answer.
func (h *handler) soa() dns.RR {
	return &dns.SOA{
		Hdr:     dns.RR_Header{Name: h.zone, Rrtype: dns.TypeSOA, Class: dns.ClassINET, Ttl: ttlError},
		Ns:      "ns." + h.zone,
		Mbox:    "hostmaster." + h.zone,
		Serial:  1,
		Refresh: 3600,
		Retry:   600,
		Expire:  86400,
		Minttl:  ttlError,
	}
}

// unescapeName undoes the presentation-format escaping miekg/dns applies to
// names: "\DDD" becomes the byte with decimal value DDD, and "\X" becomes X
// for any other character X.
func unescapeName(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' || i+1 == len(s) {
			b.WriteByte(c)
			continue
		}
		if i+3 < len(s) && isDigit(s[i+1]) && isDigit(s[i+2]) && isDigit(s[i+3]) {
			b.WriteByte(byte(int(s[i+1]-'0')*100 + int(s[i+2]-'0')*10 + int(s[i+3]-'0')))
			i += 3
			continue
		}
		b.WriteByte(s[i+1])
		i++
	}
	return b.String()
}

// maxTXTString is the most bytes one TXT character-string can hold: its
// length is a single byte on the wire.
const maxTXTString = 255

// splitTXT splits s into the character-strings of one TXT record, each at
// most maxTXTString bytes. Clients concatenate them to recover s.
func splitTXT(s string) []string {
	var out []string
	for len(s) > maxTXTString {
		out = append(out, s[:maxTXTString])
		s = s[maxTXTString:]
	}
	return append(out, s)
}

// writeReply sends m, sized for the transport. Over UDP the reply must fit
// the client's buffer: 512 bytes, or the EDNS0 size it advertised (an OPT
// record is echoed back). An answer that does not fit is dropped and TC set,
// so the client retries over TCP, which carries up to 65535 bytes. A failed
// write is logged.
func writeReply(w dns.ResponseWriter, req, m *dns.Msg) {
	_, udp := w.RemoteAddr().(*net.UDPAddr)
	size := dns.MaxMsgSize
	if udp {
		size = dns.MinMsgSize
	}
	if opt := req.IsEdns0(); opt != nil {
		m.SetEdns0(dns.DefaultMsgSize, false)
		if udp && int(opt.UDPSize()) > size {
			size = int(opt.UDPSize())
		}
	}
	m.Truncate(size)
	if err := w.WriteMsg(m); err != nil {
		log.Printf("write reply to %s for %v: %v", w.RemoteAddr(), req.Question, err)
	}
}

// recoverHandler answers SERVFAIL instead of letting a panic in next take
// the process down: miekg/dns does not recover panics in handlers.
type recoverHandler struct {
	next dns.Handler
}

func (h recoverHandler) ServeDNS(w dns.ResponseWriter, req *dns.Msg) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic serving %v: %v", req.Question, r)
			m := new(dns.Msg)
			m.SetRcode(req, dns.RcodeServerFailure)
			w.WriteMsg(m)
		}
	}()
	h.next.ServeDNS(w, req)
}

// newHandler returns the server's full handler chain for zone, which may be
// given in any case, with or without the trailing dot.
func newHandler(zone string) dns.Handler {
	return recoverHandler{next: &handler{zone: strings.ToLower(dns.Fqdn(zone))}}
}

func main() {
	listen := flag.String("listen", "127.0.0.1:8053", "address to serve on (host:port)")
	zone := flag.String("zone", "calc.test.", "zone suffix that expressions hang under")
	flag.Parse()

	h := newHandler(*zone)

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
