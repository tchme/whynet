package probe

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"strings"
	"time"
)

// controlSNI is a neutral name for the control handshake: if the real name
// is cut and this one passes on the same address, the block is by name (DPI).
const controlSNI = "example.org"

func (p *prober) tls(s *Step) {
	if !p.tcpOK {
		s.Facts["tls.status"] = "skip"
		s.Status = "skip"
	} else {
		st, err := p.handshake(p.ip, p.host, false)
		cls := tlsClass(err)
		s.Facts["tls.status"] = cls
		s.Evidence["real"] = tlsEvidence(p.host, st, err)
		p.tlsOK = err == nil
		if m := interceptor(st, err, p.env.Markers.Interceptors); m != "" {
			s.Facts["tls.intercepted"] = "true"
			s.Facts["tls.interceptor"] = m
		}
		switch cls {
		case "reset", "timeout", "eof", "error":
			_, cerr := p.handshake(p.ip, controlSNI, true)
			// a TLS alert is still an answer from the server: the path is open. A plain close is the
			// server refusing a name it has no certificate for (linkedin.com behind Google's balancer):
			// the path is open too, but the name test can't be made.
			ctrl := "failed"
			switch c := tlsClass(cerr); {
			case cerr == nil || c == "alert":
				ctrl = "ok"
			case c == "eof":
				ctrl = "closed"
			}
			s.Facts["tls.control"] = ctrl
			s.Evidence["control"] = map[string]any{"sni": controlSNI, "result": tlsClass(cerr), "error": errText(cerr)}
		}
		if !p.tlsOK {
			s.Status = "fail"
		}
	}
	if p.dnsDiffer {
		st, err := p.handshake(p.dohIP, p.host, false)
		s.Facts["tls.alt"] = tlsClass(err)
		s.Evidence["alt"] = map[string]any{"ip": p.dohIP, "handshake": tlsEvidence(p.host, st, err)}
	}
}

func (p *prober) handshake(ip, sni string, insecure bool) (*tls.ConnectionState, error) {
	return p.handshakeAt(net.JoinHostPort(ip, "443"), sni, insecure)
}

// handshakeAt is a TLS handshake with sni to any address and port (programs use more than 443).
func (p *prober) handshakeAt(addr, sni string, insecure bool) (*tls.ConnectionState, error) {
	ctx, cancel := p.timeout()
	defer cancel()
	raw, err := p.env.Dial(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	defer raw.Close()
	c := tls.Client(raw, &tls.Config{ServerName: sni, RootCAs: p.env.RootCAs, InsecureSkipVerify: insecure})
	if err := c.HandshakeContext(ctx); err != nil {
		return nil, err
	}
	st := c.ConnectionState()
	return &st, nil
}

func tlsEvidence(sni string, st *tls.ConnectionState, err error) map[string]any {
	ev := map[string]any{"sni": sni}
	if err != nil {
		ev["error"] = err.Error()
		if cv := (*tls.CertificateVerificationError)(nil); errors.As(err, &cv) {
			ev["chain"] = certs(cv.UnverifiedCertificates)
		}
		return ev
	}
	ev["version"] = tls.VersionName(st.Version)
	ev["cipher"] = tls.CipherSuiteName(st.CipherSuite)
	if len(st.VerifiedChains) > 0 {
		ev["chain"] = certs(st.VerifiedChains[0])
	} else {
		ev["chain"] = certs(st.PeerCertificates)
	}
	return ev
}

func certs(cs []*x509.Certificate) []map[string]any {
	out := make([]map[string]any, len(cs))
	for i, c := range cs {
		out[i] = map[string]any{
			"subject":    c.Subject.String(),
			"issuer":     c.Issuer.String(),
			"not_before": c.NotBefore.UTC().Format(time.RFC3339),
			"not_after":  c.NotAfter.UTC().Format(time.RFC3339),
			"dns_names":  c.DNSNames[:min(5, len(c.DNSNames))],
		}
	}
	return out
}

// interceptor names the HTTPS-scanning software found in the chain, if any.
func interceptor(st *tls.ConnectionState, err error, markers []string) string {
	var chain []*x509.Certificate
	switch {
	case st != nil && len(st.VerifiedChains) > 0:
		chain = st.VerifiedChains[0]
	case st != nil:
		chain = st.PeerCertificates
	default:
		if cv := (*tls.CertificateVerificationError)(nil); errors.As(err, &cv) {
			chain = cv.UnverifiedCertificates
		}
	}
	for _, c := range chain {
		if m := firstIn(strings.ToLower(c.Issuer.String()+" "+c.Subject.String()), markers); m != "" {
			return m
		}
	}
	return ""
}

func firstIn(s string, markers []string) string {
	for _, m := range markers {
		if strings.Contains(s, m) {
			return m
		}
	}
	return ""
}
