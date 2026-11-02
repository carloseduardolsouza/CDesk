export namespace main {
	
	export class Allowed {
	    name: string;
	    id: string;
	
	    static createFrom(source: any = {}) {
	        return new Allowed(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.id = source["id"];
	    }
	}
	export class Settings {
	    name: string;
	    askSeconds: number;
	    askOnTop: boolean;
	    askSound: boolean;
	    allowControl: boolean;
	    allowClipboard: boolean;
	    onlyAllowed: boolean;
	    allowed: Allowed[];
	    mdns: boolean;
	    upnp: boolean;
	    holePunch: boolean;
	    port: number;
	    priority: string;
	    fps: number;
	    trayOnClose: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.askSeconds = source["askSeconds"];
	        this.askOnTop = source["askOnTop"];
	        this.askSound = source["askSound"];
	        this.allowControl = source["allowControl"];
	        this.allowClipboard = source["allowClipboard"];
	        this.onlyAllowed = source["onlyAllowed"];
	        this.allowed = this.convertValues(source["allowed"], Allowed);
	        this.mdns = source["mdns"];
	        this.upnp = source["upnp"];
	        this.holePunch = source["holePunch"];
	        this.port = source["port"];
	        this.priority = source["priority"];
	        this.fps = source["fps"];
	        this.trayOnClose = source["trayOnClose"];
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

