export namespace fileversions {
	
	export class VersionContent {
	    id: number;
	    turn_id: string;
	    file_uid: string;
	    file_path: string;
	    created_at: string;
	    content: number[];
	
	    static createFrom(source: any = {}) {
	        return new VersionContent(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.turn_id = source["turn_id"];
	        this.file_uid = source["file_uid"];
	        this.file_path = source["file_path"];
	        this.created_at = source["created_at"];
	        this.content = source["content"];
	    }
	}
	export class VersionRecord {
	    id: number;
	    turn_id: string;
	    file_uid: string;
	    file_path: string;
	    created_at: string;
	
	    static createFrom(source: any = {}) {
	        return new VersionRecord(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.turn_id = source["turn_id"];
	        this.file_uid = source["file_uid"];
	        this.file_path = source["file_path"];
	        this.created_at = source["created_at"];
	    }
	}

}

export namespace main {
	
	export class SearchResult {
	    path: string;
	    matchType: string;
	    snippet: string;
	    line?: number;
	
	    static createFrom(source: any = {}) {
	        return new SearchResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.matchType = source["matchType"];
	        this.snippet = source["snippet"];
	        this.line = source["line"];
	    }
	}
	export class VCSInfo {
	    git: boolean;
	    svn: boolean;
	
	    static createFrom(source: any = {}) {
	        return new VCSInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.git = source["git"];
	        this.svn = source["svn"];
	    }
	}
	export class applyScenarioArgs {
	    scenario_id: string;
	
	    static createFrom(source: any = {}) {
	        return new applyScenarioArgs(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.scenario_id = source["scenario_id"];
	    }
	}
	export class askUserResponse {
	    answer: string;
	    custom?: string;
	    pipe_addr: string;
	
	    static createFrom(source: any = {}) {
	        return new askUserResponse(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.answer = source["answer"];
	        this.custom = source["custom"];
	        this.pipe_addr = source["pipe_addr"];
	    }
	}
	export class cancelChatArgs {
	    turn_id: string;
	
	    static createFrom(source: any = {}) {
	        return new cancelChatArgs(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.turn_id = source["turn_id"];
	    }
	}
	export class createSessionArgs {
	    workDir: string;
	    title: string;
	
	    static createFrom(source: any = {}) {
	        return new createSessionArgs(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.workDir = source["workDir"];
	        this.title = source["title"];
	    }
	}
	export class sendChatArgs {
	    session_id: string;
	    turn_id: string;
	    q: string;
	    files: string[];
	    llm: string;
	    think: string;
	    effort: string;
	
	    static createFrom(source: any = {}) {
	        return new sendChatArgs(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.session_id = source["session_id"];
	        this.turn_id = source["turn_id"];
	        this.q = source["q"];
	        this.files = source["files"];
	        this.llm = source["llm"];
	        this.think = source["think"];
	        this.effort = source["effort"];
	    }
	}
	export class updateTaskArgs {
	    task_id: string;
	    status: string;
	    progress: number;
	    result: string;
	
	    static createFrom(source: any = {}) {
	        return new updateTaskArgs(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.task_id = source["task_id"];
	        this.status = source["status"];
	        this.progress = source["progress"];
	        this.result = source["result"];
	    }
	}

}

