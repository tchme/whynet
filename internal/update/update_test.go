package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"0.1.2", "0.1.1", true}, {"0.1.10", "0.1.9", true}, {"1.0.0", "0.9.9", true},
		{"0.1.1", "0.1.1", false}, {"0.1.0", "0.1.1", false}, {"v0.2.0", "0.1.1-dev", true}, {"0.1.1", "0.1.1-dev", false},
	}
	for _, c := range cases {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%s, %s) = %v", c.a, c.b, got)
		}
	}
}

// A fake GitHub: the release API and the exe. The update swaps files and keeps the old one aside.
func TestLatestAndApply(t *testing.T) {
	newExe := []byte("new whynet build")
	sum := sha256.Sum256(newExe)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			fmt.Fprintf(w, `{"tag_name":"v0.1.2","html_url":"https://github.com/tchme/whynet/releases/tag/v0.1.2",
				"assets":[{"name":"notes.txt"},{"name":"whynet.exe","size":%d,"digest":"sha256:%s","browser_download_url":"%s/whynet.exe"}]}`,
				len(newExe), hex.EncodeToString(sum[:]), srv.URL)
		case "/whynet.exe":
			w.Write(newExe)
		}
	}))
	t.Cleanup(srv.Close)
	LatestURL = srv.URL + "/latest"

	rel, err := Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rel.Version != "0.1.2" || rel.Size != int64(len(newExe)) || rel.SHA256 == "" {
		t.Fatalf("release %+v", rel)
	}

	exe := filepath.Join(t.TempDir(), "whynet.exe")
	os.WriteFile(exe, []byte("old whynet"), 0o755)
	if err := Apply(context.Background(), rel, exe); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != string(newExe) {
		t.Errorf("exe after update = %q", b)
	}
	if b, _ := os.ReadFile(exe + ".old"); string(b) != "old whynet" {
		t.Errorf("old exe kept aside = %q", b)
	}
	CleanOld(exe)
	if _, err := os.Stat(exe + ".old"); !os.IsNotExist(err) {
		t.Error("CleanOld left the old exe")
	}

	// A corrupted download never replaces the working exe.
	rel.SHA256 = "00"
	if err := Apply(context.Background(), rel, exe); err == nil {
		t.Error("a checksum mismatch must fail")
	}
	if b, _ := os.ReadFile(exe); string(b) != string(newExe) {
		t.Errorf("a failed update changed the exe: %q", b)
	}
	if _, err := os.Stat(exe + ".new"); !os.IsNotExist(err) {
		t.Error("a failed update left exe.new behind")
	}
}
