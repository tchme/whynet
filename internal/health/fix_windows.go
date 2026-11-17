package health

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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

// Execute runs a batch: the actions that need no rights in this process, the rest through the
// elevated helper (one UAC prompt) unless this process already runs elevated. progress is called
// after each action. When the prompt is declined, the admin actions come back failed and declined is true.
func Execute(actions []Action, m probe.Markers, progress func(Outcome)) (outs []Outcome, declined bool) {
	var mine, admin []Action
	for _, a := range actions {
		if a.Admin() && !etw.Elevated() {
			admin = append(admin, a)
		} else {
			mine = append(mine, a)
		}
	}
	report := func(o Outcome) {
		outs = append(outs, o)
		if progress != nil {
			progress(o)
		}
	}
	ok := adapterCheck(m)
	for _, a := range mine {
		report(applyOne(a, ok))
	}
	if len(admin) == 0 {
		return outs, false
	}
	err := viaHelper(admin, report)
	if err == nil {
		return outs, false
	}
	// What the helper did not answer for counts as not done.
	for _, a := range admin {
		if !slicesHas(outs, a) {
			report(Outcome{Action: a, Error: err.Error()})
		}
	}
	return outs, errors.Is(err, etw.ErrDeclined)
}

func slicesHas(outs []Outcome, a Action) bool {
	for _, o := range outs {
		if actionEqual(o.Action, a) {
			return true
		}
	}
	return false
}

// viaHelper sends the actions to an elevated copy of whynet and reads one outcome per line.
func viaHelper(actions []Action, report func(Outcome)) error {
	conn, rd, err := etw.Elevate("fix", 2*time.Minute)
	if err != nil {
		return err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(2 * time.Minute))
	b, _ := json.Marshal(actions)
	if _, err := conn.Write(append(b, '\n')); err != nil {
		return err
	}
	dec := json.NewDecoder(rd)
	for range actions {
		var o Outcome
		if err := dec.Decode(&o); err != nil {
			return fmt.Errorf("health: the helper stopped answering: %w", err)
		}
		report(o)
	}
	return nil
}

// FixJob is the elevated helper's "fix" job: one JSON line of actions in, one JSON line per outcome
// out. Every action is checked again here (Validate): the helper trusts nothing it is sent.
func FixJob(m probe.Markers) etw.Job {
	return func(rw *bufio.ReadWriter) error {
		line, err := rw.ReadBytes('\n')
		if err != nil {
			return err
		}
		var actions []Action
		if err := json.Unmarshal(line, &actions); err != nil {
			return err
		}
		ok := adapterCheck(m)
		enc := json.NewEncoder(rw)
		for _, a := range actions {
			if err := enc.Encode(applyOne(a, ok)); err != nil {
				return err
			}
			rw.Flush()
		}
		return nil
	}
}

// adapterCheck: a name or IPv4 index of one of this PC's adapters that is up and not a VPN.
func adapterCheck(m probe.Markers) func(name string, index uint32) bool {
	ads, _ := probe.Adapters()
	return func(name string, index uint32) bool {
		for _, a := range ads {
			if m.IsVPN(a) {
				continue
			}
			if name != "" && a.Name == name || index != 0 && a.Index == index {
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
	case "hosts_comment":
		return editHostsFile(a.Lines, true)
	case "hosts_uncomment":
		return editHostsFile(a.Lines, false)
	case "dns_source":
		name := "name=" + strconv.FormatUint(uint64(a.Index), 10)
		if a.Value == "dhcp" {
			return run(30*time.Second, "netsh", "interface", "ipv4", "set", "dnsservers", name, "source=dhcp")
		}
		if err := run(30*time.Second, "netsh", "interface", "ipv4", "set", "dnsservers", name, "source=static",
			"address="+a.Values[0], "register=primary", "validate=no"); err != nil {
			return err
		}
		for i, v := range a.Values[1:] {
			if err := run(30*time.Second, "netsh", "interface", "ipv4", "add", "dnsservers", name, "address="+v,
				"index="+strconv.Itoa(i+2), "validate=no"); err != nil {
				return err
			}
		}
		return nil
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
func editHostsFile(lines []string, off bool) error {
	path := hostsPath()
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var out string
	var n int
	if off {
		now := time.Now()
		out, n = commentHosts(string(b), lines, now)
		if n > 0 {
			backup := path + ".whynet-" + now.Format("2006-01-02")
			for i := 2; ; i++ { // never overwrite an older copy
				if _, err := os.Stat(backup); errors.Is(err, os.ErrNotExist) {
					break
				}
				backup = path + ".whynet-" + now.Format("2006-01-02") + "-" + strconv.Itoa(i)
			}
			if err := os.WriteFile(backup, b, 0o644); err != nil {
				return fmt.Errorf("health: hosts copy: %w", err)
			}
		}
	} else {
		out, n = uncommentHosts(string(b), lines)
	}
	if n == 0 {
		return errors.New("health: none of the lines are in hosts any more")
	}
	return os.WriteFile(path, []byte(out), 0o644)
}

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
	want := uint32(windows.SERVICE_RUNNING)
	if start {
		if err := windows.StartService(h, 0, nil); err != nil && !errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
			return err
		}
	} else {
		want = windows.SERVICE_STOPPED
		var st windows.SERVICE_STATUS
		if err := windows.ControlService(h, windows.SERVICE_CONTROL_STOP, &st); err != nil && !errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
			return err
		}
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

// The undo file: the actions that take back every fix batch since the last undo, newest first.
type undoFile struct {
	Saved   time.Time `json:"saved"`
	Actions []Action  `json:"actions"`
}

func undoPath() string {
	dir, err := os.UserCacheDir() // %LOCALAPPDATA%
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "whynet", "health", "undo.json")
}

// SaveUndo puts a batch's undo in front of what is already saved (a later batch is undone first).
func SaveUndo(actions []Action) error {
	if len(actions) == 0 {
		return nil
	}
	u := undoFile{Saved: time.Now(), Actions: slices.Concat(actions, LoadUndo())}
	b, err := json.MarshalIndent(u, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(undoPath()), 0o755); err != nil {
		return err
	}
	return os.WriteFile(undoPath(), b, 0o644)
}

// LoadUndo is what "Вернуть как было" would run; nil when nothing is saved.
func LoadUndo() []Action {
	b, err := os.ReadFile(undoPath())
	if err != nil {
		return nil
	}
	var u undoFile
	if json.Unmarshal(b, &u) != nil {
		return nil
	}
	return u.Actions
}

// KeepUndo saves what an undo could not take back (to try again), or removes the file when all went.
func KeepUndo(failed []Action) error {
	if len(failed) == 0 {
		err := os.Remove(undoPath())
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	b, err := json.MarshalIndent(undoFile{Saved: time.Now(), Actions: failed}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(undoPath(), b, 0o644)
}
