package health

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"whynet/internal/etw"
	"whynet/internal/probe"
)

var (
	procFlushDNS    = windows.NewLazySystemDLL("dnsapi.dll").NewProc("DnsFlushResolverCache")
	procFlushARP    = iphlpapi.NewProc("FlushIpNetTable2")
	procInetSetOpt  = windows.NewLazySystemDLL("wininet.dll").NewProc("InternetSetOptionW")
	procMultiToWide = windows.NewLazySystemDLL("kernel32.dll").NewProc("MultiByteToWideChar")
)

// ErrDeclined: the person said no to the administrator prompt; the actions that need it did not run.
var ErrDeclined = etw.ErrDeclined

// adapterCheck: a name or IPv4 index of one of this PC's adapters that is up and not a VPN.
func adapterCheck(m probe.Markers) func(name string, index uint32) bool {
	ads, _ := probe.Adapters()
	return func(name string, index uint32) bool {
		for _, a := range ads {
			if m.IsVPN(a) {
				continue
			}
			if (name == "" || a.Name == name) && (index == 0 || a.Index == index) && (name != "" || index != 0) {
				return true
			}
		}
		return false
	}
}

// applyOne validates and runs one action, recovering from a panic.
func applyOne(a Action, adapterOK func(string, uint32) bool) (o Outcome) {
	o.Action = a
	defer func() {
		if p := recover(); p != nil {
			o.OK, o.Error = false, fmt.Sprint("panic: ", p)
		}
	}()
	err := Validate(a, adapterOK)
	if err == nil {
		err = apply(a)
	}
	if err != nil {
		o.Error = err.Error()
	} else {
		o.OK = true
	}
	return o
}

func apply(a Action) error {
	switch a.ID {
	case "proxy_enable":
		v, _ := strconv.Atoi(a.Value)
		return inetSetting(func(k registry.Key) error { return k.SetDWordValue("ProxyEnable", uint32(v)) })
	case "pac_set":
		return inetSetting(func(k registry.Key) error {
			if a.Value == "" {
				if err := k.DeleteValue("AutoConfigURL"); err != nil && !errors.Is(err, registry.ErrNotExist) {
					return err
				}
				return nil
			}
			return k.SetStringValue("AutoConfigURL", a.Value)
		})
	case "winhttp_set":
		if a.Value == "" {
			return run(30*time.Second, "netsh", "winhttp", "reset", "proxy")
		}
		args := []string{"winhttp", "set", "proxy", "proxy-server=" + a.Value}
		if len(a.Values) == 1 {
			args = append(args, "bypass-list="+a.Values[0])
		}
		return run(30*time.Second, "netsh", args...)
	case "service_start":
		return service(a.Value, true)
	case "service_stop":
		return service(a.Value, false)
	case "tcp_global":
		return run(30*time.Second, "netsh", "int", "tcp", "set", "global", a.Value)
	case "dhcp_renew":
		return run(90*time.Second, "ipconfig", "/renew", a.Adapter)
	case "time_resync":
		if err := service("W32Time", true); err != nil {
			return err
		}
		return run(30*time.Second, "w32tm", "/resync", "/force")
	case "flush_dns":
		if r, _, err := procFlushDNS.Call(); r == 0 {
			return err
		}
		return nil
	case "flush_arp":
		if r, _, _ := procFlushARP.Call(0, 0); r != 0 { // AF_UNSPEC, every interface
			return syscall.Errno(r)
		}
		return nil
	}
	return errors.New("health: unknown action " + a.ID)
}

// inetSetting changes the person's Internet Settings and tells running programs (WinINET) to reread them.
func inetSetting(change func(registry.Key) error) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, internetSettings, registry.SET_VALUE)
	if err != nil {
		return err
	}
	err = change(k)
	k.Close()
	if err != nil {
		return err
	}
	const settingsChanged, refresh = 39, 37
	procInetSetOpt.Call(0, settingsChanged, 0, 0)
	procInetSetOpt.Call(0, refresh, 0, 0)
	return nil
}

// editHostsFile turns lines off (a copy of the file goes next to it first) or back on.
// service starts or stops a Windows service and waits up to 15 s for it to get there.
func service(name string, start bool) error {
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return err
	}
	defer windows.CloseServiceHandle(scm)
	h, err := windows.OpenService(scm, windows.StringToUTF16Ptr(name), windows.SERVICE_START|windows.SERVICE_STOP|windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return err
	}
	defer windows.CloseServiceHandle(h)
	if start {
		if err := windows.StartService(h, 0, nil); err != nil && !errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
			return err
		}
	} else {
		var st windows.SERVICE_STATUS
		if err := windows.ControlService(h, windows.SERVICE_CONTROL_STOP, &st); err != nil && !errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
			return err
		}
	}
	return waitService(h, name, start)
}

func waitService(h windows.Handle, name string, start bool) error {
	want := uint32(windows.SERVICE_STOPPED)
	if start {
		want = windows.SERVICE_RUNNING
	}
	for range 30 {
		var st windows.SERVICE_STATUS
		if err := windows.QueryServiceStatus(h, &st); err != nil {
			return err
		}
		if st.CurrentState == want {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("health: %s did not %s in 15 s", name, map[bool]string{true: "start", false: "stop"}[start])
}

// run starts a Windows tool without a window (never through a shell) and fails with its output.
func run(limit time.Duration, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, strings.Join(strings.Fields(oemText(out)), " "))
	}
	return nil
}

// oemText decodes a console tool's output: without a console it writes in the OEM code page.
func oemText(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	const cpOEM = 1
	n, _, _ := procMultiToWide.Call(cpOEM, 0, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)), 0, 0)
	if n == 0 {
		return string(b)
	}
	w := make([]uint16, n)
	procMultiToWide.Call(cpOEM, 0, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)), uintptr(unsafe.Pointer(&w[0])), n)
	return windows.UTF16ToString(w)
}
