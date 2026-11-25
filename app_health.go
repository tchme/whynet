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
	State string `json:"state,omitempty"` // after a fix: fixed, left, gone, unconfirmed
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

func (h *healthState) latest() (*health.Result, []rules.Rule) {
	if h.after != nil {
		return h.after, h.afterF
	}
	return h.res, h.finds
}

func (a *App) healthReport(h *healthState, hide bool) (string, string) {
	_, issues, _ := health.InspectUndo()
	return report.HealthMarkdown(h.res, h.finds, h.batches, h.after, h.afterF, a.t, hide, issues...), report.HealthFileName(h.res)
}

func (h *healthState) batch(after *health.Result, finds []rules.Rule, outs []health.Outcome, undo, declined bool) (report.HealthBatch, []rules.Rule) {
	bt := report.HealthBatch{Undo: undo, Outcomes: outs, Declined: declined}
	before, previous := h.latest()
	if before != nil {
		bt.BeforeFacts = before.Facts()
		if !undo {
			bt.States = report.HealthFixStates(before, after, previous, finds, outs)
		}
	}
	return bt, previous
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
	h := a.health
	a.mu.Unlock()
	return a.healthView(h, report.HealthBatch{}, nil, ""), nil
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
	res := health.Run(ctx, a.markers, report.HealthProgress(a.healthRules, func(s probe.Step, finds []rules.Rule) {
		runtime.EventsEmit(a.ctx, "health-step", a.healthStepView(s, finds))
	}))
	return res, rules.MatchAll(a.healthRules, res.Facts())
}

func (a *App) healthStepView(s probe.Step, finds []rules.Rule) StepView {
	v := a.stepView(s)
	v.Status = report.AreaStatus(s, finds)
	v.Note = report.HealthAreaIssue(a.t, s)
	return v
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
	return a.runBatch(false)
}

// UndoHealth takes back what the fix batches since the last undo changed.
func (a *App) UndoHealth() (*HealthView, error) {
	return a.runBatch(true)
}

func (a *App) runBatch(undo bool) (*HealthView, error) {
	if !a.begin() {
		return nil, errors.New(a.t.UI["busy"])
	}
	defer a.end()
	a.mu.Lock()
	h := a.health
	a.mu.Unlock()
	if h == nil {
		if !undo {
			return nil, errors.New(a.t.UI["busy"])
		}
		h = &healthState{}
	}
	outs, declined, issues := health.Execute(h.plan, undo, a.markers, func(i int, o health.Outcome) {
		runtime.EventsEmit(a.ctx, "health-fix", map[string]any{"i": i, "ok": o.OK, "error": o.Error})
	})
	res, finds := a.runHealth()
	bt, previous := h.batch(res, finds, outs, undo, declined)
	bt.UndoIssues = issues
	a.mu.Lock()
	if a.health == nil || a.health.res == nil { // an undo before any check this run
		a.health = &healthState{res: res, finds: finds}
	}
	a.health.batches = append(a.health.batches, bt)
	a.health.after, a.health.afterF = res, finds
	a.health.plan = a.healthPlan(res, finds)
	h = a.health
	a.mu.Unlock()
	mode := "fix"
	if undo {
		mode = "undo"
	}
	return a.healthView(h, bt, previous, mode), nil
}

// healthView shows the current check and any changes from the immediately preceding batch.
func (a *App) healthView(h *healthState, bt report.HealthBatch, previous []rules.Rule, mode string) *HealthView {
	H := a.t.Health
	after := mode != ""
	cur, finds := h.latest()
	facts := cur.Facts()
	v := &HealthView{After: after, Declined: bt.Declined}
	actions, issues, available := health.InspectUndo()
	issues = append(issues, bt.UndoIssues...)
	for _, act := range actions {
		v.UndoList = append(v.UndoList, HealthOutcomeView{Text: report.HealthAction(a.t, act, facts, false),
			DoneText: report.HealthAction(a.t, act, facts, true), Admin: act.Admin()})
	}
	v.Undo = available
	incomplete := false
	for _, s := range cur.Steps {
		sv := a.healthStepView(s, finds)
		v.Areas = append(v.Areas, sv)
		incomplete = incomplete || sv.Note != ""
		if sv.Status == "ok" && !slices.ContainsFunc(finds, func(r rules.Rule) bool { return r.Area == s.ID }) {
			v.OKAreas = append(v.OKAreas, sv.Name)
		}
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
	problems, advice, fixed, total := 0, 0, 0, 0
	uncertain := false
	for _, f := range previous {
		state := bt.States[f.ID]
		if state == "" {
			continue
		}
		if f.Fix != "" {
			total++
			if state == "fixed" {
				fixed++
			}
		}
		uncertain = uncertain || state == "unconfirmed" || state == "gone"
		if state == "left" || state == "unconfirmed" && slices.ContainsFunc(finds, func(r rules.Rule) bool { return r.ID == f.ID }) {
			continue // use the current finding and its facts below
		}
		fv := view(f, bt.BeforeFacts)
		fv.State, fv.Tag = state, H["tag_"+state]
		v.Finds = append(v.Finds, fv)
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
		fv := view(f, facts)
		if state := bt.States[f.ID]; state == "unconfirmed" || state == "left" && f.Kind == "problem" {
			fv.State, fv.Tag = state, H["tag_"+state]
		}
		v.Finds = append(v.Finds, fv)
	}

	plural := func(n int, forms string) string { return strconv.Itoa(n) + " " + locales.Plural(n, forms) }
	ok := 0
	for _, o := range bt.Outcomes {
		if o.OK {
			ok++
		}
	}
	failed := ok != len(bt.Outcomes) || (after && len(bt.Outcomes) == 0) || bt.Declined
	switch {
	case mode == "undo" && len(bt.Outcomes) == 0:
		v.Head = H["undo_unconfirmed"]
	case mode == "undo" && failed:
		v.Head = locales.Fill(H["undone_part"], map[string]string{"done": strconv.Itoa(ok), "total": strconv.Itoa(len(bt.Outcomes))})
	case mode == "undo":
		v.Head = H["undone"]
	case mode == "fix" && total > 0:
		v.Head = locales.Fill(H["after_head"], map[string]string{"done": strconv.Itoa(fixed), "total": strconv.Itoa(total)})
	case mode == "fix" && failed:
		v.Head = H["cleanup_unconfirmed"]
	case mode == "fix":
		v.Head = H["cleaned"]
	case problems > 0 && advice > 0:
		v.Head = locales.Fill(H["head_both"], map[string]string{"problems": plural(problems, H["problem_forms"]), "advice": plural(advice, H["advice_forms"])})
	case problems > 0:
		v.Head = locales.Fill(H["head_problems"], map[string]string{"problems": plural(problems, H["problem_forms"])})
	case advice > 0:
		v.Head = locales.Fill(H["head_advice"], map[string]string{"advice": plural(advice, H["advice_forms"])})
	case incomplete:
		v.Head = H["head_incomplete"]
	default:
		v.Head = H["head_ok"]
	}
	switch {
	case problems > 0:
		v.Tone = "bad"
	case advice > 0 || incomplete || failed || uncertain || fixed != total || len(issues) > 0:
		v.Tone = "warn"
	default:
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
	if len(issues) > 0 {
		v.FixNote += "\n" + strings.Join(report.HealthUndoNotes(a.t, facts, issues), "\n")
	}
	before := bt.BeforeFacts
	if before == nil {
		before = facts
	}
	for _, o := range bt.Outcomes {
		st := "ok"
		if !o.OK {
			st = "fail"
		}
		e := o.Error
		if e == health.ErrDeclined.Error() {
			e = "" // the line under the list says it in words
		}
		v.Done = append(v.Done, HealthOutcomeView{Text: report.HealthOutcomeText(a.t, o, before), Status: st, Error: e})
	}
	return v
}
