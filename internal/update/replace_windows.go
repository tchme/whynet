package update

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

var replaceFile = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReplaceFileW")

func lockUpdate(path string) (func(), error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(name, windows.GENERIC_WRITE|windows.DELETE, 0, nil,
		windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_TEMPORARY|windows.FILE_FLAG_DELETE_ON_CLOSE, 0)
	if errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_DELETE_PENDING) {
		return nil, ErrBusy
	}
	if err != nil {
		return nil, err
	}
	return func() { windows.CloseHandle(h) }, nil
}

func replaceExe(exe, tmp, old string) error {
	paths := [3]*uint16{}
	for i, path := range []string{exe, tmp, old} {
		name, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return err
		}
		paths[i] = name
	}
	result, _, err := replaceFile.Call(uintptr(unsafe.Pointer(paths[0])), uintptr(unsafe.Pointer(paths[1])), uintptr(unsafe.Pointer(paths[2])), 0, 0, 0)
	if result != 0 {
		return nil
	}
	// In this case Windows has moved the original to old but failed to install tmp.
	if errors.Is(err, windows.ERROR_UNABLE_TO_MOVE_REPLACEMENT_2) {
		return restoreExe(exe, old, err)
	}
	return err
}

func restoreExe(exe, old string, cause error) error {
	from, err := windows.UTF16PtrFromString(old)
	if err != nil {
		return errors.Join(cause, err)
	}
	to, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return errors.Join(cause, err)
	}
	if err := windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH); err != nil {
		return errors.Join(cause, fmt.Errorf("restore executable from %s: %w", old, err))
	}
	return cause
}
