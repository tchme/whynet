package health

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var replaceFileW = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReplaceFileW")
var errHostsReplacementConflict = errors.New("hosts changed during replacement")

func fixedHostsPath() (string, error) {
	dir, err := windows.GetSystemDirectory()
	return filepath.Join(dir, "drivers", "etc", "hosts"), err
}

func hostsHash(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }

func readHostsForFix(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("hosts is not a regular file: %s", path)
	}
	b, err := os.ReadFile(path)
	if bytes.HasPrefix(b, []byte{0xff, 0xfe}) || bytes.HasPrefix(b, []byte{0xfe, 0xff}) {
		return nil, fmt.Errorf("hosts uses an unsupported UTF-16 encoding: %s", path)
	}
	return b, err
}

func prepareHostEdit(b []byte, lines []string, marker string, undo bool) ([]byte, bool) {
	count := map[string]int{}
	out, changed := editHosts(string(b), func(body string) (string, bool) {
		line := body
		var marked bool
		if undo {
			if marker != "" {
				line, marked = strings.CutPrefix(body, marker)
			} else if rest, ok := strings.CutPrefix(body, "# [whynet "); ok {
				if date, original, cut := strings.Cut(rest, "] "); cut {
					if _, err := time.Parse("2006-01-02", date); err == nil {
						line, marked = original, true
					}
				}
			}
		}
		if !slices.Contains(lines, line) {
			return body, false
		}
		count[line]++
		if undo {
			return line, marked
		}
		return marker + line, true
	})
	for _, line := range lines {
		if count[line] == 0 || marker == "" && count[line] > 1 {
			return nil, false
		}
	}
	if changed == 0 {
		return b, true
	}
	return []byte(out), true
}

func backupHosts(path string, b []byte) (string, error) {
	backup, err := syncFile(filepath.Dir(path), filepath.Base(path)+".whynet-"+time.Now().Format("2006-01-02")+"-*", b)
	if err != nil {
		return "", err
	}
	check, err := os.ReadFile(backup)
	if err != nil || !bytes.Equal(b, check) {
		return "", fmt.Errorf("cannot verify the hosts backup at %s", backup)
	}
	return backup, nil
}

func replaceHosts(path string, before, after []byte, backup string) error {
	next, err := syncFile(filepath.Dir(path), "hosts-whynet-*.tmp", after)
	if err != nil {
		return err
	}
	defer os.Remove(next)
	if got, err := os.ReadFile(next); err != nil || !bytes.Equal(got, after) {
		return fmt.Errorf("cannot verify the prepared hosts file")
	}
	if got, err := readHostsForFix(path); err != nil || !bytes.Equal(got, before) {
		return fmt.Errorf("hosts changed before replacement; original copy: %s", backup)
	}
	retired := filepath.Join(filepath.Dir(path), "hosts.whynet-replaced-"+rand.Text())
	if err := replaceFile(path, next, retired); err != nil {
		if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
			// A failed ReplaceFile can move the original to its backup before returning.
			if moveErr := moveFile(retired, path, windows.MOVEFILE_WRITE_THROUGH); moveErr != nil {
				return fmt.Errorf("hosts replacement failed: %w; displaced file: %s; original copy: %s", err, retired, backup)
			}
		}
		return fmt.Errorf("hosts replacement failed: %w; original copy: %s", err, backup)
	}
	got, err := os.ReadFile(retired)
	if err != nil || !bytes.Equal(got, before) {
		return fmt.Errorf("%w; displaced file: %s; original copy: %s", errHostsReplacementConflict, retired, backup)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, after) {
		return fmt.Errorf("cannot verify replaced hosts; displaced file: %s; original copy: %s", retired, backup)
	}
	return os.Remove(retired)
}

func replaceFile(path, replacement, backup string) error {
	pathW, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	nextW, err := windows.UTF16PtrFromString(replacement)
	if err != nil {
		return err
	}
	backupW, err := windows.UTF16PtrFromString(backup)
	if err != nil {
		return err
	}
	if ok, _, err := replaceFileW.Call(uintptr(unsafe.Pointer(pathW)), uintptr(unsafe.Pointer(nextW)), uintptr(unsafe.Pointer(backupW)), 0, 0, 0); ok == 0 {
		return err
	}
	return nil
}
