package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	"whynet/internal/live"
	"whynet/locales"
	"whynet/rules"
)

// LiveFileName is a file name for the report on a stability check.
func LiveFileName(res *live.Result) string {
	target := "chain"
	if res.Server != nil {
		target = res.Server.IP.String()
		if res.Server.Name != "" {
			target = res.Server.Name
		}
	}
	target = strings.NewReplacer(":", "_", "/", "_", `\`, "_").Replace(target)
	return fmt.Sprintf("whynet-stability-%s-%s.md", target, res.Started.Format("2006-01-02-1504"))
}

// LinkName is how the window and the report call a link.
func LinkName(t *locales.Texts, l live.Link) string {
	switch {
	case l.ID == live.Router && l.Kind == "wifi":
		return t.Live["link_router_wifi"]
	case l.ID == live.Router && l.Kind == "cable":
		return t.Live["link_router_cable"]
	case l.ID == live.Beyond && l.Kind == "vpn":
		return t.Live["link_beyond_vpn"]
	}
	return t.Live["link_"+l.ID]
}

// HopNote explains a node of the route in words.
func HopNote(t *locales.Texts, h live.Hop, standIn netip.Addr) string {
	var notes []string
	if standIn.IsValid() && h.Addr == standIn.String() {
		notes = append(notes, t.Live["note_stand_in"])
	}
	if h.Note != "" {
		notes = append(notes, t.Live["note_"+h.Note])
	}
	if h.LossCounts {
		notes = append(notes, t.Live["note_loss"])
	}
	if h.Jump {
		notes = append(notes, t.Live["note_jump"])
	}
	return strings.Join(notes, "; ")
}

// LiveExtras are the verdict's extra lines: the Wi-Fi signal, the rhythm of the spikes, the lag marks.
func LiveExtras(t *locales.Texts, res *live.Result) []string {
	var out []string
	L := t.Live
	if res.WiFi != nil && res.Facts["live.router"] == "unstable" {
		out = append(out, locales.Fill(L["wifi_line"], map[string]string{"signal": strconv.Itoa(res.WiFi.AvgSignal), "band": strings.ReplaceAll(cmp(res.WiFi.Last.Band, "?"), ".", ",")}))
	}
	if s := res.Facts["live.router_periodic_s"]; s != "" && res.Facts["live.router_kind"] == "wifi" {
		out = append(out, locales.Fill(L["periodic"], map[string]string{"s": s}))
	}
	names := map[string]string{}
	for _, l := range res.Links {
		names[l.ID] = LinkName(t, l.Link)
	}
	for _, m := range res.Lags {
		out = append(out, LagText(t, m, names, false))
	}
	return out
}

// LagText describes a "лагнуло" mark: the jump nearest before it, or none within 10 s. short is
// the window's one-liner; names maps link ids to their names. No conclusion is drawn from a mark.
func LagText(t *locales.Texts, m live.LagMark, names map[string]string, short bool) string {
	vars := map[string]string{"time": m.At.Format("15:04:05"), "link": names[m.Link], "worst": fmtMs(m.WorstMs),
		"ago": strings.Replace(strconv.FormatFloat(m.AgoS, 'f', -1, 64), ".", ",", 1)}
	key := map[bool]string{true: "lag_network", false: "lag_calm"}[m.Spike]
	if short {
		key = map[bool]string{true: "lag_short_network", false: "lag_short_calm"}[m.Spike]
	}
	return locales.Fill(t.Live[key], vars)
}

func cmp(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func fmtMs(v float64) string {
	if v < 10 {
		return strconv.FormatFloat(v, 'f', 1, 64)
	}
	return strconv.Itoa(int(v + 0.5))
}

// LiveMarkdown renders the AI report of a stability check; prev (may be nil) is the check it is
// compared with. With hide, the private values in res.Mask are replaced across the whole text.
func LiveMarkdown(res *live.Result, v rules.Verdict, t *locales.Texts, hide bool, prev *live.Result) string {
	L, r := t.Live, t.Report
	d := t.LiveDiagnoses[v.ID]
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }

	w("# %s\n\n", L["report_title"])
	w("> **%s** %s\n\n", r["ai_label"], L["report_prompt"])

	w("## %s\n\n", r["summary"])
	if res.Server != nil {
		via := ""
		if res.Server.Via == "doh" {
			via = " — " + L["doh"]
		}
		w("- %s: %s (`%s`)%s\n", L["target"], strings.TrimSpace(res.Input), res.Server.IP, via)
	} else {
		w("- %s: %s\n", L["target"], L["target_none"])
	}
	w("- %s: %s\n", r["time"], res.Started.Format("2006-01-02 15:04:05 -07:00"))
	w("- %s: %d мин %02d с\n", L["duration"], res.Seconds/60, res.Seconds%60)
	w("- %s: **%s** (%s). %s\n", r["verdict"], d.Title, t.Confidence[v.Confidence], locales.Fill(d.Explain, res.Facts))
	for _, x := range LiveExtras(t, res) {
		w("  %s\n", x)
	}
	w("- %s: %s\n", r["advice"], locales.Fill(d.Advice, res.Facts))
	if osv, _ := res.Local["os"].(string); osv != "" {
		w("- %s: %s\n", r["computer"], osv)
	}
	if pub, _ := res.Local["public"].(map[string]string); pub["isp"] != "" {
		w("- %s: %s (%s, %s)\n", r["isp"], pub["isp"], pub["asn"], pub["country"])
	}
	privacy := r["privacy_shown"]
	if hide {
		privacy = r["privacy_hidden"] + "; " + L["wifi_ssid_hidden"]
	}
	w("- %s: %s\n", r["privacy"], privacy)

	writeLinks(w, t, res)
	if len(res.Trace) > 0 {
		var standIn netip.Addr
		for _, l := range res.Links {
			if l.ID == live.Target {
				standIn = l.StandIn
			}
		}
		w("\n## %s\n\n| %s | %s | %s | %s | %s | %s |\n|---|---|---|---|---|---|\n", L["report_trace"],
			L["col_ttl"], L["col_node"], L["col_network"], L["col_loss"], L["col_trace_ms"], L["col_note"])
		for _, h := range res.Trace {
			network := strings.TrimSpace(h.Network + " " + paren(h.ASN))
			if h.TTL == 1 && h.Addr != "*" && network == "" {
				network = L["home_network"]
			}
			ms := "—"
			if h.Addr != "*" {
				ms = fmtMs(h.Median) + " / " + fmtMs(h.Worst)
			}
			w("| %d | %s | %s | %d / %d | %s | %s |\n", h.TTL, h.Addr, cell(network), h.Lost, h.Sent, ms, cell(HopNote(t, h, standIn)))
		}
	}
	if res.WiFi != nil {
		x := res.WiFi.Last
		w("\n## %s\n\n- %s: %d %% (min %d %%, %d readings), RSSI %d dBm\n- %s: %s GHz, %s %d\n- %s: %s, %d / %d Mbit/s\n- SSID: %s\n",
			L["report_wifi"], L["wifi_signal"], res.WiFi.AvgSignal, res.WiFi.MinSignal, res.WiFi.Readings, x.RSSI,
			L["wifi_band"], cmp(x.Band, "?"), L["wifi_channel"], x.Channel, L["wifi_phy"], cmp(x.PHY, "?"), x.RxMbps, x.TxMbps, x.SSID)
	}
	if len(res.Lags) > 0 {
		w("\n## %s\n\n", L["report_lags"])
		for _, m := range res.Lags {
			w("- %s: network jumped within 10 s before the mark: %v; seconds before: %v; link: %s; worst: %s ms\n",
				m.At.Format("15:04:05"), m.Spike, m.AgoS, cmp(m.Link, "-"), fmtMs(m.WorstMs))
		}
	}
	if len(res.Spikes) > 0 {
		w("\n## %s\n\n| time | link | seconds | worst ms | lost |\n|---|---|---|---|---|\n", L["report_spikes"])
		for _, e := range res.Spikes {
			w("| %s | %s | %.1f | %s | %d |\n", e.At.Format("15:04:05"), e.Link, e.Seconds, fmtMs(e.WorstMs), e.Lost)
		}
		if more := res.Total - len(res.Spikes); more > 0 {
			w("\n%s\n", locales.Fill(L["report_spikes_more"], map[string]string{"n": strconv.Itoa(more)}))
		}
	}
	if len(res.Buckets) > 0 {
		w("\n## %s\n\nEach cell: best / median / worst ms · lost.\n\n| time |", L["report_buckets"])
		for _, l := range res.Links {
			w(" %s |", l.ID)
		}
		w("\n|---|%s\n", strings.Repeat("---|", len(res.Links)))
		for _, bk := range res.Buckets {
			w("| %s |", bk.At.Format("15:04:05"))
			for _, l := range res.Links {
				if s, ok := bk.Links[l.ID]; ok {
					w(" %s / %s / %s · %d |", fmtMs(s.Min), fmtMs(s.Median), fmtMs(s.Max), s.Lost)
				} else {
					w(" — |")
				}
			}
			w("\n")
		}
	}
	if prev != nil {
		w("\n## %s\n\n- %s: %s, %d s\n", L["report_compare"], r["time"], prev.Started.Format("15:04:05"), prev.Seconds)
		writeLinks(w, t, prev)
	}
	if len(res.Errors) > 0 {
		w("\n## %s\n\n", L["report_errors"])
		for k, e := range res.Errors {
			w("- %s: %s\n", k, cell(e))
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

func writeLinks(w func(string, ...any), t *locales.Texts, res *live.Result) {
	L := t.Live
	w("\n## %s\n\n| %s | %s | state | %s | %s | %s | %s | %s |\n|---|---|---|---|---|---|---|---|\n", L["report_links"],
		L["col_link"], L["col_addr"], L["col_usual"], L["col_p95"], L["col_worst"], L["col_loss"], L["col_own"])
	for _, l := range res.Links {
		addr := l.Addr.String()
		if l.StandIn.IsValid() {
			addr += " → " + l.StandIn.String()
		}
		w("| %s | %s | %s | %s ms | %s ms | %s ms | %.1f %% | %d (%.1f %%) |\n", LinkName(t, l.Link), addr, l.State,
			fmtMs(l.Median), fmtMs(l.P95), fmtMs(l.Worst), l.LossPct, l.Own, l.OwnPct)
	}
}

func paren(s string) string {
	if s == "" {
		return ""
	}
	return "(" + s + ")"
}
