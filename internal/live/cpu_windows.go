package live

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var procGetSystemTimes = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetSystemTimes")

// systemCPUTimes is the whole computer's CPU time since boot, in 100 ns units: idle and all
// (kernel time includes idle). Needs no admin rights.
func systemCPUTimes() (idle, total uint64, err error) {
	var i, k, u windows.Filetime
	if r, _, e := procGetSystemTimes.Call(uintptr(unsafe.Pointer(&i)), uintptr(unsafe.Pointer(&k)), uintptr(unsafe.Pointer(&u))); r == 0 {
		return 0, 0, e
	}
	ft := func(f windows.Filetime) uint64 { return uint64(f.HighDateTime)<<32 | uint64(f.LowDateTime) }
	return ft(i), ft(k) + ft(u), nil
}
