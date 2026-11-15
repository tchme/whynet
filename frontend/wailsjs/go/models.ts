export namespace main {
	
	export class HostView {
	    host: string;
	    kinds: string;
	    ping_ms: number;
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
	
	    static createFrom(source: any = {}) {
	        return new CheckView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.host = source["host"];
	        this.verdict = this.convertValues(source["verdict"], VerdictView);
	        this.steps = this.convertValues(source["steps"], StepView);
	        this.hosts = this.convertValues(source["hosts"], HostView);
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
	
	export class InfoView {
	    steps: StepView[];
	    ui: Record<string, string>;
	    version: string;
	
	    static createFrom(source: any = {}) {
	        return new InfoView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.steps = this.convertValues(source["steps"], StepView);
	        this.ui = source["ui"];
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
	

}

