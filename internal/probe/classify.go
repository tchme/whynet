package probe

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/netip"
	"slices"
	"syscall"
)

// Windows socket error codes; syscall's ECONN* names are different numbers there.
const (
	wsaeNetUnreach  syscall.Errno = 10051
	wsaeConnReset   syscall.Errno = 10054
	wsaeTimedOut    syscall.Errno = 10060
	wsaeConnRefused syscall.Errno = 10061
	wsaeHostUnreach syscall.Errno = 10065
)

// netClass sorts a network error into the words the rules use.
func netClass(err error) string {
	var ne net.Error
	switch {
	case err == nil:
		return "ok"
	case isErrno(err, wsaeConnReset, syscall.ECONNRESET):
		return "reset"
	case isErrno(err, wsaeConnRefused, syscall.ECONNREFUSED):
		return "refused"
	case isErrno(err, wsaeNetUnreach, wsaeHostUnreach, syscall.ENETUNREACH, syscall.EHOSTUNREACH):
		return "unreachable"
	case isErrno(err, wsaeTimedOut), errors.As(err, &ne) && ne.Timeout():
		return "timeout"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "eof"
	}
	return "error"
}

func isErrno(err error, codes ...syscall.Errno) bool {
	var e syscall.Errno
	return errors.As(err, &e) && slices.Contains(codes, e)
}

// tlsClass is netClass plus the certificate and alert cases of a handshake.
func tlsClass(err error) string {
	var (
		invalid x509.CertificateInvalidError
		name    x509.HostnameError
		unknown x509.UnknownAuthorityError
		verify  *tls.CertificateVerificationError
		alert   tls.AlertError
	)
	switch {
	case err == nil:
		return "ok"
	case errors.As(err, &invalid) && invalid.Reason == x509.Expired:
		return "cert_expired"
	case errors.As(err, &name):
		return "cert_name"
	case errors.As(err, &unknown):
		return "cert_untrusted"
	case errors.As(err, &verify), errors.As(err, &invalid):
		return "cert_invalid"
	case errors.As(err, &alert):
		return "alert"
	}
	return netClass(err)
}

// dnsClass sorts a system resolver answer.
func dnsClass(ips []string, err error) string {
	var de *net.DNSError
	switch {
	case err == nil && len(ips) > 0:
		return "ok"
	case err == nil:
		return "empty"
	case errors.As(err, &de) && de.IsNotFound:
		return "nxdomain"
	case errors.As(err, &de) && de.IsTimeout:
		return "timeout"
	}
	return "error"
}

func answer(ips []string, err error) map[string]any {
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	return map[string]any{"ips": ips}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// fakeIPs is the benchmarking range that Clash/Mihomo/sing-box "fake-ip" mode hands out.
var fakeIPs = netip.MustParsePrefix("198.18.0.0/15")

func fakeIP(ips []string) bool {
	for _, s := range ips {
		if ip, err := netip.ParseAddr(s); err != nil || !fakeIPs.Contains(ip) {
			return false
		}
	}
	return len(ips) > 0
}

func overlap(a, b []string) bool {
	for _, x := range a {
		if slices.Contains(b, x) {
			return true
		}
	}
	return false
}
