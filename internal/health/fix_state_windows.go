package health

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"whynet/internal/probe"
)

func reversible(id string) bool {
	return slices.Contains([]string{"proxy_enable", "pac_set", "winhttp_set", "hosts_comment", "hosts_uncomment", "dns_source", "tcp_global", "time_resync", "service_stop"}, id)
}

func fixKey(a Action) string {
	switch a.ID {
	case "hosts_comment", "hosts_uncomment":
		return "hosts"
	case "service_stop", "time_resync":
		return "W32Time"
	case "tcp_global":
		k, _, _ := strings.Cut(a.Value, "=")
		return k
	case "dns_source":
		return a.ID + ":" + strconv.FormatUint(uint64(a.Index), 10)
	default:
		return a.ID
	}
}

func (s fixState) equal(t fixState) bool {
	return s.Value == t.Value && s.Target == t.Target && s.Kind == t.Kind && slices.Equal(s.Values, t.Values)
}

func readFixState(a Action) (fixState, error) {
	var s fixState
	switch a.ID {
	case "proxy_enable", "pac_set":
		k, err := registry.OpenKey(registry.CURRENT_USER, internetSettings, registry.QUERY_VALUE)
		if err != nil {
			return s, err
		}
		defer k.Close()
		if a.ID == "proxy_enable" {
			var n uint64
			n, s.Kind, err = k.GetIntegerValue("ProxyEnable")
			if err == nil && (s.Kind != registry.DWORD || n > 1) {
				return s, errors.New("unknown ProxyEnable value")
			}
			if err == nil {
				s.Value = strconv.FormatUint(n, 10)
				var serverErr error
				s.Target, _, serverErr = k.GetStringValue("ProxyServer")
				if serverErr != nil && !errors.Is(serverErr, registry.ErrNotExist) {
					return s, serverErr
				}
			}
		} else {
			s.Value, s.Kind, err = k.GetStringValue("AutoConfigURL")
			if err == nil && s.Kind != registry.SZ && s.Kind != registry.EXPAND_SZ {
				return s, errors.New("unknown AutoConfigURL type")
			}
		}
		if errors.Is(err, registry.ErrNotExist) {
			return fixState{}, nil
		}
		return s, err
	case "winhttp_set":
		server, bypass, err := winHTTPProxy()
		s.Value = server
		if bypass != "" {
			s.Values = []string{bypass}
		}
		return s, err
	case "hosts_comment", "hosts_uncomment":
		path, err := fixedHostsPath()
		if err != nil {
			return s, err
		}
		b, err := readHostsForFix(path)
		return fixState{Value: hostsHash(b)}, err
	case "dns_source":
		adapters, err := probe.Adapters()
		if err != nil {
			return s, err
		}
		for _, adapter := range adapters {
			if adapter.Index != a.Index || adapter.Name != a.Adapter {
				continue
			}
			k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services\Tcpip\Parameters\Interfaces\`+adapter.GUID, registry.QUERY_VALUE)
			if err != nil {
				return s, err
			}
			defer k.Close()
			manual, _, err := k.GetStringValue("NameServer")
			if err != nil && !errors.Is(err, registry.ErrNotExist) {
				return s, err
			}
			s = fixState{Target: adapter.GUID, Value: "dhcp"}
			if s.Values = strings.FieldsFunc(manual, func(c rune) bool { return c == ' ' || c == ',' || c == '\t' }); len(s.Values) > 0 {
				s.Value = "static"
			}
			return s, nil
		}
		return s, errors.New("the DNS adapter is no longer available")
	case "tcp_global":
		out, err := netsh(context.Background(), "int", "tcp", "show", "global")
		if err != nil {
			return s, err
		}
		rss, tune := parseTCPGlobal(out)
		if strings.HasPrefix(a.Value, "rss=") {
			s.Value = "rss=" + rss
		} else {
			s.Value = "autotuninglevel=" + tune
		}
		if !tcpGlobalValue.MatchString(s.Value) {
			return s, errors.New("TCP setting could not be read")
		}
		return s, nil
	case "time_resync", "service_stop":
		return timeServiceState()
	}
	return s, fmt.Errorf("no snapshot for %s", a.ID)
}

func timeServiceState() (fixState, error) {
	h, err := openTimeService(windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return fixState{}, err
	}
	defer windows.CloseServiceHandle(h)
	var s windows.SERVICE_STATUS_PROCESS
	var needed uint32
	if err := windows.QueryServiceStatusEx(h, windows.SC_STATUS_PROCESS_INFO, (*byte)(unsafe.Pointer(&s)), uint32(unsafe.Sizeof(s)), &needed); err != nil {
		return fixState{}, err
	}
	switch s.CurrentState {
	case windows.SERVICE_RUNNING:
		p, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, s.ProcessId)
		if err != nil {
			return fixState{}, err
		}
		defer windows.CloseHandle(p)
		created, err := processCreated(p)
		return fixState{Value: "running", Target: fmt.Sprintf("%d:%d", s.ProcessId, created)}, err
	case windows.SERVICE_STOPPED:
		return fixState{Value: "stopped"}, nil
	default:
		return fixState{}, errors.New("W32Time is changing state")
	}
}

func openTimeService(rights uint32) (windows.Handle, error) {
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return 0, err
	}
	defer windows.CloseServiceHandle(scm)
	return windows.OpenService(scm, windows.StringToUTF16Ptr("W32Time"), rights)
}

func startTimeForFix() (bool, error) {
	h, err := openTimeService(windows.SERVICE_START | windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return false, err
	}
	defer windows.CloseServiceHandle(h)
	err = windows.StartService(h, 0, nil)
	if errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, waitService(h, "W32Time", true)
}

func desiredState(a Action, s fixState) fixState {
	s.Value, s.Values = a.Value, a.Values
	switch a.ID {
	case "proxy_enable":
		s.Kind = registry.DWORD
	case "pac_set":
		s.Kind = 0
		if a.Value != "" {
			s.Kind = registry.SZ
		}
	case "time_resync":
		s.Value = "running"
	case "service_stop":
		s.Value = "stopped"
	}
	return s
}

func stateMatchesAction(s fixState, a Action) bool {
	x := desiredState(a, s)
	return s.Value == x.Value && slices.Equal(s.Values, x.Values) && (a.ID != "pac_set" || a.Value != "" || s.Kind == 0)
}

func inverseAction(a Action, current fixState) Action {
	switch a.ID {
	case "time_resync":
		return Action{ID: "service_stop", Value: "W32Time"}
	case "hosts_comment":
		return Action{ID: "hosts_uncomment", Lines: a.Lines}
	default:
		a.Value, a.Values = current.Value, current.Values
		return a
	}
}

func partialDNS(e undoEntry, s fixState) bool {
	return e.Partial && e.Undo.ID == "dns_source" && s.Target == e.Before.Target && s.Value == "static" &&
		len(s.Values) > 0 && len(s.Values) < len(e.Before.Values) && slices.Equal(s.Values, e.Before.Values[:len(s.Values)])
}

func applyCaptured(a Action, current, goal fixState) (started bool, err error) {
	switch a.ID {
	case "proxy_enable", "pac_set":
		err = inetSetting(func(k registry.Key) error {
			name := "AutoConfigURL"
			if a.ID == "proxy_enable" {
				name = "ProxyEnable"
			}
			switch goal.Kind {
			case 0:
				if err := k.DeleteValue(name); !errors.Is(err, registry.ErrNotExist) {
					return err
				}
				return nil
			case registry.DWORD:
				v, err := strconv.ParseUint(goal.Value, 10, 32)
				if err != nil {
					return err
				}
				return k.SetDWordValue(name, uint32(v))
			case registry.SZ:
				return k.SetStringValue(name, goal.Value)
			case registry.EXPAND_SZ:
				return k.SetExpandStringValue(name, goal.Value)
			}
			return errors.New("unsupported registry value type")
		})
	case "dns_source":
		err = applyDNS(a, current, goal)
	case "time_resync":
		if current.Value == "stopped" {
			started, err = startTimeForFix()
			if err != nil {
				return started, err
			}
		}
		err = run(30*time.Second, "w32tm", "/resync", "/force")
	default:
		err = apply(a)
	}
	return started, err
}

func applyDNS(a Action, current, goal fixState) error {
	name := "name=" + strconv.FormatUint(uint64(a.Index), 10)
	if goal.Value == "dhcp" {
		return run(30*time.Second, "netsh", "interface", "ipv4", "set", "dnsservers", name, "source=dhcp")
	}
	from := len(current.Values)
	if current.Value != "static" || from == 0 || from >= len(goal.Values) || !slices.Equal(current.Values, goal.Values[:from]) {
		if err := run(30*time.Second, "netsh", "interface", "ipv4", "set", "dnsservers", name, "source=static",
			"address="+goal.Values[0], "register=primary", "validate=no"); err != nil {
			return err
		}
		from = 1
	}
	for i := from; i < len(goal.Values); i++ {
		state, err := readFixState(a)
		if err != nil || state.Target != goal.Target || state.Value != "static" || !slices.Equal(state.Values, goal.Values[:i]) {
			return errors.New("the DNS list changed before the next server could be added")
		}
		if err := run(30*time.Second, "netsh", "interface", "ipv4", "add", "dnsservers", name, "address="+goal.Values[i],
			"index="+strconv.Itoa(i+1), "validate=no"); err != nil {
			return err
		}
	}
	return nil
}
