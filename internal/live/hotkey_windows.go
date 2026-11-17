package live

import (
	"time"

	"golang.org/x/sys/windows"
)

var (
	user32               = windows.NewLazySystemDLL("user32.dll")
	procGetAsyncKeyState = user32.NewProc("GetAsyncKeyState")
)

const (
	vkControl  = 0x11
	vkMenu     = 0x12 // Alt
	hotkeyPoll = 30 * time.Millisecond
)

// Hotkey watches Ctrl+Alt+L system-wide by reading the keyboard state every 30 ms. RegisterHotKey is
// not used: a game that reads raw input with RIDEV_NOHOTKEYS switches registered hotkeys off while
// it has the focus (Frostpunk, 2026-11-17: the keys reached the game, never whynet). Polling only
// reads the key state and swallows nothing, so the game sees the keys too.
type Hotkey struct {
	stop, done chan struct{}
}

// ListenHotkey calls onPress on every press of Ctrl+Alt+L until Close. The error is kept for the
// callers: polling cannot be refused, unlike a registered hotkey another program holds.
func ListenHotkey(onPress func()) (*Hotkey, error) {
	h := &Hotkey{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(h.done)
		t := time.NewTicker(hotkeyPoll)
		defer t.Stop()
		was := false
		for {
			select {
			case <-h.stop:
				return
			case <-t.C:
			}
			down := keyDown(vkControl) && keyDown(vkMenu) && keyDown('L')
			if down && !was { // once per press, however long it is held
				func() {
					defer func() { recover() }()
					onPress()
				}()
			}
			was = down
		}
	}()
	return h, nil
}

func keyDown(vk uintptr) bool {
	r, _, _ := procGetAsyncKeyState.Call(vk)
	return r&0x8000 != 0
}

// Close stops watching.
func (h *Hotkey) Close() {
	if h == nil {
		return
	}
	close(h.stop)
	<-h.done
}
