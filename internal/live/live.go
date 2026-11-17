// Package live is the stability check: while the person plays, it pings every link of the
// chain (router, the node beyond it, the internet, the game server) at once and traces the
// route, so the analysis can tell where instability lives. Like package probe, it gathers
// facts; the rules (rules/live.yaml) decide.
package live

import (
	"errors"
	"net/netip"
	"time"

	"whynet/internal/probe"
)

// ErrDeclined means the person said no to the administrator prompt for the game's frames.
var ErrDeclined = errors.New("the administrator prompt was declined")

// Link ids, nearest first.
const (
	Router   = "router"
	Beyond   = "beyond" // the first node beyond the router: the ISP, or the VPN server
	CtlRU    = "ctl_ru" // a steady address in Russia
	CtlWorld = "ctl_world"
	Target   = "server"
)

// Controls are the two internet addresses every check compares against.
var Controls = map[string]netip.Addr{
	CtlRU:    netip.MustParseAddr("77.88.8.8"), // Yandex DNS, anycast
	CtlWorld: netip.MustParseAddr("1.1.1.1"),   // Cloudflare DNS, anycast
}

// Link is one link of the chain.
type Link struct {
	ID      string     `json:"id"`
	Addr    netip.Addr `json:"addr"`
	Name    string     `json:"name,omitempty"`     // the server's name, when given by name
	Kind    string     `json:"kind,omitempty"`     // router: wifi, cable, other; beyond: isp, vpn
	StandIn netip.Addr `json:"stand_in,omitempty"` // the server ignores pings: the last answering hop of its route
}

// Reply is one ICMP answer.
type Reply struct {
	From netip.Addr    // who answered: the target, or a router on the way when the TTL ran out
	RTT  time.Duration //
	Kind string        // reply (the target), ttl (a router on the way), unreachable
}

// WiFi is the state of the Wi-Fi link to the router at one moment.
type WiFi struct {
	Signal  int    `json:"signal_pct"`         // Windows' signal quality, 0..100
	RSSI    int    `json:"rssi_dbm,omitempty"` // dBm, e.g. -71
	Channel int    `json:"channel,omitempty"`  //
	Band    string `json:"band,omitempty"`     // 2.4, 5
	PHY     string `json:"phy,omitempty"`      // 802.11ac (Wi-Fi 5)...
	RxMbps  int    `json:"rx_mbps,omitempty"`  //
	TxMbps  int    `json:"tx_mbps,omitempty"`  //
	SSID    string `json:"ssid,omitempty"`     // masked in the report by default: it can name a person
}

// band tells the Wi-Fi band from the channel number.
//
// shortcut: 6 GHz reuses channel numbers 1-233, so a 6 GHz link reads as 2.4 or 5 here; read the
// channel's centre frequency (WlanGetNetworkBssList) when Wi-Fi 6E/7 links show up in reports.
func band(ch int) string {
	switch {
	case ch >= 1 && ch <= 14:
		return "2.4"
	case ch >= 32 && ch <= 177:
		return "5"
	}
	return ""
}

// Result is a whole stability check: facts for the rules, everything else for the window and the report.
type Result struct {
	Input   string            `json:"input"`
	Server  *probe.Server     `json:"server,omitempty"`
	Started time.Time         `json:"started"`
	Seconds int               `json:"seconds"`
	Facts   map[string]string `json:"facts"`
	Links   []LinkStats       `json:"links"`
	Trace   []Hop             `json:"trace"`
	WiFi    *WiFiSummary      `json:"wifi,omitempty"`
	CPU     *CPUSummary       `json:"cpu,omitempty"`
	Frames  *FramesSummary    `json:"frames,omitempty"` // the game's frames (admin rights; nil when not recorded or no game)
	Lags    []LagMark         `json:"lag_marks,omitempty"`
	Spikes  []Episode         `json:"spikes,omitempty"`   // every spell of trouble a link caused, up to maxEpisodes
	Total   int               `json:"spikes_total"`       // all of them
	Buckets []Bucket          `json:"buckets,omitempty"`  // the session in slices of time
	Chart   Chart             `json:"-"`                  // for the window only
	Local   map[string]any    `json:"computer,omitempty"` // OS, public address, adapters (the local step's evidence)
	Errors  map[string]string `json:"errors,omitempty"`   // what failed on the way (Wi-Fi API, a panic...)
	Mask    map[string]string `json:"-"`                  // private value -> what the report shows
}

// LinkStats sums up one link over the session. Times are milliseconds.
type LinkStats struct {
	Link
	State   string  `json:"state"` // stable, unstable, silent
	Sent    int     `json:"sent"`
	Lost    int     `json:"lost"`
	Median  float64 `json:"median_ms"`
	P95     float64 `json:"p95_ms"`
	Worst   float64 `json:"worst_ms"`
	Jitter  float64 `json:"jitter_ms"`
	Events  int     `json:"events"`     // spikes and losses, its own and repeated from nearer links
	Own     int     `json:"own"`        // the ones this link caused ("по его вине")
	OwnPct  float64 `json:"own_pct"`    //
	OwnP95  float64 `json:"own_p95_ms"` // over the moments nearer links were calm
	OwnMed  float64 `json:"own_median_ms"`
	LossPct float64 `json:"loss_pct"`
}

// Hop is one node of the route, over every trace round.
type Hop struct {
	TTL        int     `json:"ttl"`
	Addr       string  `json:"addr"` // "*" when it never answered
	ASN        string  `json:"asn,omitempty"`
	Network    string  `json:"network,omitempty"`
	Sent       int     `json:"sent"`
	Lost       int     `json:"lost"`
	Median     float64 `json:"median_ms"`
	Worst      float64 `json:"worst_ms"`
	LossCounts bool    `json:"loss_counts"`    // its loss reaches every later hop: real, not a router ignoring pings
	Jump       bool    `json:"jump,omitempty"` // the delay goes up here and stays up: where the route gets long
	Note       string  `json:"note,omitempty"` // silent (never answers), rate_limited (loses, later hops don't), target, stand_in
}

// WiFiSummary is the Wi-Fi link over the session.
type WiFiSummary struct {
	Last      WiFi `json:"last"`
	MinSignal int  `json:"min_signal_pct"`
	AvgSignal int  `json:"avg_signal_pct"`
	Readings  int  `json:"readings"`
}

// CPUSummary is the whole computer's CPU load over the session, read every tick without admin
// rights. On its own it is evidence only; with the game's frames it names the processor as the cause.
type CPUSummary struct {
	Avg      int     `json:"avg_pct"`
	Max      int     `json:"max_pct"`
	BusyPct  float64 `json:"busy_pct"` // share of the readings at CPUBusy (90 %) or more
	Readings int     `json:"readings"`
}

// LagMark is a moment the person marked "лагнуло": a bookmark, with the network jump nearest to it.
type LagMark struct {
	At        time.Time `json:"at"`
	Spike     bool      `json:"spike"`                 // the network jumped within 10 s before the mark (or 1 s after)
	AgoS      float64   `json:"ago_s,omitempty"`       // how long before the mark it jumped (negative: after)
	Link      string    `json:"link,omitempty"`        // the link that caused that jump
	WorstMs   float64   `json:"worst_ms,omitempty"`    // the worst answer at that moment, any link
	CPU       int       `json:"cpu_max_pct"`           // the highest CPU load over the same 10 s before (1 s after); -1 not measured
	Stall     bool      `json:"stall,omitempty"`       // the game's frames stalled in the same window
	StallAgoS float64   `json:"stall_ago_s,omitempty"` // how long before the mark (negative: after)
	StallMs   float64   `json:"stall_ms,omitempty"`    // the longest frame of that stall
}

// Episode is a spell of trouble one link caused: consecutive ticks with its own spikes or losses.
type Episode struct {
	At      time.Time `json:"at"`
	Link    string    `json:"link"`
	Seconds float64   `json:"seconds"`
	WorstMs float64   `json:"worst_ms"` // 0 when every answer in it was lost
	Lost    int       `json:"lost"`
}

// Bucket is a slice of the session: per link, the best, median and worst answer and the losses.
type Bucket struct {
	At     time.Time              `json:"at"`
	Links  map[string]BucketStats `json:"links"`
	CPU    int                    `json:"cpu_max_pct"`       // the highest CPU load in the slice; -1 not measured
	FPSMin float64                `json:"fps_min,omitempty"` // the game's lowest fps in the slice; -1 not in front; 0 frames not recorded
	Stalls int                    `json:"stalls,omitempty"`  // the game's stalls that began in the slice
}

type BucketStats struct {
	Min    float64 `json:"min_ms"`
	Median float64 `json:"median_ms"`
	Max    float64 `json:"max_ms"`
	Lost   int     `json:"lost"`
}

// Chart is the session thinned for drawing: at most maxPoints per link, the worst answer of each
// slice kept, so no spike disappears.
type Chart struct {
	Start  time.Time              `json:"start"`
	Step   float64                `json:"step_s"` // seconds per point
	Series map[string]ChartSeries `json:"series"`
	CPU    []int                  `json:"cpu,omitempty"`   // the highest CPU load per point, -1 unknown; empty when not measured
	FPS    []float64              `json:"fps,omitempty"`   // the game's lowest fps per point, -1 not in front; empty without frames
	Stall  []int                  `json:"stall,omitempty"` // the game's stalls per point
}

type ChartSeries struct {
	Ms    []float64 `json:"ms"`    // -1: lost
	State []int     `json:"state"` // 0 calm, 1 this link's own trouble, 2 repeated from a nearer link ([]uint8 would be base64 in JSON)
}
