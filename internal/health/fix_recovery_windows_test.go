package health

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func testEntry(a Action, alternate string) undoEntry {
	undo := a
	undo.Value = alternate
	return undoEntry{Action: a, Undo: undo, Before: fixState{Value: alternate}, After: fixState{Value: a.Value}}
}

func TestJournalPrecedesMutationAndPreservesPendingOnLostReply(t *testing.T) {
	path := filepath.Join(t.TempDir(), "undo.json")
	a := Action{ID: "proxy_enable", Value: "0"}
	second := Action{ID: "flush_dns"}
	entry := testEntry(a, "1")
	owner, err := currentFixOwner()
	if err != nil {
		t.Fatal(err)
	}
	entry.Owner = owner
	seen := 0
	s := fixSession{prepare: func(r fixRequest) (fixPrepared, error) {
		seen++
		return fixPrepared{Entry: &entry}, nil
	}, commit: func() (fixResult, error) {
		entries, err := readUndo(path)
		if err != nil || len(entries) != 1 || entries[0].Pending != "fix" || entries[0].Owner != owner || !entries[0].Undo.Equal(entry.Undo) {
			t.Fatalf("mutation was acknowledged without a durable record: %+v, %v", entries, err)
		}
		return fixResult{}, io.ErrUnexpectedEOF
	}}
	outs, _, issues := executeBatch(path, Plan{Actions: []Action{a, second}}, false, s, nil, true, undoEligibility, nil)
	entries, err := readUndo(path)
	if err != nil || len(entries) != 1 || entries[0].Pending != "fix" || !entries[0].Owner.live() || seen != 1 || len(issues) != 1 || len(outs) != 2 || outs[0].OK || outs[1].OK {
		t.Fatalf("lost reply: entries=%+v, err=%v, seen=%d, issues=%+v, outcomes=%+v", entries, err, seen, issues, outs)
	}
	outs, _, issues = executeBatch(path, Plan{}, true, s, nil, true, undoEligibility, nil)
	if len(issues) == 0 || seen != 1 || len(outs) > 0 && outs[0].OK {
		t.Fatalf("undo ran while the original writer was alive: %+v, %+v", outs, issues)
	}
}

func TestUndoJournalDoesNotWriteOverCorruptionOrAnotherWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "undo.json")
	a := Action{ID: "proxy_enable", Value: "0"}
	entry := testEntry(a, "1")
	const damaged = `{"saved":`
	if err := os.WriteFile(path, []byte(damaged), 0o600); err != nil {
		t.Fatal(err)
	}
	s := fixSession{prepare: func(fixRequest) (fixPrepared, error) {
		return fixPrepared{Entry: &entry}, nil
	}, commit: func() (fixResult, error) {
		t.Fatal("operation reached a commit without durable preparation")
		return fixResult{}, nil
	}}
	run := func() []UndoIssue {
		_, _, issues := executeBatch(path, Plan{Actions: []Action{a}}, false, s, nil, true, nil, nil)
		if len(issues) == 0 {
			t.Fatal("journal failure was not reported")
		}
		return issues
	}
	run()
	if got, _ := os.ReadFile(path); string(got) != damaged {
		t.Errorf("corrupted bytes changed: %q", got)
	}
	if err := writeUndo(path, []undoEntry{entry}); err != nil {
		t.Fatal(err)
	}
	old, _ := os.ReadFile(path)
	unlock, err := lockUndo(path)
	if err != nil {
		t.Fatal(err)
	}
	run()
	unlock()
	h, err := windows.CreateFile(windows.StringToUTF16Ptr(path), windows.GENERIC_READ, windows.FILE_SHARE_READ,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	run()
	windows.CloseHandle(h)
	if got, _ := os.ReadFile(path); !bytes.Equal(old, got) {
		t.Error("a second writer or failed replacement changed the journal")
	}
}

func TestUndoJournalBatchesAndPartialRetry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "undo.json")
	proxy := Action{ID: "proxy_enable", Value: "0"}
	dns := Action{ID: "dns_source", Index: 2, Adapter: "test adapter", Value: "dhcp"}
	oldDNS := []string{"192.0.2.8", "192.0.2.9"}
	e1, e2 := testEntry(proxy, "1"), testEntry(dns, "static")
	e2.Undo.Values, e2.Before.Values = oldDNS, oldDNS
	e2.Before.Target, e2.After.Target = "adapter-stable-id", "adapter-stable-id"
	states := map[string]fixState{fixKey(proxy): e1.Before, fixKey(dns): e2.Before}
	known := map[string]undoEntry{fixKey(proxy): e1, fixKey(dns): e2}
	var req fixRequest
	var presented []string
	stopAfterOneDNS := true
	s := fixSession{prepare: func(r fixRequest) (fixPrepared, error) {
		req = r
		e := known[fixKey(r.Action)]
		if r.Undo != nil {
			e = *r.Undo
			e.Partial = r.Action.ID == "dns_source"
		}
		return fixPrepared{Entry: &e}, nil
	}, commit: func() (fixResult, error) {
		key := fixKey(req.Action)
		e := known[key]
		r := fixResult{Outcome: Outcome{Action: req.Action, OK: true}}
		if req.Undo == nil {
			states[key], r.Observed = e.After, "after"
			return r, nil
		}
		presented = append(presented, key)
		if req.Action.ID == "dns_source" && stopAfterOneDNS {
			stopAfterOneDNS = false
			partial := e.Before
			partial.Values = partial.Values[:1]
			states[key] = partial
			r.Outcome.OK, r.Outcome.Error, r.Observed = false, "second server rejected", "partial"
			return r, nil
		}
		states[key], r.Observed = e.Before, "before"
		return r, nil
	}}
	eligible := func(e undoEntry) (string, error) { return observe(e, states[fixKey(e.Undo)], nil), nil }
	for _, a := range []Action{proxy, dns} {
		outs, _, issues := executeBatch(path, Plan{Actions: []Action{a}}, false, s, nil, true, eligible, nil)
		if len(issues) != 0 || len(outs) != 1 || !outs[0].OK {
			t.Fatalf("fix: %+v, %+v", outs, issues)
		}
	}
	outs, _, _ := executeBatch(path, Plan{}, true, s, nil, true, eligible, nil)
	entries, err := readUndo(path)
	if err != nil || len(outs) != 2 || outs[0].OK || !outs[1].OK || !slices.Equal(presented, []string{fixKey(dns), fixKey(proxy)}) ||
		len(entries) != 1 || !partialDNS(entries[0], states[fixKey(dns)]) || !slices.Equal(entries[0].Undo.Values, oldDNS) {
		t.Fatalf("partial undo: %+v, saved %+v, calls %v, err %v", outs, entries, presented, err)
	}
	outs, _, _ = executeBatch(path, Plan{}, true, s, nil, true, eligible, nil)
	if len(outs) != 1 || !outs[0].OK || !slices.Equal(states[fixKey(dns)].Values, oldDNS) {
		t.Fatalf("partial undo retry: %+v, values %v", outs, states[fixKey(dns)].Values)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("final journal did not disappear: %v", err)
	}
}

func TestLegacyUndoKeepsUnknownChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "undo.json")
	// The journal written by 0.7.0 stored inverse commands only.
	legacy := []byte(`{"saved":"2026-11-17T17:41:00Z","actions":[{"id":"winhttp_set","value":"127.0.0.1:8080","values":["<local>;example.com"]}]}`)
	if err := os.WriteFile(path, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := readUndo(path)
	if err != nil || len(entries) != 1 || !entries[0].Legacy {
		t.Fatalf("legacy read: %+v, %v", entries, err)
	}
	entry := entries[0]
	state := fixState{Value: entry.Undo.Value, Values: []string{"<local>;changed.example"}}
	eligible := func(e undoEntry) (string, error) { return settingUndo(e, state) }
	outs, _, issues := executeBatch(path, Plan{}, true, fixSession{}, nil, true, eligible, nil)
	if len(outs) != 1 || outs[0].OK || len(issues) != 1 {
		t.Fatalf("legacy overwrote a newer setting: %+v, %+v", outs, issues)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(legacy, got) {
		t.Fatal("unknown legacy bytes were rewritten")
	}
	state.Values = entry.Undo.Values
	outs, _, _ = executeBatch(path, Plan{}, true, fixSession{}, nil, true, eligible, nil)
	if len(outs) != 1 || !outs[0].OK || !outs[0].Already {
		t.Fatalf("exact already-restored value was not recognized: %+v", outs)
	}
}

func TestDeclinedAdminKeepsCompletedUserChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "undo.json")
	proxy := Action{ID: "proxy_enable", Value: "0"}
	admin := Action{ID: "tcp_global", Value: "rss=enabled"}
	entry := testEntry(proxy, "1")
	s := fixSession{prepare: func(req fixRequest) (fixPrepared, error) {
		if !req.Action.Equal(proxy) {
			t.Fatalf("elevated action passed to the user session: %+v", req.Action)
		}
		return fixPrepared{Entry: &entry}, nil
	}, commit: func() (fixResult, error) {
		return fixResult{Observed: "after", Outcome: Outcome{Action: proxy, OK: true}}, nil
	}}
	prompts := 0
	elevate := func() (fixSession, error) { prompts++; return fixSession{}, ErrDeclined }
	var indexes []int
	outs, declined, issues := executeBatch(path, Plan{Actions: []Action{proxy, admin, admin}}, false, s, elevate, false, nil,
		func(i int, _ Outcome) { indexes = append(indexes, i) })
	entries, err := readUndo(path)
	if err != nil || len(entries) != 1 || !entries[0].Undo.Equal(entry.Undo) || entries[0].Pending != "" || len(issues) != 0 || !declined || prompts != 1 ||
		len(outs) != 3 || !outs[0].OK || outs[1].OK || outs[2].OK || !slices.Equal(indexes, []int{0, 1, 2}) {
		t.Fatalf("UAC refusal: outs=%+v, declined=%v, issues=%+v, journal=%+v (%v), prompts=%d, indexes=%v", outs, declined, issues, entries, err, prompts, indexes)
	}
}

func TestTimeServiceUndoRetainsConfirmedStartAfterResyncFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "undo.json")
	a := Action{ID: "time_resync"}
	e := undoEntry{Action: a, Undo: Action{ID: "service_stop", Value: "W32Time"}, Before: fixState{Value: "stopped"}, After: fixState{Value: "running"}}
	s := fixSession{prepare: func(fixRequest) (fixPrepared, error) {
		return fixPrepared{Entry: &e}, nil
	}, commit: func() (fixResult, error) {
		return fixResult{Observed: "after", StartKnown: true, Started: true, ServiceInstance: "123:456", Outcome: Outcome{Action: a, Error: "resync failed"}}, nil
	}}
	outs, _, _ := executeBatch(path, Plan{Actions: []Action{a}}, false, s, nil, true, nil, nil)
	entries, err := readUndo(path)
	if err != nil || len(outs) != 1 || outs[0].OK || len(entries) != 1 || entries[0].After.Target != "123:456" {
		t.Fatalf("W32Time effect was lost: %+v, %+v, %v", outs, entries, err)
	}
	if _, err := settingUndo(entries[0], fixState{Value: "running", Target: "123:789"}); err == nil {
		t.Error("a different W32Time process could be stopped")
	}
}

func TestFixHelperNeverAppliesBeforeAcknowledgement(t *testing.T) {
	for _, action := range []string{"disconnect", "approve"} {
		t.Run(action, func(t *testing.T) {
			client, helper := net.Pipe()
			defer client.Close()
			defer helper.Close()
			client.SetDeadline(time.Now().Add(time.Second))
			a := Action{ID: "flush_arp"}
			committed := make(chan struct{}, 1)
			exit := make(chan error, 1)
			go func() {
				exit <- serveFix(bufio.NewReadWriter(bufio.NewReader(helper), bufio.NewWriter(helper)), func(fixRequest) (fixPrepared, fixWork) {
					return fixPrepared{}, func() fixResult {
						committed <- struct{}{}
						return fixResult{Outcome: Outcome{Action: a, OK: true}}
					}
				})
			}()
			enc, dec := json.NewEncoder(client), json.NewDecoder(client)
			var prep fixPrepared
			if err := enc.Encode(fixRequest{Action: a}); err != nil {
				t.Fatal(err)
			}
			if err := dec.Decode(&prep); err != nil {
				t.Fatal(err)
			}
			if action == "approve" {
				var r fixResult
				if err := enc.Encode(true); err != nil {
					t.Fatal(err)
				}
				if err := dec.Decode(&r); err != nil || !r.Outcome.OK {
					t.Fatalf("approved action: %+v, %v", r, err)
				}
			}
			client.Close()
			<-exit
			if (len(committed) == 1) != (action == "approve") {
				t.Fatal("unexpected commit count")
			}
		})
	}
}

func TestHostsReplacementBackupAndPartialUndo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts")
	first, second := "192.0.2.1 a.example", "192.0.2.2 b.example"
	original := []byte("\ufeff" + first + "\r\n" + second + "\n# unchanged\xe0")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	act := Action{ID: "hosts_comment", Lines: []string{first, second}}
	p, work := prepareHostsFix(path, fixRequest{Action: act})
	if p.Error != "" || p.Entry == nil || work == nil {
		t.Fatalf("preparation failed: %+v", p)
	}
	if got, _ := os.ReadFile(p.Entry.Backup); !bytes.Equal(got, original) || hostsHash(got) != p.Entry.Before.Value {
		t.Fatal("backup is not a byte-identical copy")
	}
	h, err := windows.CreateFile(windows.StringToUTF16Ptr(path), windows.GENERIC_READ, windows.FILE_SHARE_READ,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := work(); got.Outcome.OK {
		t.Fatal("a denied replacement was reported as successful")
	}
	windows.CloseHandle(h)
	if unchanged, _ := os.ReadFile(path); !bytes.Equal(unchanged, original) {
		t.Fatal("failed replacement damaged the active file")
	}
	if got := work(); !got.Outcome.OK || got.Observed != "after" {
		t.Fatalf("replacement failed: %+v", got)
	}
	changed, _ := os.ReadFile(path)
	if !bytes.HasPrefix(changed, []byte("\ufeff"+p.Entry.Marker+first+"\r\n")) || !bytes.HasSuffix(changed, []byte("# unchanged\xe0")) {
		t.Fatalf("replacement lost encoding, line endings or unrelated bytes: %q", changed)
	}
	partial := bytes.Replace(changed, []byte(p.Entry.Marker+first), []byte(first), 1)
	if err := os.WriteFile(path, partial, 0o600); err != nil {
		t.Fatal(err)
	}
	req := fixRequest{Action: p.Entry.Undo, Undo: p.Entry}
	prep, undo := prepareHostsFix(path, req)
	if prep.Error != "" || undo == nil {
		t.Fatalf("partial hosts undo was rejected: %+v", prep)
	}
	conflict := append(bytes.Clone(partial), []byte("\n# added by another editor")...)
	if err := os.WriteFile(path, conflict, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := undo(); got.Outcome.OK || !strings.Contains(got.Outcome.Error, "changed") {
		t.Fatalf("missed conflict before undo: %+v", got)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, conflict) {
		t.Fatal("undo overwrote a newer external edit")
	}
	if err := os.WriteFile(path, partial, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := undo(); !got.Outcome.OK || got.Observed != "before" {
		t.Fatalf("undo did not finish the partially restored file: %+v", got)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, original) {
		t.Fatal("undo did not restore the original bytes")
	}
}
