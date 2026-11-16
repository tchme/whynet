package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"whynet/internal/probe"
	"whynet/locales"
	"whynet/rules"
)

// AppFileName is a file name for the app check's report, safe on Windows.
func AppFileName(res *probe.AppResult) string {
	name := strings.NewReplacer(" ", "_", ":", "_", "/", "_", `\`, "_").Replace(res.Host)
	return fmt.Sprintf("whynet-app-%s-%s.md", name, res.Started.Format("2006-01-02-1504"))
}

// AppMarkdown renders the app check's AI report: the program (its exe name only; the window title
// never comes here), the recording, every server with what the program saw and what whynet found,
// the raw data. With hide, the private values in res.Mask are replaced across the whole text.
func AppMarkdown(res *probe.AppResult, v rules.Verdict, t *locales.Texts, hide bool) string {
	facts := res.Facts()
	d := t.AppDiagnoses[v.ID]
	r, a := t.Report, t.App
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }

	w("# %s\n\n", a["report_title"])
	w("> **%s** %s\n\n", r["ai_label"], a["ai_prompt"])
	w("## %s\n\n", r["summary"])
	w("- %s: %s (`%s`)\n", a["program"], res.Host, res.Exe)
	w("- %s: %s\n", a["recorded"], locales.Fill(a["recorded_line"], map[string]string{
		"start": res.Started.Format("2006-01-02 15:04:05 -07:00"), "seconds": strconv.Itoa(int(res.Seconds + 0.5))}))
	w("- %s: **%s** (%s). %s\n", r["verdict"], d.Title, t.Confidence[v.Confidence], locales.Fill(d.Explain, facts))
	w("- %s: %s\n", r["advice"], locales.Fill(d.Advice, facts))
	writeEnv(w, t, res.Steps, facts, hide)
	writeSteps(w, t, res.Steps)

	if len(res.Servers) > 0 {
		w("\n## %s\n\n%s\n|---|---|---|---|---|---|---|---|\n", a["servers"], a["servers_cols"])
		for _, s := range res.Servers {
			name, from := s.Name, a["name_"+s.NameFrom]
			if name == "" {
				name, from = "—", a["name_none"]
			}
			if s.Ad {
				name += " (" + a["ad"] + ")"
			}
			addr := fmt.Sprintf("%s:%d", s.IP, s.Port)
			if s.FakeIP {
				addr += "; " + a["fakeip"] + " " + orDash(s.RealIP)
			}
			owner := strings.TrimSpace(s.ASN + " " + s.Owner)
			seen := locales.Fill(a["seen"], map[string]string{"conns": strconv.Itoa(s.Conns), "cut": strconv.Itoa(s.Cut),
				"stuck": strconv.Itoa(s.Stuck), "open": strconv.Itoa(s.Open)})
			check := a["not_checked"]
			if s.Checked {
				check = a["result_"+s.Result]
				if s.Detail != "" {
					check += " (" + s.Detail + ")"
				}
			}
			ping := "—"
			if s.Tries > 0 {
				ping = strconv.FormatInt(s.PingMs, 10)
				if s.PingMs == 0 {
					ping = "<1" // answered on this PC's side: a router that proxies everything answers the connect itself
				}
				if s.PingTLS {
					ping += "; " + r["ping_tls"]
				}
			}
			if s.Lost > 0 {
				ping += " (" + locales.Fill(r["lost"], map[string]string{"lost": strconv.Itoa(s.Lost), "tries": strconv.Itoa(s.Tries)}) + ")"
			}
			zapret := "—"
			if s.DPIName != "" || s.DPIIP != "" {
				zapret = locales.Fill(a["zapret"], map[string]string{"name": orDash(s.DPIName), "ip": orDash(s.DPIIP),
					"game": map[bool]string{true: t.Values["true"], false: t.Values["false"]}[s.GamePort]})
			}
			w("| %s | %s | %s | %s | %s | %s | %s | %s |\n", cell(name), from, addr, cell(orDash(owner)), seen, cell(check), cell(ping), zapret)
		}
	}

	var raw bytes.Buffer
	enc := json.NewEncoder(&raw)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(map[string]any{"verdict": v, "check": res}); err != nil {
		raw.WriteString(err.Error())
	}
	w("\n## %s\n\n````json\n%s````\n\n%s %s\n", r["raw"], raw.String(), r["footer"], Version)

	out := b.String()
	if hide {
		for private, shown := range res.Mask {
			out = strings.ReplaceAll(out, private, shown)
		}
	}
	return out
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
