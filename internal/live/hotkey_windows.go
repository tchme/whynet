package live

import (
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32                 = windows.NewLazySystemDLL("user32.dll")
	procRegisterHotKey     = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey   = user32.NewProc("UnregisterHotKey")
	procGetMessageW        = user32.NewProc("GetMessageW")
	procPostThreadMessageW = user32.NewProc("PostThreadMessageW")
)

const (
	modAlt      = 0x0001
	modControl  = 0x0002
	modNoRepeat = 0x4000
	wmHotkey    = 0x0312
	wmQuit      = 0x0012
	hotkeyID    = 0x5701
)

type msg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	X, Y    int32
	Private uint32
}

// Hotkey is Ctrl+Alt+L, system-wide: it works while a game has the focus. Windows ties a hotkey to
// the thread that registered it, so that thread is locked and runs its own message loop.
type Hotkey struct {
	tid  uint32
	done chan struct{}
}

// ListenHotkey calls onPress on every Ctrl+Alt+L until Close. It fails when another program holds
// the combination.
func ListenHotkey(onPress func()) (*Hotkey, error) {
	h := &Hotkey{done: make(chan struct{})}
	started := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(h.done)
		h.tid = windows.GetCurrentThreadId()
		if r, _, err := procRegisterHotKey.Call(0, hotkeyID, modControl|modAlt|modNoRepeat, 'L'); r == 0 {
			started <- err
			return
		}
		defer procUnregisterHotKey.Call(0, hotkeyID)
		started <- nil
		var m msg
		for {
			r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
			if int32(r) <= 0 { // WM_QUIT, or an error
				return
			}
			if m.Message == wmHotkey && m.WParam == hotkeyID {
				func() {
					defer func() { recover() }()
					onPress()
				}()
			}
		}
	}()
	if err := <-started; err != nil {
		<-h.done
		return nil, err
	}
	return h, nil
}

// Close frees the combination for other programs.
func (h *Hotkey) Close() {
	if h == nil {
		return
	}
	procPostThreadMessageW.Call(uintptr(h.tid), wmQuit, 0, 0)
	<-h.done
}
