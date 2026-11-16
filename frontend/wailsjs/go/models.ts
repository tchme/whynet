export namespace live {
	
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
	
	export class ChartView {
	    start: number;
	    step: number;
	    series: Record<string, live.ChartSeries>;
	
	    static createFrom(source: any = {}) {
	        return new ChartView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.start = source["start"];
	        this.step = source["step"];
	        this.series = this.convertValues(source["series"], live.ChartSeries, true);
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
	export class CheckView {
	    host: string;
	    verdict: VerdictView;
	    steps: StepView[];
	    hosts: HostView[];
	    slow: SlowView[];
	
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
	    ui: Record<string, string>;
	    live: Record<string, string>;
	    version: string;
	
	    static createFrom(source: any = {}) {
	        return new InfoView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.steps = this.convertValues(source["steps"], StepView);
	        this.ui = source["ui"];
	        this.live = source["live"];
	        this.version = source["version"];
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
	
	    static createFrom(source: any = {}) {
	        return new LiveStartView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.links = this.convertValues(source["links"], LiveLinkView);
	        this.hotkey = source["hotkey"];
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

