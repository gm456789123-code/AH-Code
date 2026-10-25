export namespace main {
	
	export class AgentAction {
	    type: string;
	    label: string;
	    detail: string;
	    lineRange: string;
	    icon: string;
	
	    static createFrom(source: any = {}) {
	        return new AgentAction(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.type = source["type"];
	        this.label = source["label"];
	        this.detail = source["detail"];
	        this.lineRange = source["lineRange"];
	        this.icon = source["icon"];
	    }
	}
	export class BuildDetection {
	    found: boolean;
	    command: string;
	    label: string;
	
	    static createFrom(source: any = {}) {
	        return new BuildDetection(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.found = source["found"];
	        this.command = source["command"];
	        this.label = source["label"];
	    }
	}
	export class CacheStats {
	    totalSessions: number;
	    totalSizeBytes: number;
	    sizeFormatted: string;
	    needsWarning: boolean;
	    warningMessage: string;
	
	    static createFrom(source: any = {}) {
	        return new CacheStats(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.totalSessions = source["totalSessions"];
	        this.totalSizeBytes = source["totalSizeBytes"];
	        this.sizeFormatted = source["sizeFormatted"];
	        this.needsWarning = source["needsWarning"];
	        this.warningMessage = source["warningMessage"];
	    }
	}
	export class ChatMessageItem {
	    id: string;
	    sender: string;
	    text: string;
	    thought?: string;
	    actions?: AgentAction[];
	    timestamp: string;
	    model?: string;
	
	    static createFrom(source: any = {}) {
	        return new ChatMessageItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.sender = source["sender"];
	        this.text = source["text"];
	        this.thought = source["thought"];
	        this.actions = this.convertValues(source["actions"], AgentAction);
	        this.timestamp = source["timestamp"];
	        this.model = source["model"];
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
	export class ChatTurn {
	    role: string;
	    content: string;
	    model?: string;
	
	    static createFrom(source: any = {}) {
	        return new ChatTurn(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.role = source["role"];
	        this.content = source["content"];
	        this.model = source["model"];
	    }
	}
	export class ChatRequest {
	    message: string;
	    model: string;
	    images: string[];
	    projectPath: string;
	    sessionId: string;
	    history: ChatTurn[];
	    logSessionId: string;
	
	    static createFrom(source: any = {}) {
	        return new ChatRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.message = source["message"];
	        this.model = source["model"];
	        this.images = source["images"];
	        this.projectPath = source["projectPath"];
	        this.sessionId = source["sessionId"];
	        this.history = this.convertValues(source["history"], ChatTurn);
	        this.logSessionId = source["logSessionId"];
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
	export class DiffStats {
	    additions: number;
	    deletions: number;
	    files?: string[];
	    diffText?: string;
	
	    static createFrom(source: any = {}) {
	        return new DiffStats(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.additions = source["additions"];
	        this.deletions = source["deletions"];
	        this.files = source["files"];
	        this.diffText = source["diffText"];
	    }
	}
	export class CommandResult {
	    command: string;
	    stdout: string;
	    stderr: string;
	    exitCode: number;
	    success: boolean;
	    duration: string;
	
	    static createFrom(source: any = {}) {
	        return new CommandResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.command = source["command"];
	        this.stdout = source["stdout"];
	        this.stderr = source["stderr"];
	        this.exitCode = source["exitCode"];
	        this.success = source["success"];
	        this.duration = source["duration"];
	    }
	}
	export class ChatResponse {
	    reply: string;
	    thought: string;
	    actions: AgentAction[];
	    sessionId: string;
	    status: string;
	    commandRan?: CommandResult;
	    tokensLeft: number;
	    errorMessage?: string;
	    diffStats?: DiffStats;
	
	    static createFrom(source: any = {}) {
	        return new ChatResponse(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.reply = source["reply"];
	        this.thought = source["thought"];
	        this.actions = this.convertValues(source["actions"], AgentAction);
	        this.sessionId = source["sessionId"];
	        this.status = source["status"];
	        this.commandRan = this.convertValues(source["commandRan"], CommandResult);
	        this.tokensLeft = source["tokensLeft"];
	        this.errorMessage = source["errorMessage"];
	        this.diffStats = this.convertValues(source["diffStats"], DiffStats);
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
	
	export class ClaudeAccountInfo {
	    isInstalled: boolean;
	    isLoggedIn: boolean;
	    email: string;
	    subscriptionType: string;
	
	    static createFrom(source: any = {}) {
	        return new ClaudeAccountInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.isInstalled = source["isInstalled"];
	        this.isLoggedIn = source["isLoggedIn"];
	        this.email = source["email"];
	        this.subscriptionType = source["subscriptionType"];
	    }
	}
	
	
	export class EngineStatus {
	    supported: boolean;
	    installed: boolean;
	    running: boolean;
	    installing: boolean;
	    version: string;
	
	    static createFrom(source: any = {}) {
	        return new EngineStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.supported = source["supported"];
	        this.installed = source["installed"];
	        this.running = source["running"];
	        this.installing = source["installing"];
	        this.version = source["version"];
	    }
	}
	export class GatewayStatus {
	    url: string;
	    online: boolean;
	
	    static createFrom(source: any = {}) {
	        return new GatewayStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.url = source["url"];
	        this.online = source["online"];
	    }
	}
	export class GatewayUser {
	    id: string;
	    email: string;
	    name: string;
	    avatar: string;
	    initials: string;
	    role: string;
	    tokens: number;
	    plan: string;
	    sessionToken: string;
	
	    static createFrom(source: any = {}) {
	        return new GatewayUser(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.email = source["email"];
	        this.name = source["name"];
	        this.avatar = source["avatar"];
	        this.initials = source["initials"];
	        this.role = source["role"];
	        this.tokens = source["tokens"];
	        this.plan = source["plan"];
	        this.sessionToken = source["sessionToken"];
	    }
	}
	export class GrokAccountInfo {
	    isInstalled: boolean;
	    isLoggedIn: boolean;
	    email: string;
	    firstName: string;
	    tier: string;
	    grokPath: string;
	    liveSync: boolean;
	    syncState: string;
	
	    static createFrom(source: any = {}) {
	        return new GrokAccountInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.isInstalled = source["isInstalled"];
	        this.isLoggedIn = source["isLoggedIn"];
	        this.email = source["email"];
	        this.firstName = source["firstName"];
	        this.tier = source["tier"];
	        this.grokPath = source["grokPath"];
	        this.liveSync = source["liveSync"];
	        this.syncState = source["syncState"];
	    }
	}
	export class GrokQuotaUsage {
	    weeklyRemainingPct: number;
	    weeklyRefreshTime: string;
	    weeklyKnown: boolean;
	    periodLabel: string;
	    fiveHourRemainingPct: number;
	    fiveHourRefreshTime: string;
	    fiveHourKnown: boolean;
	    productLabel: string;
	    productRemainingPct: number;
	    productKnown: boolean;
	    requestsLast5Hours: number;
	    requestsLastWeek: number;
	    tier: string;
	    live: boolean;
	    source: string;
	
	    static createFrom(source: any = {}) {
	        return new GrokQuotaUsage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.weeklyRemainingPct = source["weeklyRemainingPct"];
	        this.weeklyRefreshTime = source["weeklyRefreshTime"];
	        this.weeklyKnown = source["weeklyKnown"];
	        this.periodLabel = source["periodLabel"];
	        this.fiveHourRemainingPct = source["fiveHourRemainingPct"];
	        this.fiveHourRefreshTime = source["fiveHourRefreshTime"];
	        this.fiveHourKnown = source["fiveHourKnown"];
	        this.productLabel = source["productLabel"];
	        this.productRemainingPct = source["productRemainingPct"];
	        this.productKnown = source["productKnown"];
	        this.requestsLast5Hours = source["requestsLast5Hours"];
	        this.requestsLastWeek = source["requestsLastWeek"];
	        this.tier = source["tier"];
	        this.live = source["live"];
	        this.source = source["source"];
	    }
	}
	export class LoginSessionInfo {
	    status: string;
	    authUrl: string;
	    userCode: string;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new LoginSessionInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.status = source["status"];
	        this.authUrl = source["authUrl"];
	        this.userCode = source["userCode"];
	        this.message = source["message"];
	    }
	}
	export class SystemHardwareSpecs {
	    totalRamGb: number;
	    freeRamGb: number;
	    gpuName: string;
	    vramGb: number;
	    freeVramGb: number;
	    freeDiskGb: number;
	    hasNvidiaGpu: boolean;
	
	    static createFrom(source: any = {}) {
	        return new SystemHardwareSpecs(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.totalRamGb = source["totalRamGb"];
	        this.freeRamGb = source["freeRamGb"];
	        this.gpuName = source["gpuName"];
	        this.vramGb = source["vramGb"];
	        this.freeVramGb = source["freeVramGb"];
	        this.freeDiskGb = source["freeDiskGb"];
	        this.hasNvidiaGpu = source["hasNvidiaGpu"];
	    }
	}
	export class ModelCompatibility {
	    modelId: string;
	    modelName: string;
	    tierLabel: string;
	    downloadSizeGb: number;
	    requiredRamGb: number;
	    recommendedVramGb: number;
	    verdict: string;
	    verdictTitle: string;
	    verdictDetail: string;
	    estimatedSpeed: string;
	    canInstall: boolean;
	    specs: SystemHardwareSpecs;
	
	    static createFrom(source: any = {}) {
	        return new ModelCompatibility(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.modelId = source["modelId"];
	        this.modelName = source["modelName"];
	        this.tierLabel = source["tierLabel"];
	        this.downloadSizeGb = source["downloadSizeGb"];
	        this.requiredRamGb = source["requiredRamGb"];
	        this.recommendedVramGb = source["recommendedVramGb"];
	        this.verdict = source["verdict"];
	        this.verdictTitle = source["verdictTitle"];
	        this.verdictDetail = source["verdictDetail"];
	        this.estimatedSpeed = source["estimatedSpeed"];
	        this.canInstall = source["canInstall"];
	        this.specs = this.convertValues(source["specs"], SystemHardwareSpecs);
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
	export class ModelDef {
	    id: string;
	    name: string;
	    description: string;
	    kind: string;
	    ollamaTag?: string;
	    sizeHint?: string;
	    supportsVision: boolean;
	    command?: string;
	    args?: string[];
	    builtin: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ModelDef(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.description = source["description"];
	        this.kind = source["kind"];
	        this.ollamaTag = source["ollamaTag"];
	        this.sizeHint = source["sizeHint"];
	        this.supportsVision = source["supportsVision"];
	        this.command = source["command"];
	        this.args = source["args"];
	        this.builtin = source["builtin"];
	    }
	}
	export class ModelStatus {
	    id: string;
	    name: string;
	    description: string;
	    kind: string;
	    ollamaTag?: string;
	    sizeHint?: string;
	    supportsVision: boolean;
	    command?: string;
	    args?: string[];
	    builtin: boolean;
	    installed: boolean;
	    installing: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ModelStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.description = source["description"];
	        this.kind = source["kind"];
	        this.ollamaTag = source["ollamaTag"];
	        this.sizeHint = source["sizeHint"];
	        this.supportsVision = source["supportsVision"];
	        this.command = source["command"];
	        this.args = source["args"];
	        this.builtin = source["builtin"];
	        this.installed = source["installed"];
	        this.installing = source["installing"];
	    }
	}
	export class ProjectFileItem {
	    name: string;
	    path: string;
	    isDir: boolean;
	    size: number;
	    modTime: string;
	
	    static createFrom(source: any = {}) {
	        return new ProjectFileItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.path = source["path"];
	        this.isDir = source["isDir"];
	        this.size = source["size"];
	        this.modTime = source["modTime"];
	    }
	}
	export class QuotaStatus {
	    allowed: boolean;
	    tokensRemaining: number;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new QuotaStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.allowed = source["allowed"];
	        this.tokensRemaining = source["tokensRemaining"];
	        this.message = source["message"];
	    }
	}
	export class RemoteOllamaStatus {
	    configured: boolean;
	    connected: boolean;
	    url: string;
	    models: string[];
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new RemoteOllamaStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.configured = source["configured"];
	        this.connected = source["connected"];
	        this.url = source["url"];
	        this.models = source["models"];
	        this.error = source["error"];
	    }
	}
	export class SessionItem {
	    id: string;
	    title: string;
	    project: string;
	    updatedAt: number;
	    dateStr: string;
	
	    static createFrom(source: any = {}) {
	        return new SessionItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.title = source["title"];
	        this.project = source["project"];
	        this.updatedAt = source["updatedAt"];
	        this.dateStr = source["dateStr"];
	    }
	}
	
	export class VisionStatus {
	    online: boolean;
	    hasCloudKey: boolean;
	    hasGroqKey: boolean;
	    hasGeminiKey: boolean;
	    groqApiKey: string;
	    geminiApiKey: string;
	    visionModel: string;
	    activeProvider: string;
	    hasLocalEyes: boolean;
	    localEyesName: string;
	    localEyesTag: string;
	
	    static createFrom(source: any = {}) {
	        return new VisionStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.online = source["online"];
	        this.hasCloudKey = source["hasCloudKey"];
	        this.hasGroqKey = source["hasGroqKey"];
	        this.hasGeminiKey = source["hasGeminiKey"];
	        this.groqApiKey = source["groqApiKey"];
	        this.geminiApiKey = source["geminiApiKey"];
	        this.visionModel = source["visionModel"];
	        this.activeProvider = source["activeProvider"];
	        this.hasLocalEyes = source["hasLocalEyes"];
	        this.localEyesName = source["localEyesName"];
	        this.localEyesTag = source["localEyesTag"];
	    }
	}
	export class WorkspaceItem {
	    path: string;
	    name: string;
	    branch?: string;
	
	    static createFrom(source: any = {}) {
	        return new WorkspaceItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.name = source["name"];
	        this.branch = source["branch"];
	    }
	}

}

