package health

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"whynet/internal/etw"
	"whynet/internal/probe"
)

// UndoIssue is a journal error or an entry that needs manual verification.
type UndoIssue struct {
	Action Action `json:"action,omitempty"`
	Reason string `json:"reason"`
	Backup string `json:"backup,omitempty"`
}

type fixSession struct {
	prepare func(fixRequest) (fixPrepared, error)
	commit  func() (fixResult, error)
	close   func()
}

func localFixSession(m probe.Markers) fixSession {
	var work fixWork
	var a Action
	return fixSession{prepare: func(req fixRequest) (p fixPrepared, err error) {
		a = req.Action
		p, work = prepareFix(req, adapterCheck(m))
		return p, nil
	}, commit: func() (fixResult, error) { return executeFixWork(work, a), nil }, close: func() {}}
}

func elevatedFixSession() (fixSession, error) {
	const limit = 5 * time.Minute
	conn, rd, err := etw.Elevate("fix", limit)
	if err != nil {
		return fixSession{}, err
	}
	if err := conn.SetDeadline(time.Now().Add(limit)); err != nil {
		conn.Close()
		return fixSession{}, err
	}
	enc, dec := json.NewEncoder(conn), json.NewDecoder(rd)
	return fixSession{close: func() { conn.Close() }, prepare: func(req fixRequest) (p fixPrepared, err error) {
		if err = enc.Encode(req); err == nil {
			err = dec.Decode(&p)
		}
		return
	}, commit: func() (r fixResult, err error) {
		if err = enc.Encode(true); err == nil {
			err = dec.Decode(&r)
		}
		return
	}}, nil
}

// InspectUndo reports the batch that an undo would consider, including any journal problems.
func InspectUndo() (actions []Action, issues []UndoIssue, available bool) {
	path, err := undoPath()
	if err != nil {
		return nil, []UndoIssue{{Reason: err.Error()}}, false
	}
	entries, err := readUndo(path)
	if err != nil {
		return nil, []UndoIssue{{Reason: err.Error()}}, false
	}
	seen := map[string]bool{}
	live := false
	for _, e := range entries {
		actions = append(actions, e.Undo)
		live = live || e.pendingLive()
		key := fixKey(e.Undo)
		if seen[key] {
			continue
		}
		seen[key] = true
		if _, err := undoEligibility(e); err != nil {
			issues = append(issues, UndoIssue{Action: e.Undo, Reason: err.Error(), Backup: e.Backup})
		} else {
			available = true
		}
	}
	return actions, issues, available && !live
}

func (e undoEntry) pendingLive() bool { return e.Pending != "" && e.Owner.PID != 0 && e.Owner.live() }

// Execute performs a batch under the user journal's lock. The process that owns the journal
// authorizes each prepared operation after writing its record. It never delegates journal access.
func Execute(plan Plan, undo bool, m probe.Markers, progress func(int, Outcome)) ([]Outcome, bool, []UndoIssue) {
	path, err := undoPath()
	if err != nil {
		return nil, false, []UndoIssue{{Reason: err.Error()}}
	}
	return executeBatch(path, plan, undo, localFixSession(m), elevatedFixSession, etw.Elevated(), undoEligibility, progress)
}

func executeBatch(path string, plan Plan, undo bool, local fixSession, elevate func() (fixSession, error), elevated bool,
	eligible func(undoEntry) (string, error), progress func(int, Outcome)) (outs []Outcome, declined bool, issues []UndoIssue) {
	unlock, storageErr := lockUndo(path)
	var entries []undoEntry
	if storageErr == nil {
		defer unlock()
		entries, storageErr = readUndo(path)
	}
	if storageErr != nil {
		issues = append(issues, UndoIssue{Reason: storageErr.Error()})
		if undo {
			return
		}
	}
	actions := plan.Actions
	if undo {
		actions = make([]Action, 0, len(entries))
		for _, e := range entries {
			if e.pendingLive() {
				issues = append(issues, UndoIssue{Action: e.Undo, Reason: "a process handling this change may still be running", Backup: e.Backup})
				return
			}
			actions = append(actions, e.Undo)
		}
	}
	var remote fixSession
	var elevateErr, broken error
	defer func() {
		if remote.close != nil {
			remote.close()
		}
	}()
	blocked := map[string]bool{}
	removed := 0
	for i, a := range actions {
		var req fixRequest
		req.Action = a
		position := 0
		if undo {
			position = i - removed
			req.Undo = &entries[position]
		} else if i < len(plan.Undo) {
			req.Hint = plan.Undo[i]
		}
		out := func() Outcome {
			fail := func(err error) Outcome { return Outcome{Action: a, Error: err.Error()} }
			key := fixKey(a)
			if broken != nil {
				return fail(broken)
			}
			if storageErr != nil && reversible(a.ID) {
				return fail(storageErr)
			}
			save := func() bool {
				if err := writeUndo(path, entries); err != nil {
					storageErr = err
					issues = append(issues, UndoIssue{Action: a, Reason: err.Error()})
					return false
				}
				return true
			}
			drop := func() bool {
				entries = slices.Delete(entries, position, position+1)
				if undo {
					removed++
				}
				return save()
			}
			if undo {
				if blocked[key] {
					return fail(errors.New("a newer change to this setting needs verification first"))
				}
				state, err := eligible(*req.Undo)
				if err != nil {
					blocked[key] = true
					issues = append(issues, UndoIssue{Action: a, Reason: err.Error(), Backup: req.Undo.Backup})
					return fail(err)
				}
				if state == "before" {
					if !drop() {
						return fail(storageErr)
					}
					return Outcome{Action: a, OK: true, Already: true}
				}
			} else if reversible(a.ID) && slices.ContainsFunc(entries, func(e undoEntry) bool {
				return e.Pending != "" && fixKey(e.Undo) == key
			}) {
				return fail(errors.New("an earlier change to this setting needs verification first"))
			}
			s := local
			if a.Admin() && !elevated {
				if remote.close == nil && elevateErr == nil {
					remote, elevateErr = elevate()
					declined = errors.Is(elevateErr, ErrDeclined)
				}
				if elevateErr != nil {
					return fail(elevateErr)
				}
				s = remote
			}
			p, err := s.prepare(req)
			if err != nil {
				broken = fmt.Errorf("helper stopped before the next action: %w", err)
				return fail(broken)
			}
			if p.Error != "" {
				return fail(errors.New(p.Error))
			}
			if p.Done {
				if !undo {
					return fail(errors.New("the setting already has the requested value"))
				}
				if !drop() {
					return fail(storageErr)
				}
				return Outcome{Action: a, OK: true, Already: true}
			}
			if p.Entry != nil {
				e := *p.Entry
				if undo {
					e.Pending = "undo"
					entries[position] = e
				} else {
					e.Pending = "fix"
					entries = slices.Insert(entries, 0, e)
				}
				if !save() {
					broken = storageErr // the helper is waiting for acknowledgement and must stop
					return fail(storageErr)
				}
			}
			r, err := s.commit()
			if err != nil || !r.Outcome.Action.Equal(a) {
				if err == nil {
					err = errors.New("the helper returned a different action")
				}
				broken = fmt.Errorf("action outcome unknown: %w", err)
				issues = append(issues, UndoIssue{Action: a, Reason: broken.Error()})
				return fail(broken)
			}
			if p.Entry != nil {
				e := &entries[position]
				e.Owner = fixOwner{}
				e.Manual = r.Conflict
				if a.ID == "time_resync" && r.ServiceInstance != "" {
					e.After.Target = r.ServiceInstance
				}
				if r.Conflict == "" && (r.Observed == "before" || !undo && (r.Skipped || a.ID == "time_resync" && r.StartKnown && !r.Started)) {
					drop()
				} else {
					if r.Observed == "after" || r.Observed == "partial" {
						e.Pending = ""
					}
					save()
				}
				if storageErr != nil {
					broken = storageErr
				}
			}
			return r.Outcome
		}()
		if undo && !out.OK {
			blocked[fixKey(a)] = true
		}
		outs = append(outs, out)
		if progress != nil {
			progress(i, out)
		}
	}
	return
}
