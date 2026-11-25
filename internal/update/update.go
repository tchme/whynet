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
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// LatestURL is the GitHub API for the newest release; a variable so tests can point it elsewhere.
var LatestURL = "https://api.github.com/repos/tchme/whynet/releases/latest"

const assetName = "whynet.exe"
const maxDownload = 200 << 20

var (
	ErrDigest  = errors.New("release has no valid SHA-256 digest")
	ErrBusy    = errors.New("another window is updating this executable")
	ErrCurrent = errors.New("this executable already contains the release")
)

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
			rel.URL, rel.Size = a.BrowserDownloadURL, a.Size
			if digest, ok := strings.CutPrefix(a.Digest, "sha256:"); ok && validDigest(digest) {
				rel.SHA256 = digest
			}
			break
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

// Apply verifies, installs and starts an update, restoring the previous executable if Start fails.
func Apply(ctx context.Context, rel *Release, exe string) error {
	return apply(ctx, rel, exe, func(path string) error { return exec.Command(path).Start() })
}

func apply(ctx context.Context, rel *Release, exe string, start func(string) error) error {
	if !validDigest(rel.SHA256) {
		return ErrDigest
	}
	unlock, err := lockUpdate(exe + ".update-lock")
	if err != nil {
		return err
	}
	defer unlock()

	current, err := os.Open(exe)
	if err != nil {
		return err
	}
	hash := sha256.New()
	_, err = io.Copy(hash, current)
	current.Close()
	if err != nil {
		return err
	}
	if strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), rel.SHA256) {
		return ErrCurrent
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	tmp, err := os.CreateTemp(filepath.Dir(exe), filepath.Base(exe)+".new-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	err = download(ctx, rel, tmp)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}

	old := exe + ".old"
	if err := replaceExe(exe, tmp.Name(), old); err != nil {
		return fmt.Errorf("replace executable: %w", err)
	}
	if err := start(exe); err != nil {
		return restoreExe(exe, old, fmt.Errorf("start new version: %w", err))
	}
	return nil
}

func validDigest(hash string) bool {
	if len(hash) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(hash)
	return err == nil
}

func download(ctx context.Context, rel *Release, f *os.File) error {
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
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxDownload+1))
	switch {
	case err != nil:
		return err
	case n > maxDownload:
		return errors.New("download exceeds the size limit")
	case rel.Size > 0 && n != rel.Size:
		return fmt.Errorf("download: got %d bytes, expected %d", n, rel.Size)
	case !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), rel.SHA256):
		return errors.New("download: checksum does not match the release")
	}
	return f.Sync()
}
