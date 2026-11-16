package probe

import (
	"net/netip"
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

// ipListed says how zapret-style address lists (ipset-*.txt) treat ip: "true", "false" or
// "excluded", plus the matching entry. An empty ipset-all.txt is Flowseal's "any" mode:
// the bypass applies to every address. Lines are addresses or CIDR ranges.
//
// shortcut: like hostListed, checks every ipset file, not only those the running strategy uses.
func ipListed(files map[string]string, ip string) (verdict, entry string) {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return "unknown", ""
	}
	verdict = "false"
	for name, content := range files {
		lname := strings.ToLower(name)
		if !strings.HasPrefix(lname, "ipset") {
			continue
		}
		exclude := strings.Contains(lname, "exclude")
		if !exclude && strings.TrimSpace(content) == "" && lname == "ipset-all.txt" {
			if verdict == "false" {
				verdict, entry = "true", "any"
			}
			continue
		}
		for _, line := range strings.Split(content, "\n") {
			s, _, _ := strings.Cut(line, "#")
			if s = strings.TrimSpace(s); s == "" {
				continue
			}
			pfx, err := netip.ParsePrefix(s)
			if err != nil {
				a, aerr := netip.ParseAddr(s)
				if aerr != nil {
					continue
				}
				pfx = netip.PrefixFrom(a, a.BitLen())
			}
			if !pfx.Contains(addr) {
				continue
			}
			if exclude {
				return "excluded", s // an exclude list wins, as in winws
			}
			verdict, entry = "true", s
			break
		}
	}
	return verdict, entry
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
