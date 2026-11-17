package main

import (
	"os"
	"unsafe"

	"github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/sys/windows"
)

var (
	user32           = windows.NewLazySystemDLL("user32.dll")
	procFindWindowEx = user32.NewProc("FindWindowExW")
	procGetRect      = user32.NewProc("GetWindowRect")
	procMonitorFrom  = user32.NewProc("MonitorFromWindow")
	procMonitorInfo  = user32.NewProc("GetMonitorInfoW")
	procSetPos       = user32.NewProc("SetWindowPos")
)

// FitWindow resizes the window (in the page's px, as Wails takes them) and keeps it inside the work area
// of the monitor it is on. Not done in the page: Wails' WindowGetPosition is in desktop coordinates while
// WindowSetPosition counts from the monitor's corner, so on a monitor left of the main one every move
// pushed the window another monitor-width off the screen (docs/bugs.md).
func (a *App) FitWindow(w, h int) {
	runtime.WindowSetSize(a.ctx, w, h)
	hwnd := ownWindow()
	if hwnd == 0 {
		return
	}
	var r windows.Rect
	if ok, _, _ := procGetRect.Call(hwnd, uintptr(unsafe.Pointer(&r))); ok == 0 {
		return
	}
	const nearest = 2 // MONITOR_DEFAULTTONEAREST
	mon, _, _ := procMonitorFrom.Call(hwnd, nearest)
	mi := struct {
		size      uint32
		mon, work windows.Rect
		flags     uint32
	}{size: 40}
	if ok, _, _ := procMonitorInfo.Call(mon, uintptr(unsafe.Pointer(&mi))); ok == 0 {
		return
	}
	x := keepInside(r.Left, r.Right-r.Left, mi.work.Left, mi.work.Right)
	y := keepInside(r.Top, r.Bottom-r.Top, mi.work.Top, mi.work.Bottom)
	if x != r.Left || y != r.Top {
		const flags = 0x0001 | 0x0004 | 0x0010 // SWP_NOSIZE | SWP_NOZORDER | SWP_NOACTIVATE
		procSetPos.Call(hwnd, 0, uintptr(x), uintptr(y), 0, 0, flags)
	}
}

// keepInside moves a span [pos, pos+size) into [lo, hi); a span wider than that starts at lo.
func keepInside(pos, size, lo, hi int32) int32 {
	return max(lo, min(pos, hi-size))
}

// ownWindow is this process's Wails window (class "wailsWindow"; another Wails app may have one too).
func ownWindow() uintptr {
	class, _ := windows.UTF16PtrFromString("wailsWindow")
	me := uint32(os.Getpid())
	var h uintptr
	for {
		h, _, _ = procFindWindowEx.Call(0, h, uintptr(unsafe.Pointer(class)), 0)
		if h == 0 {
			return 0
		}
		var pid uint32
		windows.GetWindowThreadProcessId(windows.HWND(h), &pid)
		if pid == me {
			return h
		}
	}
}
