package probe

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"time"
)

// ErrIPv6 means a server was given as an IPv6 address; the stability check is IPv4 only for now.
var ErrIPv6 = errors.New("IPv6 address")

// Server is where a stability check pings: the address, the name it came from, and how it was found.
type Server struct {
	IP   netip.Addr `json:"ip"`
	Name string     `json:"name,omitempty"` // "" when an IP was given
	Via  string     `json:"via,omitempty"`  // system, doh (the system DNS gave a fake-ip or nothing)
}

// ResolveServer turns what the person typed (an IP, a name, even a URL) into one IPv4 address.
// Behind a fake-ip router the system DNS hands out stand-in addresses that lead to the router's
// proxy, not to the server; the independent DNS then gives the real one.
//
// shortcut: IPv4 only, like the site check's DNS; lift it when a real game server needs IPv6.
func ResolveServer(ctx context.Context, input string) (Server, error) {
	if ip, err := netip.ParseAddr(strings.Trim(strings.TrimSpace(input), "[]")); err == nil && !ip.Is4() && !ip.Is4In6() {
		return Server{}, ErrIPv6
	}
	host, _, err := Target(input)
	if err != nil {
		return Server{}, err
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if !ip.Is4() {
			return Server{}, ErrIPv6
		}
		return Server{IP: ip}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip4", host)
	var sys []string
	for _, ip := range ips {
		sys = append(sys, ip.Unmap().String())
	}
	if err == nil && len(sys) > 0 && !fakeIP(sys) {
		return Server{IP: ips[0].Unmap(), Name: host, Via: "system"}, nil
	}
	doh, derr := dohLookup(ctx, host)
	for _, s := range doh.IPs {
		if ip, err := netip.ParseAddr(s); err == nil && ip.Is4() {
			return Server{IP: ip, Name: host, Via: "doh"}, nil
		}
	}
	if err == nil {
		err = derr
	}
	if err == nil {
		err = errors.New("no IPv4 address for " + host)
	}
	return Server{}, err
}
