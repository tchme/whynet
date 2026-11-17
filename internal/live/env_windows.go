package live

import (
	"context"
	"errors"
	"net/netip"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"whynet/internal/etw"
	"whynet/internal/probe"
)

// DefaultEnv is the real network of this computer.
func DefaultEnv(m probe.Markers) Env {
	local := probe.DefaultEnv(m).Local
	return Env{
		Echo:       systemEcho,
		WiFi:       systemWiFi,
		CPUTimes:   systemCPUTimes,
		Foreground: foreground,
		ExeName:    probe.ExeName,
		Adapters:   probe.Adapters,
		RouteIf:    routeIf,
		Resolve:    probe.ResolveServer,
		ASN:        probe.ASN,
		Local:      func(ctx context.Context) probe.Local { return local(ctx, "", m) },
		Markers:    m,

		Tick:         500 * time.Millisecond,
		Timeout:      time.Second,
		TraceEvery:   10,
		WiFiEvery:    10,
		StandInAfter: 20,
		MaxTicks:     7200,
	}
}

// SystemFrames streams every frame on this computer through package etw (a UAC prompt unless
// whynet runs as administrator). Set it as Env.Frames to record the game's frames.
func SystemFrames(onFrame func(pid uint32, at time.Time)) (stop func(), err error) {
	stop, err = etw.Stream(etw.Handlers{Frame: func(f etw.Frame) { onFrame(f.PID, f.Time) }},
		time.Hour+time.Minute) // a recording stops by itself after an hour
	if errors.Is(err, etw.ErrDeclined) {
		err = ErrDeclined
	}
	return stop, err
}

var (
	procGetForegroundWindow      = user32.NewProc("GetForegroundWindow") // user32: hotkey_windows.go
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
)

// foreground is the exe whose window is in front, "" when none or hidden.
func foreground() string {
	w, _, _ := procGetForegroundWindow.Call()
	if w == 0 {
		return ""
	}
	var pid uint32
	procGetWindowThreadProcessId.Call(w, uintptr(unsafe.Pointer(&pid)))
	return probe.ExeName(pid)
}

// routeIf is the interface Windows would send traffic to ip through.
func routeIf(ip netip.Addr) (uint32, error) {
	var idx uint32
	err := windows.GetBestInterfaceEx(&windows.SockaddrInet4{Addr: ip.As4()}, &idx)
	return idx, err
}
