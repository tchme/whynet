package health

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
)

type fixOwner struct {
	PID     uint32 `json:"pid"`
	Created int64  `json:"created"`
}

func processCreated(h windows.Handle) (int64, error) {
	var start, exit, kernel, user windows.Filetime
	err := windows.GetProcessTimes(h, &start, &exit, &kernel, &user)
	return start.Nanoseconds(), err
}

func currentFixOwner() (fixOwner, error) {
	created, err := processCreated(windows.CurrentProcess())
	return fixOwner{uint32(os.Getpid()), created}, err
}

func (p fixOwner) live() bool {
	if p.PID == 0 {
		return true
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, p.PID)
	if err != nil {
		return !errors.Is(err, windows.ERROR_INVALID_PARAMETER)
	}
	defer windows.CloseHandle(h)
	created, err := processCreated(h)
	if err == nil && created != p.Created {
		return false
	}
	status, err := windows.WaitForSingleObject(h, 0)
	return err != nil || status != windows.WAIT_OBJECT_0
}

// fixState is the part of a setting that whynet's action checks or modifies.
type fixState struct {
	Value  string   `json:"value,omitempty"`
	Values []string `json:"values,omitempty"`
	Target string   `json:"target,omitempty"`
	Kind   uint32   `json:"kind,omitempty"` // registry type, zero when absent
}

type undoEntry struct {
	Action  Action   `json:"action"`
	Undo    Action   `json:"undo"`
	Before  fixState `json:"before"`
	After   fixState `json:"after"`
	Backup  string   `json:"backup,omitempty"`
	Marker  string   `json:"marker,omitempty"`
	Pending string   `json:"pending,omitempty"` // fix or undo
	Owner   fixOwner `json:"owner,omitempty"`
	Legacy  bool     `json:"legacy,omitempty"`
	Partial bool     `json:"partial,omitempty"`
	Manual  string   `json:"manual,omitempty"`
}

type undoFile struct {
	Version int         `json:"version,omitempty"`
	Saved   time.Time   `json:"saved"`
	Entries []undoEntry `json:"entries,omitempty"`
	Actions []Action    `json:"actions,omitempty"` // 0.7.0, used only when reading
}

func undoPath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "whynet", "health", "undo.json"), nil
}

func readUndo(path string) ([]undoEntry, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var u undoFile
	if err := json.Unmarshal(b, &u); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	switch {
	case u.Version == 0 && !u.Saved.IsZero() && u.Actions != nil && u.Entries == nil:
		for _, a := range u.Actions {
			u.Entries = append(u.Entries, undoEntry{Undo: a, Legacy: true})
		}
	case u.Version == 1 && !u.Saved.IsZero() && u.Actions == nil && u.Entries != nil:
	default:
		return nil, fmt.Errorf("%s: unknown undo format", path)
	}
	for _, e := range u.Entries {
		if !reversible(e.Undo.ID) || Validate(e.Undo, func(string, uint32) bool { return true }) != nil ||
			!e.Legacy && (!reversible(e.Action.ID) || fixKey(e.Action) != fixKey(e.Undo) || e.Pending != "" && e.Pending != "fix" && e.Pending != "undo") {
			return nil, fmt.Errorf("%s: invalid undo entry", path)
		}
	}
	return u.Entries, nil
}

func lockUndo(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	p, err := windows.UTF16PtrFromString(path + ".lock")
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, err
	}
	var lock windows.Overlapped
	if err := windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &lock); err != nil {
		windows.CloseHandle(h)
		return nil, err
	}
	return func() { windows.CloseHandle(h) }, nil
}

func writeUndo(path string, entries []undoEntry) error {
	if len(entries) == 0 {
		err := os.Remove(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	b, err := json.MarshalIndent(undoFile{Version: 1, Saved: time.Now(), Entries: entries}, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := syncFile(filepath.Dir(path), "undo-*.tmp", b)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	return moveFile(tmp, path, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

func syncFile(dir, pattern string, b []byte) (name string, err error) {
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", err
	}
	name = f.Name()
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(name)
		return "", err
	}
	return name, nil
}

func moveFile(from, to string, flags uint32) error {
	f, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	t, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(f, t, flags)
}
