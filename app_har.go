package main

import (
	"errors"
	"os"
	"slices"
	"strconv"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"whynet/internal/probe"
	"whynet/locales"
	"whynet/rules"
)

// The person's own browser record (HAR): read it, then compare it with whynet's check of the same site.

const maxHAR = 200 << 20 // a record with page contents can be big; the sanitized copy is small

// HARInfo is what the window shows once a record is read, before comparing.
type HARInfo struct {
	Site     string `json:"site"` // "" when the person cancelled the file dialog
	Requests int    `json:"requests"`
	Recheck  bool   `json:"recheck"` // the last check was of another site, or there was none: it runs first
}

// ReadHAR reads a record pasted into the window.
func (a *App) ReadHAR(text string) (HARInfo, error) { return a.readHAR([]byte(text)) }

// PasteHAR reads a record from the clipboard.
func (a *App) PasteHAR() (HARInfo, error) {
	text, err := runtime.ClipboardGetText(a.ctx)
	if err != nil {
		return HARInfo{}, err
	}
	return a.readHAR([]byte(text))
}

// ReadHARFile reads a record saved as a file, dropped on the window or picked.
func (a *App) ReadHARFile(path string) (HARInfo, error) {
	if st, err := os.Stat(path); err == nil && st.Size() > maxHAR {
		return HARInfo{}, errors.New(a.t.UI["own_big"])
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return HARInfo{}, err
	}
	return a.readHAR(b)
}

// PickHAR asks for a record file.
func (a *App) PickHAR() (HARInfo, error) {
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{Title: a.t.UI["own_pick"],
		Filters: []runtime.FileFilter{{DisplayName: "HAR (*.har, *.json, *.txt)", Pattern: "*.har;*.json;*.txt"}}})
	if err != nil || path == "" {
		return HARInfo{}, err
	}
	return a.ReadHARFile(path)
}

func (a *App) readHAR(b []byte) (HARInfo, error) {
	rec, err := probe.ParseHAR(b, a.markers)
	switch {
	case errors.Is(err, probe.ErrOneRequest):
		return HARInfo{}, errors.New(a.t.UI["own_one"])
	case errors.Is(err, probe.ErrEmptyHAR):
		return HARInfo{}, errors.New(a.t.UI["own_empty"])
	case err != nil:
		return HARInfo{}, errors.New(a.t.UI["own_not"])
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ownRec = rec
	return HARInfo{Site: rec.Site, Requests: rec.Requests, Recheck: a.last == nil || !probe.SameSite(a.last.Host, rec.Site)}, nil
}

// CompareHAR adds the record read last to the last check as its "own" step, checking the record's
// site first when the last check was of another one, and gives the new verdict.
func (a *App) CompareHAR() (*CheckView, error) {
	a.mu.Lock()
	rec, last := a.ownRec, a.last
	a.mu.Unlock()
	if rec == nil {
		return nil, errors.New(a.t.UI["own_not"])
	}
	if last == nil || !probe.SameSite(last.Host, rec.Site) {
		if _, err := a.check(rec.PageURL, probe.DefaultEnv(a.markers), 2*time.Minute); err != nil {
			return nil, err
		}
	}
	a.mu.Lock()
	res := a.last
	res.Steps = slices.DeleteFunc(res.Steps, func(s probe.Step) bool { return s.ID == "own" }) // a new record replaces the old
	res.Steps = append(res.Steps, probe.OwnStep(rec, res))
	a.verdict, a.lastKind = rules.Match(a.rules, res.Facts()), "site" // the report is of this check again
	v := a.verdict
	a.mu.Unlock()
	return a.view(res, v), nil
}

// OwnView is the record's table in "Details".
type OwnView struct {
	Requests int      `json:"requests"`
	Blocked  int      `json:"blocked"` // stopped by the browser itself, ads included
	Failed   int      `json:"failed"`  // network errors, no answer, server errors (ads excluded)
	Late     int      `json:"late"`
	ShownMs  int64    `json:"shown_ms"`
	Rows     []OwnRow `json:"rows"`
	MoreAds  int      `json:"more_ads"` // ad rows left out of Rows
}

// OwnRow is one file of the record against whynet's view of it.
type OwnRow struct {
	File     string `json:"file"`
	Kind     string `json:"kind"`
	Ms       int64  `json:"ms"`
	What     string `json:"what"`   // what happened in the person's browser
	Whynet   string `json:"whynet"` // what whynet's browser saw
	WhynetOK bool   `json:"whynet_ok"`
	Site     bool   `json:"site"` // a page's own server stopped by the browser: the likely culprit
	Ad       bool   `json:"ad"`
}

const maxOwnRows, maxAdRows = 15, 3

func (a *App) ownView(res *probe.Result) *OwnView {
	var rec *probe.OwnRecord
	for _, s := range res.Steps {
		if r, ok := s.Evidence["record"].(*probe.OwnRecord); ok {
			rec = r
		}
	}
	if rec == nil {
		return nil
	}
	ui := a.t.UI
	v := &OwnView{Requests: rec.Requests, ShownMs: rec.ShownMs}
	ads := 0
	for _, f := range rec.Files {
		switch {
		case f.What == "blocked":
			v.Blocked++
		case f.Ad:
		case f.What == "late":
			v.Late++
		default:
			v.Failed++
		}
		if f.Ad {
			if ads++; ads > maxAdRows {
				v.MoreAds++
				continue
			}
		} else if len(v.Rows)-min(ads, maxAdRows) >= maxOwnRows {
			continue
		}
		row := OwnRow{File: f.URL, Kind: a.t.Kinds[f.Kind], Ms: f.Ms, Ad: f.Ad, Site: f.What == "blocked" && !f.Ad}
		switch f.What {
		case "blocked":
			row.What = ui["own_blocked"]
		case "none":
			row.What = ui["own_none"]
		case "late":
			row.What = a.t.Phases[f.Phase]
		default:
			row.What = f.Error
		}
		switch w := f.Whynet; {
		case w == nil:
			row.Whynet = ui["own_wh_none"]
		case w.File && w.OK:
			row.Whynet, row.WhynetOK = locales.Fill(ui["own_wh_ok"], map[string]string{"s": strconv.FormatFloat(float64(w.Ms)/1000, 'f', 1, 64)}), true
		case w.File:
			row.Whynet = ui["own_wh_fail"]
		case w.OK:
			row.Whynet, row.WhynetOK = locales.Fill(ui["own_wh_server"], map[string]string{"ok": strconv.Itoa(w.Loaded), "n": strconv.Itoa(w.Requests)}), true
		default:
			row.Whynet = ui["own_wh_server_fail"]
		}
		v.Rows = append(v.Rows, row)
	}
	return v
}
