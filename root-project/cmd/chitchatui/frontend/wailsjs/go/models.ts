export namespace model {
	
	export class BrowserOption {
	    key: string;
	    label: string;
	    description: string;
	
	    static createFrom(source: any = {}) {
	        return new BrowserOption(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.label = source["label"];
	        this.description = source["description"];
	    }
	}
	export class BrowserSettings {
	    profile: string;
	    backend: string;
	    headless: boolean;
	
	    static createFrom(source: any = {}) {
	        return new BrowserSettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.profile = source["profile"];
	        this.backend = source["backend"];
	        this.headless = source["headless"];
	    }
	}
	export class GeneralSettings {
	    poll_min_seconds: number;
	    poll_max_seconds: number;
	    action_min_seconds: number;
	    action_max_seconds: number;
	    match_reply_timeout_min_seconds: number;
	    match_reply_timeout_max_seconds: number;
	    match_close_wait_min_seconds: number;
	    match_close_wait_max_seconds: number;
	    reply_delay_min_seconds: number;
	    reply_delay_max_seconds: number;
	
	    static createFrom(source: any = {}) {
	        return new GeneralSettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.poll_min_seconds = source["poll_min_seconds"];
	        this.poll_max_seconds = source["poll_max_seconds"];
	        this.action_min_seconds = source["action_min_seconds"];
	        this.action_max_seconds = source["action_max_seconds"];
	        this.match_reply_timeout_min_seconds = source["match_reply_timeout_min_seconds"];
	        this.match_reply_timeout_max_seconds = source["match_reply_timeout_max_seconds"];
	        this.match_close_wait_min_seconds = source["match_close_wait_min_seconds"];
	        this.match_close_wait_max_seconds = source["match_close_wait_max_seconds"];
	        this.reply_delay_min_seconds = source["reply_delay_min_seconds"];
	        this.reply_delay_max_seconds = source["reply_delay_max_seconds"];
	    }
	}
	export class HomeStats {
	    bot_accounts: number;
	    running_accounts: number;
	    user_send_count: number;
	    dead_accounts: number;
	    traffic_users: number;
	    messages_sent: number;
	    snap_tg_sent_count: number;
	    skipped_accounts: number;
	
	    static createFrom(source: any = {}) {
	        return new HomeStats(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.bot_accounts = source["bot_accounts"];
	        this.running_accounts = source["running_accounts"];
	        this.user_send_count = source["user_send_count"];
	        this.dead_accounts = source["dead_accounts"];
	        this.traffic_users = source["traffic_users"];
	        this.messages_sent = source["messages_sent"];
	        this.snap_tg_sent_count = source["snap_tg_sent_count"];
	        this.skipped_accounts = source["skipped_accounts"];
	    }
	}
	export class LogEntry {
	    time: string;
	    level: string;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new LogEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.time = source["time"];
	        this.level = source["level"];
	        this.message = source["message"];
	    }
	}
	export class MessageSettings {
	    chat_mode: string;
	    sequence: string;
	    usernames: string;
	    first_message?: string;
	    reply_message?: string;
	    loaded_sequences?: string[];
	    use_loaded_sequences: boolean;
	    keyword_rules_content?: string;
	    keyword_file_path?: string;
	    ai_enabled?: boolean;
	    ai_base_url?: string;
	    ai_api_key?: string;
	    ai_model?: string;
	
	    static createFrom(source: any = {}) {
	        return new MessageSettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.chat_mode = source["chat_mode"];
	        this.sequence = source["sequence"];
	        this.usernames = source["usernames"];
	        this.first_message = source["first_message"];
	        this.reply_message = source["reply_message"];
	        this.loaded_sequences = source["loaded_sequences"];
	        this.use_loaded_sequences = source["use_loaded_sequences"];
	        this.keyword_rules_content = source["keyword_rules_content"];
	        this.keyword_file_path = source["keyword_file_path"];
	        this.ai_enabled = source["ai_enabled"];
	        this.ai_base_url = source["ai_base_url"];
	        this.ai_api_key = source["ai_api_key"];
	        this.ai_model = source["ai_model"];
	    }
	}
	export class ProxySettings {
	    enabled: boolean;
	    entries: string;
	    address?: string;
	    username?: string;
	    password?: string;
	
	    static createFrom(source: any = {}) {
	        return new ProxySettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.entries = source["entries"];
	        this.address = source["address"];
	        this.username = source["username"];
	        this.password = source["password"];
	    }
	}
	export class UISnapshot {
	    running: boolean;
	    license_invalid: boolean;
	    license_key: string;
	    license_reason: string;
	    home: HomeStats;
	    browser: BrowserSettings;
	    message: MessageSettings;
	    proxy: ProxySettings;
	    general: GeneralSettings;
	    browser_options: BrowserOption[];
	    logs: LogEntry[];
	
	    static createFrom(source: any = {}) {
	        return new UISnapshot(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.running = source["running"];
	        this.license_invalid = source["license_invalid"];
	        this.license_key = source["license_key"];
	        this.license_reason = source["license_reason"];
	        this.home = this.convertValues(source["home"], HomeStats);
	        this.browser = this.convertValues(source["browser"], BrowserSettings);
	        this.message = this.convertValues(source["message"], MessageSettings);
	        this.proxy = this.convertValues(source["proxy"], ProxySettings);
	        this.general = this.convertValues(source["general"], GeneralSettings);
	        this.browser_options = this.convertValues(source["browser_options"], BrowserOption);
	        this.logs = this.convertValues(source["logs"], LogEntry);
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

