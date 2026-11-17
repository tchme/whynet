package health

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	iphlpapi        = windows.NewLazySystemDLL("iphlpapi.dll")
	procConnHint    = iphlpapi.NewProc("GetNetworkConnectivityHint")
	procWinHTTPConf = windows.NewLazySystemDLL("winhttp.dll").NewProc("WinHttpGetDefaultProxyConfiguration")
	procGlobalFree  = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalFree")
)

const internetSettings = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

// connectivity is Windows' own verdict on the internet (the tray icon's "no internet"):
// GetNetworkConnectivityHint, Windows 10 2004 and later.
func connectivity() string {
	var hint struct {
		Level, Cost                             int32
		ApproachingLimit, OverLimit, Roaming, _ byte
	}
	if r, _, _ := procConnHint.Call(uintptr(unsafe.Pointer(&hint))); r != 0 {
		return "unknown"
	}
	switch hint.Level {
	case 1:
		return "none"
	case 2:
		return "local"
	case 3:
		return "internet"
	case 4:
		return "constrained" // a captive portal or similar
	}
	return "unknown"
}

// inetProxy is the person's proxy setting (WinINET: browsers and most programs).
func inetProxy() (enabled bool, server, pac string, err error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, internetSettings, registry.QUERY_VALUE)
	if err != nil {
		return false, "", "", err
	}
	defer k.Close()
	on, _, _ := k.GetIntegerValue("ProxyEnable")
	server, _, _ = k.GetStringValue("ProxyServer")
	pac, _, _ = k.GetStringValue("AutoConfigURL")
	return on == 1, server, pac, nil
}

// winHTTPProxy is the machine-wide WinHTTP proxy (Windows Update, the Store, some services); "" when direct.
func winHTTPProxy() (server, bypass string, err error) {
	var cfg struct {
		Access        uint32
		Proxy, Bypass *uint16
	}
	if r, _, e := procWinHTTPConf.Call(uintptr(unsafe.Pointer(&cfg))); r == 0 {
		return "", "", e
	}
	defer func() {
		for _, p := range []*uint16{cfg.Proxy, cfg.Bypass} {
			if p != nil {
				procGlobalFree.Call(uintptr(unsafe.Pointer(p)))
			}
		}
	}()
	if cfg.Access != 3 { // WINHTTP_ACCESS_TYPE_NAMED_PROXY
		return "", "", nil
	}
	return windows.UTF16PtrToString(cfg.Proxy), windows.UTF16PtrToString(cfg.Bypass), nil
}

// netsh runs netsh without a window. Its labels follow the Windows language; the values don't.
func netsh(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "netsh", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// winsockPaths lists the provider DLL of every Winsock catalog entry (32- and 64-bit catalogs),
// read from the registry: netsh prints it under a translated label.
func winsockPaths() ([]string, error) {
	var out []string
	base := `SYSTEM\CurrentControlSet\Services\WinSock2\Parameters\Protocol_Catalog9\`
	for _, cat := range []string{"Catalog_Entries", "Catalog_Entries64"} {
		k, err := registry.OpenKey(registry.LOCAL_MACHINE, base+cat, registry.ENUMERATE_SUB_KEYS)
		if err != nil {
			if cat == "Catalog_Entries" {
				return nil, err
			}
			continue
		}
		names, _ := k.ReadSubKeyNames(-1)
		k.Close()
		for _, n := range names {
			e, err := registry.OpenKey(registry.LOCAL_MACHINE, base+cat+`\`+n, registry.QUERY_VALUE)
			if err != nil {
				continue
			}
			b, _, err := e.GetBinaryValue("PackedCatalogItem")
			e.Close()
			if err != nil {
				continue
			}
			// The item starts with the provider path: ANSI, zero-terminated, in a 260-byte field.
			if i := strings.IndexByte(string(b[:min(len(b), 260)]), 0); i > 0 {
				out = append(out, string(b[:i]))
			}
		}
	}
	return out, nil
}

// dnsConfig is an adapter's DNS servers set by hand and the ones DHCP gave (registry, no admin).
func dnsConfig(guid string) (manual, dhcp []string) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services\Tcpip\Parameters\Interfaces\`+guid, registry.QUERY_VALUE)
	if err != nil {
		return nil, nil
	}
	defer k.Close()
	split := func(s string) []string {
		return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' })
	}
	m, _, _ := k.GetStringValue("NameServer")
	d, _, _ := k.GetStringValue("DhcpNameServer")
	return split(m), split(d)
}

// errorCount is an interface's receive + send errors so far; ok is false when Windows can't say.
func errorCount(luid uint64) (n uint64, ok bool) {
	row := windows.MibIfRow2{InterfaceLuid: luid}
	if windows.GetIfEntry2Ex(windows.MibIfEntryNormal, &row) != nil {
		return 0, false
	}
	return row.InErrors + row.OutErrors, true
}

func hostsPath() string {
	return filepath.Join(os.Getenv("SystemRoot"), `System32\drivers\etc\hosts`)
}

// ipv4MTU is the IPv4 MTU of an interface: what `netsh interface ipv4 show subinterfaces` shows
// and what a person lowers by hand (the adapter's own MTU stays 1500).
func ipv4MTU(luid uint64) (uint32, bool) {
	row := windows.MibIpInterfaceRow{Family: windows.AF_INET, InterfaceLuid: luid}
	if windows.GetIpInterfaceEntry(&row) != nil {
		return 0, false
	}
	return row.NlMtu, true
}
