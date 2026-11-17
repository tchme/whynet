package probe

import (
	"context"
	"fmt"
	"maps"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// LocalInfo is the local step without a site (the Windows network health check): route, proxy,
// adapters, OS, DPI-bypass tools, public address, clock.
func LocalInfo(ctx context.Context, m Markers) Local {
	l := localInfo(ctx, "", m)
	delete(l.Facts, "local.hosts_entry")
	delete(l.Facts, "local.dpi_listed") // both are about a site
	return l
}

// localInfo looks at this computer: route, proxy, hosts file, adapters, OS, public address, clock.
// Each part fails on its own; its error lands in the evidence.
func localInfo(ctx context.Context, host string, m Markers) Local {
	l := Local{Facts: map[string]string{}, Evidence: map[string]any{}, Mask: map[string]string{}}
	note := func(part string, err error) {
		if err != nil {
			l.Evidence[part+"_error"] = err.Error()
		}
	}

	// A UDP "connect" sends nothing; it fails only when there is no route out.
	if c, err := net.Dial("udp", "1.1.1.1:53"); err != nil {
		l.Facts["local.net"] = "none"
		note("route", err)
	} else {
		l.Facts["local.net"] = "ok"
		c.Close()
	}

	osv, err := osVersion()
	note("os", err)
	l.Evidence["os"] = osv

	ads, err := Adapters()
	note("adapters", err)
	l.Evidence["adapters"] = ads
	l.Facts["local.vpn"] = "false"
	for _, a := range ads {
		if m.IsVPN(a) {
			l.Facts["local.vpn"] = "true"
			l.Evidence["vpn_adapter"] = a.Name
		}
	}

	dpiFacts, dpiEv, lists := dpiInfo(host, m)
	l.DPILists = lists
	maps.Copy(l.Facts, dpiFacts)
	if len(dpiEv) > 0 {
		l.Evidence["dpi_bypass"] = dpiEv
	}

	if k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE); err != nil {
		note("proxy", err)
	} else {
		enabled, _, _ := k.GetIntegerValue("ProxyEnable")
		server, _, _ := k.GetStringValue("ProxyServer")
		pac, _, _ := k.GetStringValue("AutoConfigURL")
		k.Close()
		l.Facts["local.proxy"] = "off"
		if enabled == 1 || pac != "" {
			l.Facts["local.proxy"] = "on"
			l.Evidence["proxy"] = map[string]any{"enabled": enabled == 1, "server": server, "pac": pac}
		}
	}

	if b, err := os.ReadFile(filepath.Join(os.Getenv("SystemRoot"), `System32\drivers\etc\hosts`)); err != nil {
		note("hosts", err)
	} else {
		lines := hostsLines(string(b), host)
		l.Facts["local.hosts_entry"] = strconv.FormatBool(len(lines) > 0)
		if len(lines) > 0 {
			l.Evidence["hosts_lines"] = lines
		}
	}

	l.Facts["local.clock"] = "unknown"
	if l.Facts["local.net"] == "ok" {
		info, date, err := publicInfo(ctx)
		note("public", err)
		if info != nil {
			l.Evidence["public"] = info
			if info["country"] != "" {
				l.Facts["local.country"] = info["country"] // where the internet sees this computer by default
			}
			if ip := info["ip"]; ip != "" {
				l.Mask[ip] = maskIP(ip)
			}
		}
		if date.IsZero() {
			date, err = serverDate(ctx, "https://8.8.8.8/")
			note("clock", err)
		}
		if !date.IsZero() {
			skew := time.Since(date).Round(time.Second)
			l.Facts["local.clock_skew_sec"] = strconv.Itoa(int(skew.Seconds()))
			l.Facts["local.clock"] = "ok"
			if skew.Abs() > 5*time.Minute {
				l.Facts["local.clock"] = "wrong"
			}
		}
	}

	// Not collected on purpose, masked in case they show up inside an error text.
	if h, err := os.Hostname(); err == nil && len(h) >= 3 {
		l.Mask[h] = "[computer-name]"
	}
	if u := os.Getenv("USERNAME"); len(u) >= 3 {
		l.Mask[u] = "[user-name]"
	}
	return l
}

func osVersion() (string, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE)
	if err != nil {
		return "", err
	}
	defer k.Close()
	name, _, _ := k.GetStringValue("ProductName")
	disp, _, _ := k.GetStringValue("DisplayVersion")
	build, _, _ := k.GetStringValue("CurrentBuild")
	if b, _ := strconv.Atoi(build); b >= 22000 {
		name = strings.Replace(name, "Windows 10", "Windows 11", 1) // the registry still says 10 on 11
	}
	return fmt.Sprintf("%s %s (build %s)", name, disp, build), nil
}

// Adapter is a network adapter that is up.
type Adapter struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Kind        string   `json:"kind,omitempty"` // wifi, cable, other
	IPv4        []string `json:"ipv4,omitempty"`
	Gateways    []string `json:"gateways,omitempty"`
	DNS         []string `json:"dns,omitempty"`
	Index       uint32   `json:"-"` // IPv4 interface index
	GUID        string   `json:"-"` // "{...}", as the Wi-Fi API names interfaces
	// For the Windows network health check; kept out of the site reports.
	LUID       uint64 `json:"-"`
	MTU        uint32 `json:"-"`
	Mbps       uint64 `json:"-"` // link speed, the slower direction
	DHCP       bool   `json:"-"` // IPv4 address from DHCP
	IPv6Global bool   `json:"-"` // a global IPv6 address (not ULA, not link-local)
}

// Adapters lists the network adapters that are up, with their gateways and DNS servers.
func Adapters() ([]Adapter, error) {
	size := uint32(16 << 10)
	var first *windows.IpAdapterAddresses
	for range 3 {
		buf := make([]byte, size)
		first = (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0]))
		err := windows.GetAdaptersAddresses(windows.AF_UNSPEC,
			windows.GAA_FLAG_INCLUDE_GATEWAYS|windows.GAA_FLAG_SKIP_ANYCAST|windows.GAA_FLAG_SKIP_MULTICAST, 0, first, &size)
		if err == nil {
			break
		}
		first = nil
		if err != windows.ERROR_BUFFER_OVERFLOW {
			return nil, err
		}
	}
	var out []Adapter
	for a := first; a != nil; a = a.Next {
		if a.OperStatus != windows.IfOperStatusUp || a.IfType == windows.IF_TYPE_SOFTWARE_LOOPBACK {
			continue
		}
		ad := Adapter{Name: windows.UTF16PtrToString(a.FriendlyName), Description: windows.UTF16PtrToString(a.Description),
			Kind: "other", Index: a.IfIndex, GUID: windows.BytePtrToString(a.AdapterName),
			LUID: a.Luid, MTU: a.Mtu, Mbps: min(a.TransmitLinkSpeed, a.ReceiveLinkSpeed) / 1e6, DHCP: a.Flags&0x4 != 0} // IP_ADAPTER_DHCP_ENABLED
		switch a.IfType {
		case windows.IF_TYPE_IEEE80211:
			ad.Kind = "wifi"
		case windows.IF_TYPE_ETHERNET_CSMACD:
			ad.Kind = "cable"
		}
		for u := a.FirstUnicastAddress; u != nil; u = u.Next {
			if ip := u.Address.IP(); ip.To4() != nil {
				ad.IPv4 = append(ad.IPv4, ip.String())
			} else if ip.IsGlobalUnicast() && !ip.IsPrivate() {
				ad.IPv6Global = true
			}
		}
		for g := a.FirstGatewayAddress; g != nil; g = g.Next {
			ad.Gateways = append(ad.Gateways, g.Address.IP().String())
		}
		for d := a.FirstDnsServerAddress; d != nil; d = d.Next {
			ad.DNS = append(ad.DNS, d.Address.IP().String())
		}
		out = append(out, ad)
	}
	return out, nil
}
