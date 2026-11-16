package live

import (
	"context"
	"net/netip"
	"time"

	"golang.org/x/sys/windows"

	"whynet/internal/probe"
)

// DefaultEnv is the real network of this computer.
func DefaultEnv(m probe.Markers) Env {
	local := probe.DefaultEnv(m).Local
	return Env{
		Echo:     systemEcho,
		WiFi:     systemWiFi,
		CPUTimes: systemCPUTimes,
		Adapters: probe.Adapters,
		RouteIf:  routeIf,
		Resolve:  probe.ResolveServer,
		ASN:      probe.ASN,
		Local:    func(ctx context.Context) probe.Local { return local(ctx, "", m) },
		Markers:  m,

		Tick:         500 * time.Millisecond,
		Timeout:      time.Second,
		TraceEvery:   10,
		WiFiEvery:    10,
		StandInAfter: 20,
		MaxTicks:     7200,
	}
}

// routeIf is the interface Windows would send traffic to ip through.
func routeIf(ip netip.Addr) (uint32, error) {
	var idx uint32
	err := windows.GetBestInterfaceEx(&windows.SockaddrInet4{Addr: ip.As4()}, &idx)
	return idx, err
}
