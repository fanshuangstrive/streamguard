export namespace config {
	
	export class Config {
	    listen: string;
	    upstream: string;
	    preserve_host: boolean;
	    rate: number;
	    burst: number;
	    max_wait: string;
	    timeout: string;
	    log_level: string;
	    log_file: string;
	    log_retain_days: number;
	    breaker_enabled: boolean;
	    breaker_threshold: number;
	    breaker_cooldown: string;
	    retry_enabled: boolean;
	    retry_max_attempts: number;
	    retry_initial_wait: string;
	    retry_max_wait: string;
	    size_limit_enabled: boolean;
	    size_limit_threshold: number;
	    size_limit_small_concurrent: number;
	    size_limit_large_concurrent: number;
	
	    static createFrom(source: any = {}) {
	        return new Config(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.listen = source["listen"];
	        this.upstream = source["upstream"];
	        this.preserve_host = source["preserve_host"];
	        this.rate = source["rate"];
	        this.burst = source["burst"];
	        this.max_wait = source["max_wait"];
	        this.timeout = source["timeout"];
	        this.log_level = source["log_level"];
	        this.log_file = source["log_file"];
	        this.log_retain_days = source["log_retain_days"];
	        this.breaker_enabled = source["breaker_enabled"];
	        this.breaker_threshold = source["breaker_threshold"];
	        this.breaker_cooldown = source["breaker_cooldown"];
	        this.retry_enabled = source["retry_enabled"];
	        this.retry_max_attempts = source["retry_max_attempts"];
	        this.retry_initial_wait = source["retry_initial_wait"];
	        this.retry_max_wait = source["retry_max_wait"];
	        this.size_limit_enabled = source["size_limit_enabled"];
	        this.size_limit_threshold = source["size_limit_threshold"];
	        this.size_limit_small_concurrent = source["size_limit_small_concurrent"];
	        this.size_limit_large_concurrent = source["size_limit_large_concurrent"];
	    }
	}

}

export namespace main {
	
	export class LogEntry {
	    time: string;
	    level: string;
	    source: string;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new LogEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.time = source["time"];
	        this.level = source["level"];
	        this.source = source["source"];
	        this.message = source["message"];
	    }
	}
	export class Status {
	    running: boolean;
	    addr: string;
	    upstream: string;
	    model: string;
	    rate: number;
	    burst: number;
	    total: number;
	    waited: number;
	    timedOut: number;
	    canceled: number;
	    avgWaitMs: number;
	    maxWaitMs: number;
	    breakerEnabled: boolean;
	    breakerState: string;
	    breakerTrips: number;
	    breakerRejected: number;
	    retryEnabled: boolean;
	    sizeLimitEnabled: boolean;
	    sizeLimitSmallActive: number;
	    sizeLimitLargeActive: number;
	    sizeLimitWaiting: number;
	    sizeLimitTimedOut: number;
	    configPath: string;
	
	    static createFrom(source: any = {}) {
	        return new Status(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.running = source["running"];
	        this.addr = source["addr"];
	        this.upstream = source["upstream"];
	        this.model = source["model"];
	        this.rate = source["rate"];
	        this.burst = source["burst"];
	        this.total = source["total"];
	        this.waited = source["waited"];
	        this.timedOut = source["timedOut"];
	        this.canceled = source["canceled"];
	        this.avgWaitMs = source["avgWaitMs"];
	        this.maxWaitMs = source["maxWaitMs"];
	        this.breakerEnabled = source["breakerEnabled"];
	        this.breakerState = source["breakerState"];
	        this.breakerTrips = source["breakerTrips"];
	        this.breakerRejected = source["breakerRejected"];
	        this.retryEnabled = source["retryEnabled"];
	        this.sizeLimitEnabled = source["sizeLimitEnabled"];
	        this.sizeLimitSmallActive = source["sizeLimitSmallActive"];
	        this.sizeLimitLargeActive = source["sizeLimitLargeActive"];
	        this.sizeLimitWaiting = source["sizeLimitWaiting"];
	        this.sizeLimitTimedOut = source["sizeLimitTimedOut"];
	        this.configPath = source["configPath"];
	    }
	}

}

