package main

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"whynet/internal/health"
	"whynet/internal/probe"
	"whynet/internal/report"
	"whynet/locales"
	"whynet/rules"
)

// The Windows network health check in the window (docs/health.md): check, list every finding, fix
// what was found with undo, check again.

// HealthFindingView is one finding as the window shows it.
type HealthFindingView struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"` // problem, advice, info
	Title string `json:"title"`
	Text  string `json:"text"`
	Tag   string `json:"tag"`             // починю / совет / исправлено...
	State string `json:"state,omitempty"` // after a fix: fixed, left
}

// HealthOutcomeView is one action of a fix batch: planned (Status "") or done.
type HealthOutcomeView struct {
	Text     string `json:"text"`
	DoneText string `json:"done_text,omitempty"` // the same, done: shown once it went through
	Status   string `json:"status"`              // "", ok, fail
	Error    string `json:"error,omitempty"`
	Admin    bool   `json:"admin,omitempty"` // runs through the elevated helper
}

// HealthView is a finished health check (or the re-check after a fix) as the window shows it.
type HealthView struct {
	Head     string              `json:"head"`
	Tone     string              `json:"tone"` // bad, warn, ok
	Areas    []StepView          `json:"areas"`
	Finds    []HealthFindingView `json:"finds"` // problems and advice
	Infos    []HealthFindingView `json:"infos"`
	OKAreas  []string            `json:"ok_areas"` // names of the areas with nothing found
	Plan     []HealthOutcomeView `json:"plan"`     // what "Починить" will do
	Admin    bool                `json:"admin"`    // the plan asks for administrator rights
	FixLabel string              `json:"fix_label"`
	FixNote  string              `json:"fix_note"`
	Fixable  bool                `json:"fixable"` // something besides the cache cleanup
	After    bool                `json:"after"`   // the re-check after a fix or an undo
	Done     []HealthOutcomeView `json:"done,omitempty"`
	Declined bool                `json:"declined,omitempty"`
	Undo     bool                `json:"undo"`      // "Вернуть как было" has something to take back
	UndoList []HealthOutcomeView `json:"undo_list"` // what it would run
}

// healthState is the last health check and what was done since.
type healthState struct {
	res     *health.Result
	finds   []rules.Rule
	plan    health.Plan
	batches []report.HealthBatch
	after   *health.Result // the re-check after the last batch
	afterF  []rules.Rule
}

// HealthAreas are the areas for the checking screen, all waiting.
func (a *App) healthAreas() []StepView {
	out := make([]StepView, len(health.Areas))
	for i, id := range health.Areas {
		out[i] = StepView{ID: id, Name: a.t.Steps[id].Name, Status: "wait"}
	}
	return out
}

// CheckHealth checks the PC, sending each area as a "health-step" event as it finishes.
func (a *App) CheckHealth() (*HealthView, error) {
	if !a.begin() {
		return nil, errors.New(a.t.UI["busy"])
	}
	defer a.end()
	res, finds := a.runHealth()
	a.mu.Lock()
	a.health = &healthState{res: res, finds: finds, plan: a.healthPlan(res, finds)}
	a.lastKind = "health"
	h := a.health
	a.mu.Unlock()
	return a.healthView(h, nil, nil, "", false), nil
}

func (a *App) begin() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.running {
		return false
	}
	a.running = true
	return true
}

func (a *App) end() {
	a.mu.Lock()
	a.running = false
	a.mu.Unlock()
}

// runHealth runs the check; each area goes out with the status its findings give it so far.
func (a *App) runHealth() (*health.Result, []rules.Rule) {
	ctx, cancel := context.WithTimeout(a.ctx, time.Minute)
	defer cancel()
	facts := map[string]string{}
	res := health.Run(ctx, a.markers, func(s probe.Step) {
		for k, v := range s.Facts {
			facts[k] = v
		}
		v := a.stepView(s)
		v.Status = areaStatus(s.ID, rules.MatchAll(a.healthRules, facts))
		runtime.EventsEmit(a.ctx, "health-step", v)
	})
	return res, rules.MatchAll(a.healthRules, res.Facts())
}

func areaStatus(id string, finds []rules.Rule) string {
	if st := report.AreaStatus(finds)[id]; st != "" {
		return st
	}
	return "ok"
}

// healthPlan is the batch for the fixes the findings offer.
func (a *App) healthPlan(res *health.Result, finds []rules.Rule) health.Plan {
	var fixes []string
	for _, f := range finds {
		if f.Fix != "" && !slices.Contains(fixes, f.Fix) {
			fixes = append(fixes, f.Fix)
		}
	}
	return health.MakePlan(res, fixes)
}

// FixHealth runs the batch the last check offers, sending "health-fix" (the action's index and
// outcome) after each action, then checks again.
func (a *App) FixHealth() (*HealthView, error) {
	a.mu.Lock()
	h := a.health
	a.mu.Unlock()
	if h == nil {
		return nil, errors.New(a.t.UI["busy"])
	}
	return a.runBatch(h, h.plan.Actions, false)
}

// UndoHealth takes back what the fix batches since the last undo changed.
func (a *App) UndoHealth() (*HealthView, error) {
	a.mu.Lock()
	h := a.health
	a.mu.Unlock()
	if h == nil {
		h = &healthState{}
	}
	return a.runBatch(h, health.LoadUndo(), true)
}

func (a *App) runBatch(h *healthState, actions []health.Action, undo bool) (*HealthView, error) {
	if !a.begin() {
		return nil, errors.New(a.t.UI["busy"])
	}
	defer a.end()
	// The words for what was done come from the check the batch was planned on (the re-check no
	// longer has, say, the proxy's address).
	var before map[string]string
	switch {
	case h.after != nil:
		before = h.after.Facts()
	case h.res != nil:
		before = h.res.Facts()
	}
	outs, declined := health.Execute(actions, a.markers, func(o health.Outcome) {
		i := slices.IndexFunc(actions, func(x health.Action) bool { return actionSame(x, o.Action) })
		runtime.EventsEmit(a.ctx, "health-fix", map[string]any{"i": i, "ok": o.OK, "error": o.Error})
	})
	var err error
	if undo {
		var failed []health.Action
		for _, act := range actions {
			if !slices.ContainsFunc(outs, func(o health.Outcome) bool { return o.OK && actionSame(o.Action, act) }) {
				failed = append(failed, act)
			}
		}
		err = health.KeepUndo(failed)
	} else {
		err = health.SaveUndo(health.UndoPlan(h.plan, outs))
	}
	res, finds := a.runHealth()
	a.mu.Lock()
	if a.health == nil || a.health.res == nil { // an undo before any check this run
		a.health = &healthState{res: res, finds: finds}
	}
	a.health.batches = append(a.health.batches, report.HealthBatch{Undo: undo, Outcomes: outs, Declined: declined})
	a.health.after, a.health.afterF = res, finds
	a.health.plan = a.healthPlan(res, finds)
	a.lastKind = "health"
	h = a.health
	a.mu.Unlock()
	mode := "fix"
	if undo {
		mode = "undo"
	}
	v := a.healthView(h, outs, before, mode, declined)
	if err != nil {
		v.FixNote = locales.Fill(a.t.Health["undo_failed"], map[string]string{"error": err.Error()})
	}
	return v, nil
}

func actionSame(x, y health.Action) bool {
	return x.ID == y.ID && x.Value == y.Value && x.Index == y.Index && x.Adapter == y.Adapter && slices.Equal(x.Lines, y.Lines)
}

// healthView: the findings of the last check (after a fix: the first check's findings marked
// fixed or left, plus anything new), the areas, the next batch. mode: "", fix, undo.
func (a *App) healthView(h *healthState, outs []health.Outcome, before map[string]string, mode string, declined bool) *HealthView {
	H := a.t.Health
	after := mode != ""
	cur, finds := h.res, h.finds
	if after && h.after != nil {
		cur, finds = h.after, h.afterF
	}
	facts := cur.Facts()
	v := &HealthView{After: after, Declined: declined}
	for _, act := range health.LoadUndo() {
		v.UndoList = append(v.UndoList, HealthOutcomeView{Text: report.HealthAction(a.t, act, facts, false),
			DoneText: report.HealthAction(a.t, act, facts, true), Admin: act.Admin()})
	}
	v.Undo = len(v.UndoList) > 0
	for _, s := range cur.Steps {
		sv := a.stepView(s)
		sv.Status = areaStatus(s.ID, finds)
		v.Areas = append(v.Areas, sv)
	}
	view := func(f rules.Rule, facts map[string]string) HealthFindingView {
		d := a.t.HealthFindings[f.ID]
		fv := HealthFindingView{ID: f.ID, Kind: f.Kind, Title: locales.Fill(d.Title, facts),
			Text: strings.TrimSpace(locales.Fill(d.Explain, facts) + " " + locales.Fill(d.Advice, facts))}
		switch {
		case f.Kind == "problem" && f.Fix != "":
			fv.Tag = H["tag_fix"]
			if f.Fix != "proxy_off" && f.Fix != "pac_off" { // the person's own proxy setting needs no rights
				fv.Tag = H["tag_fix_admin"]
			}
		case f.Kind == "problem":
			fv.Tag = H["kind_problem"]
		default:
			fv.Tag = H["tag_advice"]
		}
		return fv
	}
	has := func(list []rules.Rule, id string) bool {
		return slices.ContainsFunc(list, func(r rules.Rule) bool { return r.ID == id })
	}
	problems, advice, fixed, total := 0, 0, 0, 0
	marked := mode == "fix" && h.res != nil
	if marked {
		// What the first check found, each marked: gone now, or still there.
		first := h.res.Facts()
		for _, f := range h.finds {
			if f.Kind == "info" {
				continue
			}
			fv := view(f, first)
			if f.Fix != "" {
				total++
			}
			if has(finds, f.ID) {
				fv.State, fv.Tag = "left", H["tag_left"]
				if f.Kind == "advice" {
					fv.State, fv.Tag = "", H["tag_advice"]
				}
			} else {
				fv.State, fv.Tag = "fixed", H["tag_fixed"]
				if f.Fix != "" {
					fixed++
				}
			}
			v.Finds = append(v.Finds, fv)
		}
	}
	for _, f := range finds {
		if f.Kind == "info" {
			v.Infos = append(v.Infos, view(f, facts))
			continue
		}
		if f.Kind == "problem" {
			problems++
		} else {
			advice++
		}
		if !(marked && has(h.finds, f.ID)) {
			v.Finds = append(v.Finds, view(f, facts))
		}
	}
	found := report.AreaStatus(finds)
	for _, s := range cur.Steps {
		if found[s.ID] == "" && !slices.ContainsFunc(finds, func(r rules.Rule) bool { return r.Area == s.ID }) {
			v.OKAreas = append(v.OKAreas, a.t.Steps[s.ID].Name)
		}
	}

	plural := func(n int, forms string) string { return strconv.Itoa(n) + " " + locales.Plural(n, forms) }
	switch {
	case mode == "undo" && slices.ContainsFunc(outs, func(o health.Outcome) bool { return !o.OK }):
		ok := 0
		for _, o := range outs {
			if o.OK {
				ok++
			}
		}
		v.Head = locales.Fill(H["undone_part"], map[string]string{"done": strconv.Itoa(ok), "total": strconv.Itoa(len(outs))})
	case mode == "undo":
		v.Head = H["undone"]
	case marked && total > 0:
		v.Head = locales.Fill(H["after_head"], map[string]string{"done": strconv.Itoa(fixed), "total": strconv.Itoa(total)})
	case marked:
		v.Head = H["cleaned"]
	case problems > 0 && advice > 0:
		v.Head = locales.Fill(H["head_both"], map[string]string{"problems": plural(problems, H["problem_forms"]), "advice": plural(advice, H["advice_forms"])})
	case problems > 0:
		v.Head = locales.Fill(H["head_problems"], map[string]string{"problems": plural(problems, H["problem_forms"])})
	case advice > 0:
		v.Head = locales.Fill(H["head_advice"], map[string]string{"advice": plural(advice, H["advice_forms"])})
	default:
		v.Head = H["head_ok"]
	}
	switch {
	case problems > 0:
		v.Tone = "bad"
	case advice > 0:
		v.Tone = "warn"
	default:
		v.Tone = "ok"
	}
	if marked && total > 0 && fixed == total && problems == 0 {
		v.Tone = "ok"
	}

	for _, act := range h.plan.Actions {
		v.Plan = append(v.Plan, HealthOutcomeView{Text: report.HealthAction(a.t, act, facts, false),
			DoneText: report.HealthAction(a.t, act, facts, true), Admin: act.Admin()})
		v.Admin = v.Admin || act.Admin()
		v.Fixable = v.Fixable || act.ID != "flush_dns" && act.ID != "flush_arp"
	}
	v.FixLabel, v.FixNote = H["clean_button"], H["clean_note"]
	if v.Fixable {
		n := 0
		for _, f := range finds {
			if f.Fix != "" {
				n++
			}
		}
		v.FixLabel = locales.Fill(H["fix_button"], map[string]string{"n": strconv.Itoa(n)})
		v.FixNote = H["fix_note"]
		if v.Admin {
			v.FixNote = H["fix_note_admin"]
		}
	}
	for _, o := range outs {
		st := "ok"
		if !o.OK {
			st = "fail"
		}
		if before == nil {
			before = facts
		}
		e := o.Error
		if e == health.ErrDeclined.Error() {
			e = "" // the line under the list says it in words
		}
		v.Done = append(v.Done, HealthOutcomeView{Text: report.HealthAction(a.t, o.Action, before, o.OK), Status: st, Error: e})
	}
	return v
}
