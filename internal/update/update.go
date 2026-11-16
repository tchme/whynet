// Package update finds a newer whynet on GitHub Releases and swaps the running exe for it.
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// LatestURL is the GitHub API for the newest release; a variable so tests can point it elsewhere.
var LatestURL = "https://api.github.com/repos/tchme/whynet/releases/latest"

const assetName = "whynet.exe"

// Release is a published version with its exe.
type Release struct {
	Version string `json:"version"` // "0.1.2"
	Page    string `json:"page"`    // the release page, for a manual download
	URL     string `json:"-"`       // the exe
	Size    int64  `json:"-"`
	SHA256  string `json:"-"` // hex; empty when GitHub didn't publish a digest
}

// Latest asks GitHub for the newest release that has a whynet.exe.
func Latest(ctx context.Context) (*Release, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", LatestURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "whynet-update")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub: HTTP %d", resp.StatusCode)
	}
	var r struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
		Assets  []struct {
			Name               string `json:"name"`
			Size               int64  `json:"size"`
			Digest             string `json:"digest"` // "sha256:<hex>"
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r); err != nil {
		return nil, err
	}
	rel := &Release{Version: strings.TrimPrefix(r.TagName, "v"), Page: r.HTMLURL}
	for _, a := range r.Assets {
		if a.Name == assetName {
			rel.URL, rel.Size, rel.SHA256 = a.BrowserDownloadURL, a.Size, strings.TrimPrefix(a.Digest, "sha256:")
		}
	}
	if rel.URL == "" {
		return nil, errors.New("the latest release has no " + assetName)
	}
	return rel, nil
}

// Newer reports whether version a is newer than b ("0.1.10" > "0.1.9"; a suffix like "-dev" is ignored).
func Newer(a, b string) bool {
	pa, pb := parts(a), parts(b)
	for i := range 3 {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func parts(v string) [3]int {
	var out [3]int
	v, _, _ = strings.Cut(strings.TrimPrefix(v, "v"), "-")
	for i, p := range strings.SplitN(v, ".", 3) {
		out[i], _ = strconv.Atoi(p)
	}
	return out
}

// Apply downloads the release next to exe, checks it, and swaps the files: the running exe can't
// be overwritten on Windows but can be renamed, so it becomes exe.old (removed by CleanOld on the
// next start) and the new file takes its name. On any error the old exe stays in place.
func Apply(ctx context.Context, rel *Release, exe string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	tmp := exe + ".new"
	if err := download(ctx, rel, tmp); err != nil {
		os.Remove(tmp)
		return err
	}
	old := exe + ".old"
	os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, exe); err != nil {
		os.Rename(old, exe) // put the working one back
		os.Remove(tmp)
		return err
	}
	return nil
}

func download(ctx context.Context, rel *Release, path string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", rel.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "whynet-update")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, 200<<20))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	switch {
	case err != nil:
		return err
	case rel.Size > 0 && n != rel.Size:
		return fmt.Errorf("download: got %d bytes, expected %d", n, rel.Size)
	case rel.SHA256 != "" && !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), rel.SHA256):
		return errors.New("download: checksum does not match the release")
	}
	return nil
}

// CleanOld removes the previous exe left by an update.
func CleanOld(exe string) {
	os.Remove(exe + ".old")
}
