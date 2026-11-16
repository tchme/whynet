package probe

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// browsers in order of preference, all Chromium (the same switches and DevTools protocol): Edge,
// then Chrome, then plain Chromium builds for a PC with Edge removed (a friend's Windows 10 had neither).
// Not usable (2026-11-16): Yandex Browser refuses remote debugging ("disallowed by the system admin"),
// Opera GX opens the port but aborts the navigation (ERR_ABORTED).
// shortcut: Brave and Vivaldi were not run live (none on the author's PC); confirm on the first report that has them.
var browsers = []struct{ exe, dir string }{
	{"msedge.exe", `Microsoft\Edge\Application`},
	{"chrome.exe", `Google\Chrome\Application`},
	{"brave.exe", `BraveSoftware\Brave-Browser\Application`},
	{"vivaldi.exe", `Vivaldi\Application`},
}

// findBrowser returns the first browser of the list installed here (Windows 10 and 11 alike), or "".
func findBrowser() string {
	var candidates []string
	for _, b := range browsers {
		for _, root := range []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER} {
			if k, err := registry.OpenKey(root, `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\`+b.exe, registry.QUERY_VALUE); err == nil {
				if p, _, err := k.GetStringValue(""); err == nil {
					candidates = append(candidates, strings.Trim(p, `"`)) // some installers quote the path
				}
				k.Close()
			}
		}
		for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles", "LOCALAPPDATA"} {
			if base := os.Getenv(env); base != "" {
				candidates = append(candidates, filepath.Join(base, b.dir, b.exe))
			}
		}
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return c
		}
	}
	return ""
}

var (
	user32          = windows.NewLazySystemDLL("user32.dll")
	procGetWindowT  = user32.NewProc("GetWindowTextW")
	procPostMessage = user32.NewProc("PostMessageW")
)

// One callback for the whole program: Go can create only ~2000 and never frees them.
var (
	enumMu  sync.Mutex
	enumPID uint32
	enumOut []windows.HWND
	enumCB  = windows.NewCallback(func(h windows.HWND, _ uintptr) uintptr {
		var owner uint32
		windows.GetWindowThreadProcessId(h, &owner)
		if (enumPID == 0 || owner == enumPID) && windows.IsWindowVisible(h) { // 0: every process
			enumOut = append(enumOut, h)
		}
		return 1 // keep going
	})
)

// browserWindows lists the visible top-level windows of a process (0: of every process).
func browserWindows(pid uint32) []windows.HWND {
	enumMu.Lock()
	defer enumMu.Unlock()
	enumPID, enumOut = pid, nil
	windows.EnumWindows(enumCB, nil)
	return enumOut
}

// windowTitle is the title of the process's first visible window (a browser puts the page title there).
func windowTitle(pid uint32) string {
	for _, h := range browserWindows(pid) {
		buf := make([]uint16, 512)
		n, _, _ := procGetWindowT.Call(uintptr(h), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		if n > 0 {
			return windows.UTF16ToString(buf[:n])
		}
	}
	return ""
}

// closeWindows asks the process's windows to close, as the close button does.
func closeWindows(pid uint32) {
	const wmClose = 0x0010
	for _, h := range browserWindows(pid) {
		procPostMessage.Call(uintptr(h), wmClose, 0, 0)
	}
}

// hideWindow keeps the browser from flashing a console or window.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}

// DefaultBrowserFamily is "firefox" when Firefox opens web links on this PC, else "chromium".
func DefaultBrowserFamily() string {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\Shell\Associations\UrlAssociations\https\UserChoice`, registry.QUERY_VALUE)
	if err != nil {
		return "chromium"
	}
	defer k.Close()
	if id, _, _ := k.GetStringValue("ProgId"); strings.Contains(strings.ToLower(id), "firefox") {
		return "firefox"
	}
	return "chromium"
}
