package health

import (
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Action is one change of a fix batch (docs/health.md "Fixing"). Every value comes from the check's
// own evidence, never from text the person typed; the elevated helper checks each one again (Validate).
type Action struct {
	ID      string   `json:"id"`
	Adapter string   `json:"adapter,omitempty"` // the adapter's name (dhcp_renew; shown for dns_source)
	Index   uint32   `json:"index,omitempty"`   // its IPv4 interface index (dns_source)
	Value   string   `json:"value,omitempty"`   // the setting to set (see Validate)
	Values  []string `json:"values,omitempty"`  // DNS servers; the WinHTTP bypass list
	Lines   []string `json:"lines,omitempty"`   // hosts lines, exactly as in the file
}

// The actions, in the order a batch runs them: least disruptive first, the caches last.
var actionOrder = []string{"proxy_enable", "pac_set", "winhttp_set", "hosts_comment", "hosts_uncomment", "dns_source",
	"service_start", "service_stop", "tcp_global", "dhcp_renew", "time_resync", "flush_dns", "flush_arp"}

// Admin reports whether the action needs administrator rights (the proxy is the person's own
// setting; the DNS cache flush works without; measured 2026-11-17).
func (a Action) Admin() bool {
	return a.ID != "proxy_enable" && a.ID != "pac_set" && a.ID != "flush_dns"
}

// Outcome is what happened to one action.
type Outcome struct {
	Action Action `json:"action"`
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
}

// Plan is a fix batch and how to take it back.
type Plan struct {
	Actions []Action `json:"actions"`
	// Undo[i] reverses Actions[i]; nil for what has nothing to restore (caches, a new address).
	Undo []*Action `json:"undo"`
}

// MakePlan builds the batch for the fixes the findings name (rule "fix" ids), from the check's
// evidence, plus the cache cleanup: the DNS cache always, ARP when the batch runs elevated anyway.
func MakePlan(res *Result, fixes []string) Plan {
	f := res.Facts()
	var p Plan
	add := func(a Action, undo *Action) { p.Actions, p.Undo = append(p.Actions, a), append(p.Undo, undo) }
	for _, fix := range fixes {
		switch fix {
		case "proxy_off":
			add(Action{ID: "proxy_enable", Value: "0"}, &Action{ID: "proxy_enable", Value: "1"})
		case "pac_off":
			add(Action{ID: "pac_set", Value: ""}, &Action{ID: "pac_set", Value: f["health.proxy_pac_url"]})
		case "winhttp_reset":
			var bypass []string
			if w, ok := res.Evidence("health_proxy", "winhttp").(map[string]any); ok {
				if b, _ := w["bypass"].(string); b != "" {
					bypass = []string{b}
				}
			}
			add(Action{ID: "winhttp_set"}, &Action{ID: "winhttp_set", Value: f["health.winhttp_server"], Values: bypass})
		case "hosts_stale":
			lines, _ := res.Evidence("health_hosts", "stale_lines").([]string)
			if len(lines) > 0 {
				add(Action{ID: "hosts_comment", Lines: lines}, &Action{ID: "hosts_uncomment", Lines: lines})
			}
		case "dns_auto":
			if d, ok := res.Evidence("health_dns", "dns_fix").(map[string]any); ok {
				name, _ := d["adapter"].(string)
				idx, _ := d["index"].(uint32)
				manual, _ := d["manual"].([]string)
				add(Action{ID: "dns_source", Adapter: name, Index: idx, Value: "dhcp"},
					&Action{ID: "dns_source", Adapter: name, Index: idx, Value: "static", Values: manual})
			}
		case "dnscache_start":
			add(Action{ID: "service_start", Value: "Dnscache"}, nil) // stopping DNS again helps nobody
		case "autotuning":
			add(Action{ID: "tcp_global", Value: "autotuninglevel=normal"}, &Action{ID: "tcp_global", Value: "autotuninglevel=" + f["health.autotuning"]})
		case "rss":
			add(Action{ID: "tcp_global", Value: "rss=enabled"}, &Action{ID: "tcp_global", Value: "rss=disabled"})
		case "dhcp_renew":
			add(Action{ID: "dhcp_renew", Adapter: f["health.apipa_adapter"]}, nil)
		case "time_sync":
			var undo *Action
			if f["health.w32time"] == "stopped" {
				undo = &Action{ID: "service_stop", Value: "W32Time"}
			}
			add(Action{ID: "time_resync"}, undo)
		}
	}
	add(Action{ID: "flush_dns"}, nil)
	if slices.ContainsFunc(p.Actions, Action.Admin) {
		add(Action{ID: "flush_arp"}, nil)
	}
	// Run in the fixed order, the undo entries with their actions.
	idx := make([]int, len(p.Actions))
	for i := range idx {
		idx[i] = i
	}
	slices.SortStableFunc(idx, func(a, b int) int {
		return slices.Index(actionOrder, p.Actions[a].ID) - slices.Index(actionOrder, p.Actions[b].ID)
	})
	var out Plan
	for _, i := range idx {
		out.Actions, out.Undo = append(out.Actions, p.Actions[i]), append(out.Undo, p.Undo[i])
	}
	return out
}

// UndoPlan is the batch that takes back what succeeded: the undo entries of the actions that went
// through, in reverse order.
func UndoPlan(p Plan, outs []Outcome) []Action {
	var undo []Action
	for i := len(p.Actions) - 1; i >= 0; i-- {
		if i < len(p.Undo) && p.Undo[i] != nil && slices.ContainsFunc(outs, func(o Outcome) bool { return o.OK && actionEqual(o.Action, p.Actions[i]) }) {
			undo = append(undo, *p.Undo[i])
		}
	}
	return undo
}

func actionEqual(a, b Action) bool {
	return a.ID == b.ID && a.Adapter == b.Adapter && a.Index == b.Index && a.Value == b.Value &&
		slices.Equal(a.Values, b.Values) && slices.Equal(a.Lines, b.Lines)
}

var (
	tcpGlobalValue = regexp.MustCompile(`^(autotuninglevel=(normal|disabled|highlyrestricted|restricted|experimental)|rss=(enabled|disabled))$`)
	proxyValue     = regexp.MustCompile(`^[A-Za-z0-9.:;=\[\]_/-]{1,512}$`)
	bypassValue    = regexp.MustCompile(`^[A-Za-z0-9.:;*<>_\[\]-]{1,1000}$`)
)

// Validate checks an action before it runs; the elevated helper refuses anything that fails.
// adapterOK tells whether a name / index is one of this PC's own (non-VPN) adapters.
func Validate(a Action, adapterOK func(name string, index uint32) bool) error {
	bad := func(why string) error { return fmt.Errorf("health: refused %s: %s", a.ID, why) }
	switch a.ID {
	case "proxy_enable":
		if a.Value != "0" && a.Value != "1" {
			return bad("value")
		}
	case "pac_set":
		if a.Value != "" && !strings.HasPrefix(a.Value, "http://") && !strings.HasPrefix(a.Value, "https://") && !strings.HasPrefix(a.Value, "file:") {
			return bad("not a script address")
		}
	case "winhttp_set":
		if a.Value != "" && !proxyValue.MatchString(a.Value) || len(a.Values) > 1 || len(a.Values) == 1 && !bypassValue.MatchString(a.Values[0]) {
			return bad("proxy value")
		}
	case "hosts_comment", "hosts_uncomment":
		if len(a.Lines) == 0 {
			return bad("no lines")
		}
		for _, l := range a.Lines {
			if strings.ContainsAny(l, "\r\n") || strings.HasPrefix(strings.TrimSpace(l), "#") {
				return bad("line")
			}
		}
	case "dns_source":
		if !adapterOK("", a.Index) {
			return bad("not this PC's own adapter")
		}
		switch a.Value {
		case "dhcp":
		case "static":
			if len(a.Values) == 0 {
				return bad("no servers")
			}
			for _, v := range a.Values {
				if _, err := netip.ParseAddr(v); err != nil {
					return bad("server " + v)
				}
			}
		default:
			return bad("value")
		}
	case "service_start", "service_stop":
		if a.Value != "Dnscache" && a.Value != "W32Time" || a.ID == "service_stop" && a.Value != "W32Time" {
			return bad("service")
		}
	case "tcp_global":
		if !tcpGlobalValue.MatchString(a.Value) {
			return bad("value")
		}
	case "dhcp_renew":
		if a.Adapter == "" || !adapterOK(a.Adapter, 0) {
			return bad("not this PC's own adapter")
		}
	case "time_resync", "flush_dns", "flush_arp":
	default:
		return bad("unknown action")
	}
	return nil
}

// commentPrefix marks the hosts lines whynet turned off, so undo finds exactly those.
func commentPrefix(t time.Time) string { return "# [whynet " + t.Format("2006-01-02") + "] " }

// commentHosts turns off the given lines (exact match, line ending kept); n is how many it changed.
func commentHosts(content string, lines []string, now time.Time) (string, int) {
	return editHosts(content, func(l string) (string, bool) {
		if slices.Contains(lines, l) {
			return commentPrefix(now) + l, true
		}
		return l, false
	})
}

// uncommentHosts takes back what commentHosts did to the given lines, whatever day it was.
func uncommentHosts(content string, lines []string) (string, int) {
	return editHosts(content, func(l string) (string, bool) {
		if rest, ok := strings.CutPrefix(l, "# [whynet "); ok {
			if _, orig, ok := strings.Cut(rest, "] "); ok && slices.Contains(lines, orig) {
				return orig, true
			}
		}
		return l, false
	})
}

func editHosts(content string, f func(string) (string, bool)) (string, int) {
	parts := strings.SplitAfter(content, "\n")
	n := 0
	for i, p := range parts {
		body := strings.TrimRight(p, "\r\n")
		if out, ok := f(body); ok {
			parts[i] = out + p[len(body):]
			n++
		}
	}
	return strings.Join(parts, ""), n
}
