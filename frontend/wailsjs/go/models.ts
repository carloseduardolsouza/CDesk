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
	export class FileEntry {
	    name: string;
	    dir: boolean;
	    size: number;
	    mod: number;
	
	    static createFrom(source: any = {}) {
	        return new FileEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.dir = source["dir"];
	        this.size = source["size"];
	        this.mod = source["mod"];
	    }
	}
	export class FileList {
	    path: string;
	    entries: FileEntry[];
	
	    static createFrom(source: any = {}) {
	        return new FileList(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.entries = this.convertValues(source["entries"], FileEntry);
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
	export class FileProps {
	    size: number;
	    files: number;
	    dirs: number;
	    mod: number;
	
	    static createFrom(source: any = {}) {
	        return new FileProps(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.size = source["size"];
	        this.files = source["files"];
	        this.dirs = source["dirs"];
	        this.mod = source["mod"];
	    }
	}
	export class FileReq {
	    op: string;
	    path: string;
	    name: string;
	    names: string[];
	    to: string;
	    size: number;
	
	    static createFrom(source: any = {}) {
	        return new FileReq(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.op = source["op"];
	        this.path = source["path"];
	        this.name = source["name"];
	        this.names = source["names"];
	        this.to = source["to"];
	        this.size = source["size"];
	    }
	}
	export class FileResp {
	    err?: string;
	    path?: string;
	    size?: number;
	    list?: FileList;
	    props?: FileProps;
	
	    static createFrom(source: any = {}) {
	        return new FileResp(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.err = source["err"];
	        this.path = source["path"];
	        this.size = source["size"];
	        this.list = this.convertValues(source["list"], FileList);
	        this.props = this.convertValues(source["props"], FileProps);
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
	export class Settings {
	    name: string;
	    askSeconds: number;
	    askOnTop: boolean;
	    askSound: boolean;
	    allowControl: boolean;
	    allowClipboard: boolean;
	    allowFiles: boolean;
	    onlyAllowed: boolean;
	    allowed: Allowed[];
	    mdns: boolean;
	    upnp: boolean;
	    holePunch: boolean;
	    port: number;
	    priority: string;
	    fps: number;
	    trayOnClose: boolean;
	    checkUpdates: boolean;
	    showCursor: boolean;
	    hideWallpaper: boolean;
	    relay: string;
	    useServer: boolean;
	
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
	        this.allowFiles = source["allowFiles"];
	        this.onlyAllowed = source["onlyAllowed"];
	        this.allowed = this.convertValues(source["allowed"], Allowed);
	        this.mdns = source["mdns"];
	        this.upnp = source["upnp"];
	        this.holePunch = source["holePunch"];
	        this.port = source["port"];
	        this.priority = source["priority"];
	        this.fps = source["fps"];
	        this.trayOnClose = source["trayOnClose"];
	        this.checkUpdates = source["checkUpdates"];
	        this.showCursor = source["showCursor"];
	        this.hideWallpaper = source["hideWallpaper"];
	        this.relay = source["relay"];
	        this.useServer = source["useServer"];
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
	export class UpdateInfo {
	    current: string;
	    latest: string;
	    url: string;
	    newer: boolean;
	
	    static createFrom(source: any = {}) {
	        return new UpdateInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.current = source["current"];
	        this.latest = source["latest"];
	        this.url = source["url"];
	        this.newer = source["newer"];
	    }
	}

}

