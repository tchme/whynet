package update

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	previousURL := LatestURL
	t.Cleanup(func() { LatestURL = previousURL })
	LatestURL = srv.URL + "/latest"

	rel, err := Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rel.Version != "0.1.2" || rel.Size != int64(len(newExe)) || rel.SHA256 == "" {
		t.Fatalf("release %+v", rel)
	}

	exe := filepath.Join(t.TempDir(), "whynet.exe")
	writeUpdateFixture(t, exe, "old whynet")
	started := 0
	start := func(path string) error {
		started++
		if path != exe {
			t.Errorf("started %q, want %q", path, exe)
		}
		return nil
	}
	if err := apply(context.Background(), rel, exe, start); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != string(newExe) {
		t.Errorf("exe after update = %q", b)
	}
	if b, _ := os.ReadFile(exe + ".old"); string(b) != "old whynet" {
		t.Errorf("old exe kept aside = %q", b)
	}
	if err := apply(context.Background(), rel, exe, start); !errors.Is(err, ErrCurrent) || started != 1 {
		t.Fatalf("repeat installation: started %d, error %v", started, err)
	}

	// A corrupted download never replaces the working exe.
	rel.SHA256 = strings.Repeat("0", 64)
	if err := apply(context.Background(), rel, exe, start); err == nil || errors.Is(err, ErrDigest) {
		t.Error("a checksum mismatch must fail")
	}
	if b, _ := os.ReadFile(exe); string(b) != string(newExe) {
		t.Errorf("a failed update changed the exe: %q", b)
	}
	if matches, _ := filepath.Glob(exe + ".new-*"); len(matches) != 0 {
		t.Errorf("a failed update left downloads behind: %v", matches)
	}
}

func TestReleaseWithoutValidDigestCannotInstall(t *testing.T) {
	previousURL := LatestURL
	t.Cleanup(func() { LatestURL = previousURL })
	for _, digest := range []string{"", strings.Repeat("a", 64), "sha256:xyz", "sha1:" + strings.Repeat("a", 64)} {
		t.Run(digest, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/latest" {
					t.Error("unverified release requested a download")
					return
				}
				fmt.Fprintf(w, `{"tag_name":"v2.0.0","html_url":"https://example.org/release","assets":[{"name":"whynet.exe","digest":%q,"browser_download_url":"http://%s/whynet.exe"}]}`, digest, r.Host)
			}))
			defer server.Close()
			LatestURL = server.URL + "/latest"
			rel, err := Latest(t.Context())
			if err != nil || rel.Page == "" || rel.SHA256 != "" {
				t.Fatalf("release for manual download: %+v, error %v", rel, err)
			}
			exe := filepath.Join(t.TempDir(), "whynet.exe")
			writeUpdateFixture(t, exe, "working executable")
			if err := Apply(t.Context(), rel, exe); !errors.Is(err, ErrDigest) {
				t.Fatalf("Apply error = %v, want ErrDigest", err)
			}
			if b, _ := os.ReadFile(exe); string(b) != "working executable" {
				t.Fatalf("working executable changed: %q", b)
			}
		})
	}
}

func TestUpdateStartFailureRestoresWorkingExecutable(t *testing.T) {
	newExe := "not an executable"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, newExe) }))
	t.Cleanup(server.Close)
	rel := &Release{URL: server.URL, Size: int64(len(newExe)), SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(newExe)))}
	exe := filepath.Join(t.TempDir(), "whynet.exe")
	oldExe := "working executable"
	writeUpdateFixture(t, exe, oldExe)
	writeUpdateFixture(t, exe+".old", "backup from earlier update")

	if err := Apply(t.Context(), rel, exe); err == nil || !strings.Contains(err.Error(), "start new version") {
		t.Fatalf("Apply error = %v, want a process start error", err)
	}
	if b, _ := os.ReadFile(exe); string(b) != oldExe {
		t.Fatalf("original executable not restored: %q", b)
	}
}

func TestConcurrentInstallKeepsItsBackup(t *testing.T) {
	newExe := "new executable bytes"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, newExe) }))
	t.Cleanup(server.Close)
	rel := &Release{URL: server.URL, Size: int64(len(newExe)), SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(newExe)))}
	exe := filepath.Join(t.TempDir(), "whynet.exe")
	writeUpdateFixture(t, exe, "working executable")
	started := 0
	if err := apply(t.Context(), rel, exe, func(path string) error {
		started++
		err := apply(t.Context(), rel, path, func(string) error {
			started++
			return nil
		})
		if !errors.Is(err, ErrBusy) {
			t.Errorf("competing install error = %v, want ErrBusy", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe + ".old"); started != 1 || string(b) != "working executable" {
		t.Fatalf("competing install changed backup or launched again: %q, launches %d", b, started)
	}
}

func TestInterruptedReplacementLeavesAnExecutable(t *testing.T) {
	const phaseEnv = "WHYNET_TEST_UPDATE_PHASE"
	if phase := os.Getenv(phaseEnv); phase != "" {
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		unlock, err := lockUpdate(exe + ".update-lock")
		if err != nil {
			t.Fatal(err)
		}
		defer unlock()
		if phase == "after" {
			if err := replaceExe(exe, exe+".fixture", exe+".old"); err != nil {
				t.Fatal(err)
			}
		}
		fmt.Println("ready")
		var wait [1]byte
		os.Stdin.Read(wait[:])
		return
	}

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	candidate := append(append([]byte(nil), original...), 1)
	for _, phase := range []string{"before", "after"} {
		t.Run(phase, func(t *testing.T) {
			exe := filepath.Join(t.TempDir(), "whynet.exe")
			writeUpdateFixture(t, exe, string(original))
			writeUpdateFixture(t, exe+".fixture", string(candidate))
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, exe, "-test.run=^TestInterruptedReplacementLeavesAnExecutable$")
			child.Env = append(os.Environ(), phaseEnv+"="+phase)
			out, err := child.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := child.StdinPipe(); err != nil {
				t.Fatal(err)
			}
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			defer child.Wait()
			defer child.Process.Kill()
			reader := bufio.NewScanner(out)
			if !reader.Scan() || reader.Text() != "ready" {
				t.Fatalf("worker failed before checkpoint: %q, %v", reader.Text(), reader.Err())
			}
			if unlock, err := lockUpdate(exe + ".update-lock"); !errors.Is(err, ErrBusy) {
				if unlock != nil {
					unlock()
				}
				t.Fatalf("competing process should be blocked: %v", err)
			}
			if err := child.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			child.Wait()

			want := original
			if phase == "after" {
				want = candidate
				if backup, _ := os.ReadFile(exe + ".old"); sha256.Sum256(backup) != sha256.Sum256(original) {
					t.Fatal("interrupted update lost the original executable backup")
				}
			}
			if got, _ := os.ReadFile(exe); sha256.Sum256(got) != sha256.Sum256(want) {
				t.Fatal("ordinary path no longer contains the expected executable")
			}
			if output, err := exec.CommandContext(ctx, exe, "-test.run=^$").CombinedOutput(); err != nil {
				t.Fatalf("ordinary path cannot launch: %v: %s", err, output)
			}
		})
	}
}

func writeUpdateFixture(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
}
