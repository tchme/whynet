export namespace live {
	
	export class CPUSummary {
	    avg_pct: number;
	    max_pct: number;
	    busy_pct: number;
	    readings: number;
	
	    static createFrom(source: any = {}) {
	        return new CPUSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.avg_pct = source["avg_pct"];
	        this.max_pct = source["max_pct"];
	        this.busy_pct = source["busy_pct"];
	        this.readings = source["readings"];
	    }
	}
	export class ChartSeries {
	    ms: number[];
	    state: number[];
	
	    static createFrom(source: any = {}) {
	        return new ChartSeries(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ms = source["ms"];
	        this.state = source["state"];
	    }
	}
	export class Stall {
	    // Go type: time
	    at: any;
	    ms: number;
	    cpu_max_pct: number;
	    net: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Stall(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.at = this.convertValues(source["at"], null);
	        this.ms = source["ms"];
	        this.cpu_max_pct = source["cpu_max_pct"];
	        this.net = source["net"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class FramesSummary {
	    exe: string;
	    front_pct: number;
	    fps_median: number;
	    fps_avg: number;
	    fps_low1: number;
	    worst_ms: number;
	    frames: number;
	    stalls?: Stall[];
	    stalls_total: number;
	    stalls_net: number;
	    stalls_cpu: number;
	    pauses?: Stall[];
	
	    static createFrom(source: any = {}) {
	        return new FramesSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.exe = source["exe"];
	        this.front_pct = source["front_pct"];
	        this.fps_median = source["fps_median"];
	        this.fps_avg = source["fps_avg"];
	        this.fps_low1 = source["fps_low1"];
	        this.worst_ms = source["worst_ms"];
	        this.frames = source["frames"];
	        this.stalls = this.convertValues(source["stalls"], Stall);
	        this.stalls_total = source["stalls_total"];
	        this.stalls_net = source["stalls_net"];
	        this.stalls_cpu = source["stalls_cpu"];
	        this.pauses = this.convertValues(source["pauses"], Stall);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class WiFi {
	    signal_pct: number;
	    rssi_dbm?: number;
	    channel?: number;
	    band?: string;
	    phy?: string;
	    rx_mbps?: number;
	    tx_mbps?: number;
	    ssid?: string;
	
	    static createFrom(source: any = {}) {
	        return new WiFi(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.signal_pct = source["signal_pct"];
	        this.rssi_dbm = source["rssi_dbm"];
	        this.channel = source["channel"];
	        this.band = source["band"];
	        this.phy = source["phy"];
	        this.rx_mbps = source["rx_mbps"];
	        this.tx_mbps = source["tx_mbps"];
	        this.ssid = source["ssid"];
	    }
	}
	export class WiFiSummary {
	    last: WiFi;
	    min_signal_pct: number;
	    avg_signal_pct: number;
	    readings: number;
	
	    static createFrom(source: any = {}) {
	        return new WiFiSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.last = this.convertValues(source["last"], WiFi);
	        this.min_signal_pct = source["min_signal_pct"];
	        this.avg_signal_pct = source["avg_signal_pct"];
	        this.readings = source["readings"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace main {
	
	export class AppServerView {
	    proto: string;
	    label: string;
	    name_from: string;
	    addr: string;
	    owner: string;
	    ad: boolean;
	    conns: number;
	    seen: string;
	    seen_tone: string;
	    check: string;
	    tone: string;
	    ping: string;
	    zapret: string;
	
	    static createFrom(source: any = {}) {
	        return new AppServerView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.proto = source["proto"];
	        this.label = source["label"];
	        this.name_from = source["name_from"];
	        this.addr = source["addr"];
	        this.owner = source["owner"];
	        this.ad = source["ad"];
	        this.conns = source["conns"];
	        this.seen = source["seen"];
	        this.seen_tone = source["seen_tone"];
	        this.check = source["check"];
	        this.tone = source["tone"];
	        this.ping = source["ping"];
	        this.zapret = source["zapret"];
	    }
	}
	export class StepView {
	    id: string;
	    name: string;
	    status: string;
	    ms: number;
	    note?: string;
	    facts: string[][];
	
	    static createFrom(source: any = {}) {
	        return new StepView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.status = source["status"];
	        this.ms = source["ms"];
	        this.note = source["note"];
	        this.facts = source["facts"];
	    }
	}
	export class VerdictView {
	    id: string;
	    title: string;
	    explain: string;
	    advice: string;
	    confidence: string;
	    tone: string;
	
	    static createFrom(source: any = {}) {
	        return new VerdictView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.title = source["title"];
	        this.explain = source["explain"];
	        this.advice = source["advice"];
	        this.confidence = source["confidence"];
	        this.tone = source["tone"];
	    }
	}
	export class AppView {
	    program: string;
	    summary: string;
	    verdict: VerdictView;
	    steps: StepView[];
	    servers: AppServerView[];
	    zapret: boolean;
	    udp_note?: string;
	
	    static createFrom(source: any = {}) {
	        return new AppView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.program = source["program"];
	        this.summary = source["summary"];
	        this.verdict = this.convertValues(source["verdict"], VerdictView);
	        this.steps = this.convertValues(source["steps"], StepView);
	        this.servers = this.convertValues(source["servers"], AppServerView);
	        this.zapret = source["zapret"];
	        this.udp_note = source["udp_note"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ChartView {
	    start: number;
	    step: number;
	    series: Record<string, live.ChartSeries>;
	    cpu?: number[];
	    fps?: number[];
	    stall?: number[];
	
	    static createFrom(source: any = {}) {
	        return new ChartView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.start = source["start"];
	        this.step = source["step"];
	        this.series = this.convertValues(source["series"], live.ChartSeries, true);
	        this.cpu = source["cpu"];
	        this.fps = source["fps"];
	        this.stall = source["stall"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class OwnRow {
	    file: string;
	    kind: string;
	    ms: number;
	    what: string;
	    whynet: string;
	    whynet_ok: boolean;
	    site: boolean;
	    ad: boolean;
	
	    static createFrom(source: any = {}) {
	        return new OwnRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.file = source["file"];
	        this.kind = source["kind"];
	        this.ms = source["ms"];
	        this.what = source["what"];
	        this.whynet = source["whynet"];
	        this.whynet_ok = source["whynet_ok"];
	        this.site = source["site"];
	        this.ad = source["ad"];
	    }
	}
	export class OwnView {
	    requests: number;
	    blocked: number;
	    failed: number;
	    late: number;
	    shown_ms: number;
	    rows: OwnRow[];
	    more_ads: number;
	
	    static createFrom(source: any = {}) {
	        return new OwnView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.requests = source["requests"];
	        this.blocked = source["blocked"];
	        this.failed = source["failed"];
	        this.late = source["late"];
	        this.shown_ms = source["shown_ms"];
	        this.rows = this.convertValues(source["rows"], OwnRow);
	        this.more_ads = source["more_ads"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class SlowView {
	    file: string;
	    kind: string;
	    ms: number;
	    unfinished: boolean;
	    where: string;
	    ping_ms: number;
	    lost: number;
	    tries: number;
	
	    static createFrom(source: any = {}) {
	        return new SlowView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.file = source["file"];
	        this.kind = source["kind"];
	        this.ms = source["ms"];
	        this.unfinished = source["unfinished"];
	        this.where = source["where"];
	        this.ping_ms = source["ping_ms"];
	        this.lost = source["lost"];
	        this.tries = source["tries"];
	    }
	}
	export class HostView {
	    host: string;
	    kinds: string;
	    ping_ms: number;
	    lost: number;
	    tries: number;
	    kbps: number;
	    requests: number;
	    failed: number;
	    result: string;
	    tone: string;
	
	    static createFrom(source: any = {}) {
	        return new HostView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.host = source["host"];
	        this.kinds = source["kinds"];
	        this.ping_ms = source["ping_ms"];
	        this.lost = source["lost"];
	        this.tries = source["tries"];
	        this.kbps = source["kbps"];
	        this.requests = source["requests"];
	        this.failed = source["failed"];
	        this.result = source["result"];
	        this.tone = source["tone"];
	    }
	}
	export class CheckView {
	    host: string;
	    verdict: VerdictView;
	    steps: StepView[];
	    hosts: HostView[];
	    slow: SlowView[];
	    own?: OwnView;
	
	    static createFrom(source: any = {}) {
	        return new CheckView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.host = source["host"];
	        this.verdict = this.convertValues(source["verdict"], VerdictView);
	        this.steps = this.convertValues(source["steps"], StepView);
	        this.hosts = this.convertValues(source["hosts"], HostView);
	        this.slow = this.convertValues(source["slow"], SlowView);
	        this.own = this.convertValues(source["own"], OwnView);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class CompareRow {
	    label: string;
	    before: string;
	    after: string;
	    better: boolean;
	    worse: boolean;
	
	    static createFrom(source: any = {}) {
	        return new CompareRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.label = source["label"];
	        this.before = source["before"];
	        this.after = source["after"];
	        this.better = source["better"];
	        this.worse = source["worse"];
	    }
	}
	export class CompareView {
	    link: string;
	    rows: CompareRow[];
	
	    static createFrom(source: any = {}) {
	        return new CompareView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.link = source["link"];
	        this.rows = this.convertValues(source["rows"], CompareRow);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class HARInfo {
	    site: string;
	    requests: number;
	    recheck: boolean;
	
	    static createFrom(source: any = {}) {
	        return new HARInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.site = source["site"];
	        this.requests = source["requests"];
	        this.recheck = source["recheck"];
	    }
	}
	export class HealthFindingView {
	    id: string;
	    kind: string;
	    title: string;
	    text: string;
	    tag: string;
	    state?: string;
	
	    static createFrom(source: any = {}) {
	        return new HealthFindingView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.kind = source["kind"];
	        this.title = source["title"];
	        this.text = source["text"];
	        this.tag = source["tag"];
	        this.state = source["state"];
	    }
	}
	export class HealthOutcomeView {
	    text: string;
	    done_text?: string;
	    status: string;
	    error?: string;
	    admin?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new HealthOutcomeView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.text = source["text"];
	        this.done_text = source["done_text"];
	        this.status = source["status"];
	        this.error = source["error"];
	        this.admin = source["admin"];
	    }
	}
	export class HealthView {
	    head: string;
	    tone: string;
	    areas: StepView[];
	    finds: HealthFindingView[];
	    infos: HealthFindingView[];
	    ok_areas: string[];
	    plan: HealthOutcomeView[];
	    admin: boolean;
	    fix_label: string;
	    fix_note: string;
	    fixable: boolean;
	    after: boolean;
	    done?: HealthOutcomeView[];
	    declined?: boolean;
	    undo: boolean;
	    undo_list: HealthOutcomeView[];
	
	    static createFrom(source: any = {}) {
	        return new HealthView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.head = source["head"];
	        this.tone = source["tone"];
	        this.areas = this.convertValues(source["areas"], StepView);
	        this.finds = this.convertValues(source["finds"], HealthFindingView);
	        this.infos = this.convertValues(source["infos"], HealthFindingView);
	        this.ok_areas = source["ok_areas"];
	        this.plan = this.convertValues(source["plan"], HealthOutcomeView);
	        this.admin = source["admin"];
	        this.fix_label = source["fix_label"];
	        this.fix_note = source["fix_note"];
	        this.fixable = source["fixable"];
	        this.after = source["after"];
	        this.done = this.convertValues(source["done"], HealthOutcomeView);
	        this.declined = source["declined"];
	        this.undo = source["undo"];
	        this.undo_list = this.convertValues(source["undo_list"], HealthOutcomeView);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class HopView {
	    ttl: number;
	    addr: string;
	    network: string;
	    lost: number;
	    sent: number;
	    median: number;
	    worst: number;
	    note: string;
	    bad: boolean;
	
	    static createFrom(source: any = {}) {
	        return new HopView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ttl = source["ttl"];
	        this.addr = source["addr"];
	        this.network = source["network"];
	        this.lost = source["lost"];
	        this.sent = source["sent"];
	        this.median = source["median"];
	        this.worst = source["worst"];
	        this.note = source["note"];
	        this.bad = source["bad"];
	    }
	}
	
	export class InfoView {
	    steps: StepView[];
	    app_steps: StepView[];
	    ui: Record<string, string>;
	    live: Record<string, string>;
	    app: Record<string, string>;
	    version: string;
	    browser: string;
	    health: Record<string, string>;
	    health_areas: StepView[];
	
	    static createFrom(source: any = {}) {
	        return new InfoView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.steps = this.convertValues(source["steps"], StepView);
	        this.app_steps = this.convertValues(source["app_steps"], StepView);
	        this.ui = source["ui"];
	        this.live = source["live"];
	        this.app = source["app"];
	        this.version = source["version"];
	        this.browser = source["browser"];
	        this.health = source["health"];
	        this.health_areas = this.convertValues(source["health_areas"], StepView);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class LagView {
	    at: number;
	    time: string;
	    spike: boolean;
	    text: string;
	    back: number;
	
	    static createFrom(source: any = {}) {
	        return new LagView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.at = source["at"];
	        this.time = source["time"];
	        this.spike = source["spike"];
	        this.text = source["text"];
	        this.back = source["back"];
	    }
	}
	export class LiveLinkView {
	    id: string;
	    name: string;
	    addr: string;
	    stand_in?: string;
	    state: string;
	    median: number;
	    p95: number;
	    worst: number;
	    loss_pct: number;
	    own: number;
	    events: number;
	
	    static createFrom(source: any = {}) {
	        return new LiveLinkView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.addr = source["addr"];
	        this.stand_in = source["stand_in"];
	        this.state = source["state"];
	        this.median = source["median"];
	        this.p95 = source["p95"];
	        this.worst = source["worst"];
	        this.loss_pct = source["loss_pct"];
	        this.own = source["own"];
	        this.events = source["events"];
	    }
	}
	export class LiveStartView {
	    links: LiveLinkView[];
	    hotkey: boolean;
	    frames: string;
	
	    static createFrom(source: any = {}) {
	        return new LiveStartView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.links = this.convertValues(source["links"], LiveLinkView);
	        this.hotkey = source["hotkey"];
	        this.frames = source["frames"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class LiveView {
	    verdict: VerdictView;
	    extras: string[];
	    seconds: number;
	    links: LiveLinkView[];
	    trace: HopView[];
	    trace_title: string;
	    wifi?: live.WiFiSummary;
	    cpu?: live.CPUSummary;
	    frames?: live.FramesSummary;
	    lags: LagView[];
	    chart: ChartView;
	    compare?: CompareView;
	    note?: string;
	
	    static createFrom(source: any = {}) {
	        return new LiveView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.verdict = this.convertValues(source["verdict"], VerdictView);
	        this.extras = source["extras"];
	        this.seconds = source["seconds"];
	        this.links = this.convertValues(source["links"], LiveLinkView);
	        this.trace = this.convertValues(source["trace"], HopView);
	        this.trace_title = source["trace_title"];
	        this.wifi = this.convertValues(source["wifi"], live.WiFiSummary);
	        this.cpu = this.convertValues(source["cpu"], live.CPUSummary);
	        this.frames = this.convertValues(source["frames"], live.FramesSummary);
	        this.lags = this.convertValues(source["lags"], LagView);
	        this.chart = this.convertValues(source["chart"], ChartView);
	        this.compare = this.convertValues(source["compare"], CompareView);
	        this.note = source["note"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	
	

}

export namespace probe {
	
	export class Program {
	    exe: string;
	    name: string;
	    title?: string;
	    window: boolean;
	    browser: boolean;
	    conns: number;
	
	    static createFrom(source: any = {}) {
	        return new Program(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.exe = source["exe"];
	        this.name = source["name"];
	        this.title = source["title"];
	        this.window = source["window"];
	        this.browser = source["browser"];
	        this.conns = source["conns"];
	    }
	}

}

export namespace update {
	
	export class Release {
	    version: string;
	    page: string;
	
	    static createFrom(source: any = {}) {
	        return new Release(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.page = source["page"];
	    }
	}

}

