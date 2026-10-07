export namespace main {
	
	export class versionStatus {
	    current: string;
	    min: string;
	    latest: string;
	    required: boolean;
	    outdated: boolean;
	    downloadUrl: string;
	    message: string;
	    checkedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new versionStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.current = source["current"];
	        this.min = source["min"];
	        this.latest = source["latest"];
	        this.required = source["required"];
	        this.outdated = source["outdated"];
	        this.downloadUrl = source["downloadUrl"];
	        this.message = source["message"];
	        this.checkedAt = source["checkedAt"];
	    }
	}

}

