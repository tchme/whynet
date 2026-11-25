package health

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
	"unicode/utf8"

	"whynet/internal/etw"
	"whynet/internal/probe"
)

type fixRequest struct {
	Action Action
	Hint   *Action
	Undo   *undoEntry
}

type fixPrepared struct {
	Entry *undoEntry
	Done  bool
	Error string
}

type fixResult struct {
	Outcome         Outcome
	Observed        string // before, after, partial or unknown
	Started         bool
	StartKnown      bool
	Skipped         bool
	Conflict        string
	ServiceInstance string
}

type fixWork func() fixResult

// UndoLegacyUnknown identifies old entries that require manual verification.
const UndoLegacyUnknown = "the old journal does not record the changed value"

func observe(e undoEntry, s fixState, err error) string {
	switch {
	case err != nil:
		return "unknown"
	case s.equal(e.Before):
		return "before"
	case s.equal(e.After):
		return "after"
	case partialDNS(e, s):
		return "partial"
	}
	return "unknown"
}

func undoEligibility(e undoEntry) (state string, err error) {
	if e.pendingLive() {
		return "", errors.New("a process handling this change may still be running")
	}
	if e.Manual != "" {
		return "", errors.New(e.Manual)
	}
	if fixKey(e.Undo) == "hosts" {
		path, err := fixedHostsPath()
		if err != nil {
			return "", err
		}
		b, err := readHostsForFix(path)
		if err != nil {
			return "", err
		}
		_, state, err := hostsUndo(e, b)
		return state, err
	}
	s, err := readFixState(e.Undo)
	if err != nil {
		return "", err
	}
	return settingUndo(e, s)
}

func settingUndo(e undoEntry, s fixState) (string, error) {
	if e.Legacy {
		if stateMatchesAction(s, e.Undo) {
			return "before", nil
		}
		return "", errors.New(UndoLegacyUnknown)
	}
	state := observe(e, s, nil)
	if state == "before" {
		return state, nil
	}
	if e.Pending == "fix" && e.Action.ID == "time_resync" {
		return "", errors.New("cannot establish that whynet started W32Time")
	}
	if state != "after" && state != "partial" {
		return "", errors.New("the current setting differs from both recorded values")
	}
	return state, nil
}

func hostsUndo(e undoEntry, b []byte) ([]byte, string, error) {
	if !e.Legacy && hostsHash(b) == e.Before.Value {
		return b, "before", nil
	}
	out, ok := prepareHostEdit(b, e.Undo.Lines, e.Marker, true)
	if !ok || !e.Legacy && hostsHash(out) != e.Before.Value {
		return nil, "", errors.New("active hosts lines differ from the recorded snapshot")
	}
	if hostsHash(out) == hostsHash(b) {
		return b, "before", nil
	}
	return out, "after", nil
}

func prepareFix(req fixRequest, check func(string, uint32) bool) (p fixPrepared, work fixWork) {
	defer func() {
		if r := recover(); r != nil {
			p, work = fixPrepared{Error: fmt.Sprint("panic: ", r)}, nil
		}
	}()
	a := req.Action
	fail := func(err error) (fixPrepared, fixWork) { return fixPrepared{Error: err.Error()}, nil }
	if err := Validate(a, check); err != nil {
		return fail(err)
	}
	if req.Undo != nil && (!a.Equal(req.Undo.Undo) || !req.Undo.Legacy && fixKey(req.Undo.Action) != fixKey(a)) {
		return fail(errors.New("undo does not match the journal entry"))
	}
	if fixKey(a) == "hosts" {
		for _, line := range a.Lines {
			if !utf8.ValidString(line) {
				return fail(errors.New("a selected hosts line cannot be represented in the journal"))
			}
		}
		path, err := fixedHostsPath()
		if err != nil {
			return fail(err)
		}
		return prepareHostsFix(path, req)
	}
	if !reversible(a.ID) {
		if req.Undo != nil || a.ID == "service_start" && a.Value != "Dnscache" {
			return fail(errors.New("this action has no automatic undo"))
		}
		return p, func() fixResult { return fixResult{Outcome: applyOne(a, check)} }
	}
	current, err := readFixState(a)
	if err != nil {
		return fail(err)
	}
	goal := desiredState(a, current)
	e := undoEntry{Action: a, Undo: inverseAction(a, current), Before: current, After: goal}
	if req.Undo != nil {
		e = *req.Undo
		if e.Legacy {
			if stateMatchesAction(current, a) {
				return fixPrepared{Done: true}, nil
			}
			return fail(errors.New(UndoLegacyUnknown))
		}
		if current.equal(e.Before) {
			return fixPrepared{Done: true}, nil
		}
		if !current.equal(e.After) && !partialDNS(e, current) || e.Pending == "fix" && e.Action.ID == "time_resync" {
			return fail(errors.New("the current setting does not match the recorded change"))
		}
		if !stateMatchesAction(e.Before, a) && a.ID != "pac_set" && a.ID != "proxy_enable" {
			return fail(errors.New("the recorded value does not match the undo action"))
		}
		goal = e.Before
		e.Partial = a.ID == "dns_source"
	} else {
		if a.ID == "service_stop" {
			return fail(errors.New("stopping W32Time requires a recorded start"))
		}
		if req.Hint != nil && a.ID != "time_resync" && !stateMatchesAction(current, *req.Hint) {
			return fail(errors.New("the setting has changed since the check"))
		}
		if a.ID != "time_resync" && !current.equal(goal) {
			if err := Validate(e.Undo, check); err != nil {
				return fail(fmt.Errorf("the original setting cannot be restored: %w", err))
			}
		}
	}
	if current.equal(goal) && a.ID != "time_resync" {
		return fixPrepared{Done: true}, nil
	}
	if !current.equal(goal) {
		e.Owner, err = currentFixOwner()
		if err != nil {
			return fail(err)
		}
		p.Entry = &e
	}
	return p, func() fixResult {
		result := fixResult{Outcome: Outcome{Action: a}}
		actual, err := readFixState(a)
		if err != nil || !actual.equal(current) {
			result.Skipped = true
			result.Outcome.Error = "the setting changed before it could be written"
			result.Observed = observe(e, actual, err)
			return result
		}
		started, err := applyCaptured(a, current, goal)
		actual, readErr := readFixState(a)
		if started && readErr == nil && actual.Value == "running" {
			goal, e.After = actual, actual
			result.ServiceInstance = actual.Target
		}
		result.Started, result.StartKnown, result.Observed = started, true, observe(e, actual, readErr)
		if err == nil && (readErr != nil || !actual.equal(goal)) {
			err = errors.New("the setting could not be verified after writing")
		}
		if err != nil {
			result.Outcome.Error = err.Error()
		} else {
			result.Outcome.OK = true
		}
		return result
	}
}

func prepareHostsFix(path string, req fixRequest) (fixPrepared, fixWork) {
	a := req.Action
	b, err := readHostsForFix(path)
	if err != nil {
		return fixPrepared{Error: err.Error()}, nil
	}
	var out []byte
	var e undoEntry
	if req.Undo != nil {
		e = *req.Undo
		var state string
		out, state, err = hostsUndo(e, b)
		if err != nil {
			return fixPrepared{Error: err.Error()}, nil
		}
		if state == "before" {
			return fixPrepared{Done: true}, nil
		}
		if e.Legacy {
			e.Action = Action{ID: "hosts_comment", Lines: a.Lines}
			e.Before, e.After, e.Legacy = fixState{Value: hostsHash(out)}, fixState{Value: hostsHash(b)}, false
		}
	} else {
		if a.ID != "hosts_comment" {
			return fixPrepared{Error: "hosts undo requires a journal entry"}, nil
		}
		e.Marker = "# [whynet " + time.Now().Format("2006-01-02") + "-" + rand.Text() + "] "
		var ok bool
		if out, ok = prepareHostEdit(b, a.Lines, e.Marker, false); !ok {
			return fixPrepared{Error: "a selected line in hosts has changed since the check"}, nil
		}
		e.Action, e.Undo = a, inverseAction(a, fixState{})
		e.Before, e.After = fixState{Value: hostsHash(b)}, fixState{Value: hostsHash(out)}
	}
	backup, err := backupHosts(path, b)
	if err != nil {
		return fixPrepared{Error: err.Error()}, nil
	}
	if e.Backup == "" {
		e.Backup = backup
	}
	if e.Owner, err = currentFixOwner(); err != nil {
		return fixPrepared{Error: err.Error()}, nil
	}
	return fixPrepared{Entry: &e}, func() fixResult {
		r := fixResult{Outcome: Outcome{Action: a, Backup: backup}}
		err := replaceHosts(path, b, out, backup)
		got, readErr := readHostsForFix(path)
		r.Observed = observe(e, fixState{Value: hostsHash(got)}, readErr)
		if err != nil {
			r.Outcome.Error = err.Error()
			if errors.Is(err, errHostsReplacementConflict) {
				r.Conflict = err.Error()
			}
		} else {
			r.Outcome.OK = true
		}
		return r
	}
}

func executeFixWork(work fixWork, a Action) (r fixResult) {
	defer func() {
		if p := recover(); p != nil {
			r = fixResult{Outcome: Outcome{Action: a, Error: fmt.Sprint("panic: ", p)}}
		}
	}()
	return work()
}

// FixJob revalidates each elevated operation and waits for the window's durable-write acknowledgement.
func FixJob(m probe.Markers) etw.Job {
	return func(rw *bufio.ReadWriter) error {
		return serveFix(rw, func(req fixRequest) (fixPrepared, fixWork) {
			if !req.Action.Admin() {
				return fixPrepared{Error: "a user action cannot run in the elevated helper"}, nil
			}
			return prepareFix(req, adapterCheck(m))
		})
	}
}

func serveFix(rw *bufio.ReadWriter, prepare func(fixRequest) (fixPrepared, fixWork)) error {
	dec, enc := json.NewDecoder(rw), json.NewEncoder(rw)
	send := func(value any) error {
		if err := enc.Encode(value); err != nil {
			return err
		}
		return rw.Flush()
	}
	for {
		var req fixRequest
		if err := dec.Decode(&req); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		p, work := prepare(req)
		if err := send(p); err != nil {
			return err
		}
		if work == nil {
			continue
		}
		var approved bool
		if err := dec.Decode(&approved); err != nil {
			return err
		}
		if !approved {
			return nil
		}
		if err := send(executeFixWork(work, req.Action)); err != nil {
			return err
		}
	}
}
