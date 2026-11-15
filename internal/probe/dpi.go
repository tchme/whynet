package probe

import (
	"os"
	"path/filepath"
	"strings"
)

// dpiRoot finds a DPI-bypass bundle folder from an exe path, a service command line
// ("C:\z\bin\winws.exe" --args) or a driver path (\??\C:\z\bin\WinDivert64.sys):
// the folder holding the file, or the one above it when that is "bin".
func dpiRoot(path string) string {
	p := strings.TrimPrefix(strings.TrimSpace(path), `\??\`)
	if rest, ok := strings.CutPrefix(p, `"`); ok {
		p, _, _ = strings.Cut(rest, `"`)
	} else if i := strings.Index(strings.ToLower(p), ".exe "); i >= 0 {
		p = p[:i+4]
	}
	if p == "" {
		return ""
	}
	dir := filepath.Dir(p)
	if strings.EqualFold(filepath.Base(dir), "bin") {
		dir = filepath.Dir(dir)
	}
	return dir
}

// hostListed says how zapret-style domain lists treat host: "true", "false" or "excluded".
// files maps a list file name to its content. A line names a domain and its subdomains;
// "^domain" names only the domain itself. ipset-* files hold addresses and are skipped.
//
// shortcut: checks every list in the folder, not only those the running strategy uses;
// read the strategy's --hostlist arguments if this misleads.
func hostListed(files map[string]string, host string) (verdict string, in []string) {
	excluded := false
	for name, content := range files {
		lname := strings.ToLower(name)
		if strings.HasPrefix(lname, "ipset") {
			continue
		}
		for _, line := range strings.Split(content, "\n") {
			d, _, _ := strings.Cut(line, "#")
			d = strings.ToLower(strings.TrimSpace(d))
			exact := strings.HasPrefix(d, "^")
			d = strings.TrimPrefix(d, "^")
			if d == "" || host != d && (exact || !strings.HasSuffix(host, "."+d)) {
				continue
			}
			if strings.Contains(lname, "exclude") {
				excluded = true
			} else {
				in = append(in, name)
			}
			break
		}
	}
	switch {
	case excluded:
		return "excluded", in
	case len(in) > 0:
		return "true", in
	}
	return "false", nil
}

// readLists loads the .txt lists from a bundle's "lists" folder; nil if there is none.
func readLists(root string) map[string]string {
	entries, err := os.ReadDir(filepath.Join(root, "lists"))
	if err != nil {
		return nil
	}
	files := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".txt") {
			continue
		}
		if info, err := e.Info(); err != nil || info.Size() > 16<<20 {
			continue
		}
		if b, err := os.ReadFile(filepath.Join(root, "lists", e.Name())); err == nil {
			files[e.Name()] = string(b)
		}
	}
	return files
}
