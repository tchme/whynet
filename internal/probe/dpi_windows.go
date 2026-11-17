package probe

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

type dpiProc struct {
	Tool    string `json:"tool"`
	PID     uint32 `json:"pid"`
	Exe     string `json:"exe,omitempty"`
	Cmdline string `json:"cmdline,omitempty"`
	Error   string `json:"error,omitempty"`
}

// dpiInfo finds DPI-bypass tools (zapret/Flowseal, GoodbyeDPI) and the WinDivert driver
// they run on, and whether host is in zapret's lists. All of it works without admin rights.
// lists are the bundle's list files, for the address check once the address is known.
func dpiInfo(host string, m Markers) (facts map[string]string, ev map[string]any, lists map[string]string) {
	facts = map[string]string{"local.dpi_tool": "none", "local.dpi_installed": "false", "local.windivert": "absent"}
	ev = map[string]any{}
	running := map[string]bool{}
	var paths []string // exe, service and driver paths that may lead to the bundle folder

	procs, err := dpiProcesses(m.DPIProcesses)
	if err != nil {
		ev["processes_error"] = err.Error()
	}
	for _, p := range procs {
		running[p.Tool] = true
		paths = append(paths, p.Exe)
	}
	if len(procs) > 0 {
		ev["processes"] = procs
	}

	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		ev["services_error"] = err.Error()
	} else {
		defer windows.CloseServiceHandle(scm)
	}
	services := map[string]any{}
	look := func(name string) (state string) {
		state, image := serviceState(scm, name)
		if state != "absent" {
			services[name] = map[string]string{"state": state, "image_path": image}
			facts["local.dpi_installed"] = "true"
			paths = append(paths, image)
		}
		return state
	}
	for name, tool := range m.DPIServices {
		if look(name) == "running" {
			running[tool] = true
		}
	}
	for _, name := range m.WinDivert {
		if st := look(name); st != "absent" && facts["local.windivert"] != "running" {
			facts["local.windivert"] = st
		}
	}
	if len(services) > 0 {
		ev["services"] = services
	}

	switch tools := slices.Sorted(maps.Keys(running)); len(tools) {
	case 0:
	case 1:
		facts["local.dpi_tool"] = tools[0]
	default:
		facts["local.dpi_tool"] = "several"
		ev["tools"] = tools
	}

	if facts["local.dpi_tool"] != "none" || facts["local.dpi_installed"] == "true" {
		facts["local.dpi_listed"] = "unknown"
		for _, p := range paths {
			root := dpiRoot(p)
			if !filepath.IsAbs(root) {
				continue // an unreadable exe path would turn into "lists" under the current folder
			}
			if files := readLists(root); files != nil {
				verdict, in := hostListed(files, host)
				facts["local.dpi_listed"] = verdict
				ev["folder"] = root
				ev["lists"] = slices.Sorted(maps.Keys(files))
				if len(in) > 0 {
					ev["listed_in"] = in
				}
				lists = files
				// Flowseal's own settings, as its service.bat menu shows them.
				if f := ipsetFilter(files); f != "" {
					facts["local.dpi_ipset_filter"] = f
				}
				if _, err := os.Stat(filepath.Join(root, "service.bat")); err == nil {
					b, _ := os.ReadFile(filepath.Join(root, "utils", "game_filter.enabled"))
					mode, tcp, udp := gameFilter(string(b))
					facts["local.dpi_game_filter"] = mode
					if mode != "disabled" {
						ev["game_filter"] = map[string]string{"tcp": tcp, "udp": udp}
					}
				}
				break
			}
		}
	}
	return facts, ev, lists
}

// ServiceState is a Windows service's state: "running", "stopped", "absent" or "unknown".
func ServiceState(name string) string {
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err == nil {
		defer windows.CloseServiceHandle(scm)
	}
	st, _ := serviceState(scm, name)
	return st
}

// RunningTools returns the labels of running processes whose exe name is in names (lowercase exe -> label).
func RunningTools(names map[string]string) ([]string, error) {
	ps, err := dpiProcesses(names)
	var out []string
	for _, p := range ps {
		if !slices.Contains(out, p.Tool) {
			out = append(out, p.Tool)
		}
	}
	return out, err
}

// serviceState is "running", "stopped", "absent" or "unknown", plus the service's command line.
// The registry key tells whether the service exists; the SCM tells its state.
func serviceState(scm windows.Handle, name string) (state, image string) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services\`+name, registry.QUERY_VALUE)
	if err != nil {
		return "absent", ""
	}
	image, _, _ = k.GetStringValue("ImagePath")
	k.Close()
	if scm == 0 {
		return "unknown", image
	}
	h, err := windows.OpenService(scm, windows.StringToUTF16Ptr(name), windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return "unknown", image
	}
	defer windows.CloseServiceHandle(h)
	var st windows.SERVICE_STATUS
	if err := windows.QueryServiceStatus(h, &st); err != nil {
		return "unknown", image
	}
	if st.CurrentState == windows.SERVICE_RUNNING {
		return "running", image
	}
	return "stopped", image
}

// dpiProcesses lists running processes whose exe name is in names (lowercase name -> tool).
func dpiProcesses(names map[string]string) ([]dpiProc, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)
	var out []dpiProc
	e := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		tool, ok := names[strings.ToLower(windows.UTF16ToString(e.ExeFile[:]))]
		if !ok {
			continue
		}
		p := dpiProc{Tool: tool, PID: e.ProcessID}
		if err := procDetails(&p); err != nil {
			p.Error = err.Error()
		}
		out = append(out, p)
	}
	return out, nil
}

// procDetails reads the exe path and command line (the bypass strategy). An elevated
// process still allows this limited query from a normal one.
func procDetails(p *dpiProc) error {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, p.PID)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return err
	}
	p.Exe = windows.UTF16ToString(buf[:n])
	b := make([]byte, 64<<10)
	var ret uint32
	if err := windows.NtQueryInformationProcess(h, windows.ProcessCommandLineInformation, unsafe.Pointer(&b[0]), uint32(len(b)), &ret); err != nil {
		return err
	}
	p.Cmdline = (*windows.NTUnicodeString)(unsafe.Pointer(&b[0])).String()
	return nil
}
