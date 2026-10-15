package model

import "strings"

type BrowserOption struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

type BrowserSettings struct {
	Profile  string `json:"profile"`
	Backend  string `json:"backend"`
	Headless bool   `json:"headless"`
	// LiveChat is set only by the Show Mode preset (validateBrowserRun). It
	// keeps the browser open after login and chats inside that browser.
	LiveChat bool `json:"live_chat,omitempty"`
}

const (
	ChatModeSingle = "single"
	ChatModeFolder = "folder"
	// ChatModeEVAChat is the third dashboard mode. It replaces the old
	// keyword-only responder with the stateful EVAChat Flow engine.
	ChatModeEVAChat = "eva_chat_flow"
	// ChatModeKeyword is retained only so existing saved settings can migrate
	// safely. It is never exposed as a selectable dashboard mode.
	ChatModeKeyword = "keyword"
)

type MessageSettings struct {
	ChatMode            string   `json:"chat_mode"`
	Sequence            string   `json:"sequence"`
	Usernames           string   `json:"usernames"`
	FirstMessage        string   `json:"first_message,omitempty"`
	ReplyMessage        string   `json:"reply_message,omitempty"`
	LoadedSequences     []string `json:"loaded_sequences,omitempty"`
	UseLoadedSequences  bool     `json:"use_loaded_sequences"`
	KeywordRulesContent string   `json:"keyword_rules_content,omitempty"`
	KeywordFilePath     string   `json:"keyword_file_path,omitempty"`
	// EVAChat Flow AI compose layer (OpenAI-compatible chat completions).
	// The key is stored only in the local data/ settings file.
	AIEnabled bool   `json:"ai_enabled,omitempty"`
	AIBaseURL string `json:"ai_base_url,omitempty"`
	AIAPIKey  string `json:"ai_api_key,omitempty"`
	AIModel   string `json:"ai_model,omitempty"`
}

type ProxySettings struct {
	Enabled  bool   `json:"enabled"`
	Entries  string `json:"entries"`
	Address  string `json:"address,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

type GeneralSettings struct {
	PollMinSeconds              int `json:"poll_min_seconds"`
	PollMaxSeconds              int `json:"poll_max_seconds"`
	ActionMinSeconds            int `json:"action_min_seconds"`
	ActionMaxSeconds            int `json:"action_max_seconds"`
	MatchReplyTimeoutMinSeconds int `json:"match_reply_timeout_min_seconds"`
	MatchReplyTimeoutMaxSeconds int `json:"match_reply_timeout_max_seconds"`
	MatchCloseWaitMinSeconds    int `json:"match_close_wait_min_seconds"`
	MatchCloseWaitMaxSeconds    int `json:"match_close_wait_max_seconds"`
	ReplyDelayMinSeconds        int `json:"reply_delay_min_seconds"`
	ReplyDelayMaxSeconds        int `json:"reply_delay_max_seconds"`
}

type HomeStats struct {
	BotAccounts     int `json:"bot_accounts"`
	RunningAccounts int `json:"running_accounts"`
	UserSendCount   int `json:"user_send_count"`
	DeadAccounts    int `json:"dead_accounts"`
	TrafficUsers    int `json:"traffic_users"`
	MessagesSent    int `json:"messages_sent"`
	SnapTgSentCount int `json:"snap_tg_sent_count"`
	SkippedAccounts int `json:"skipped_accounts"`
}

type LogEntry struct {
	Time    string `json:"time"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

type UISnapshot struct {
	Running        bool            `json:"running"`
	LicenseInvalid bool            `json:"license_invalid"`
	LicenseKey     string          `json:"license_key"`
	LicenseReason  string          `json:"license_reason"`
	Home           HomeStats       `json:"home"`
	Browser        BrowserSettings `json:"browser"`
	Message        MessageSettings `json:"message"`
	Proxy          ProxySettings   `json:"proxy"`
	General        GeneralSettings `json:"general"`
	BrowserOptions []BrowserOption `json:"browser_options"`
	Logs           []LogEntry      `json:"logs"`
}

func DefaultBrowserOptions() []BrowserOption {
	return []BrowserOption{
		{
			Key:         "rod",
			Label:       "Rod",
			Description: "Uses Rod for login capture with a persistent browser profile.",
		},
		{
			Key:         "chromedp",
			Label:       "ChromeDP",
			Description: "Uses Chrome DevTools Protocol through chromedp.",
		},
		{
			Key:         "show_mode",
			Label:       "Show Mode",
			Description: "Visible Rod browser for manual login. After login, chatting continues inside this browser (incoming auto-detect, replies, skip) until you close it.",
		},
	}
}

func DefaultBrowserSettings() BrowserSettings {
	return BrowserSettings{
		Profile:  "default",
		Backend:  "rod",
		Headless: false,
	}
}

func DefaultMessageSettings() MessageSettings {
	return NewMessageSettings("hello how are you\nok i am checking. wait a bit.", "")
}

func DefaultProxySettings() ProxySettings {
	return ProxySettings{}
}

func DefaultGeneralSettings() GeneralSettings {
	return GeneralSettings{
		PollMinSeconds:              20,
		PollMaxSeconds:              45,
		ActionMinSeconds:            20,
		ActionMaxSeconds:            45,
		MatchReplyTimeoutMinSeconds: 60,
		MatchReplyTimeoutMaxSeconds: 60,
		MatchCloseWaitMinSeconds:    0,
		MatchCloseWaitMaxSeconds:    0,
		ReplyDelayMinSeconds:        0,
		ReplyDelayMaxSeconds:        0,
	}
}

func NewMessageSettings(sequence string, usernames string) MessageSettings {
	settings := MessageSettings{
		ChatMode:  ChatModeSingle,
		Sequence:  NormalizeMessageSequence(sequence),
		Usernames: NormalizeUsernameList(usernames),
	}

	lines := settings.SequenceLines()
	if len(lines) > 0 {
		settings.FirstMessage = lines[0]
	}
	if len(lines) > 1 {
		settings.ReplyMessage = lines[1]
	}

	return settings
}

func (s MessageSettings) EffectiveChatMode() string {
	switch s.ChatMode {
	case ChatModeEVAChat:
		return ChatModeEVAChat
	case ChatModeKeyword:
		// v1.0 stored the third mode as "keyword". Migrate it at read time
		// rather than leaving existing users with a dead mode after upgrade.
		return ChatModeEVAChat
	case ChatModeFolder:
		return ChatModeFolder
	case ChatModeSingle:
		return ChatModeSingle
	default:
		if s.UseLoadedSequences && len(s.LoadedSequences) > 0 {
			return ChatModeFolder
		}
		return ChatModeSingle
	}
}

func (s MessageSettings) SequenceLines() []string {
	return MessageSequenceLines(s.Sequence)
}

func (s MessageSettings) UsernameLines() []string {
	return UsernameLines(s.Usernames)
}

func MessageSequenceLines(sequence string) []string {
	normalized := NormalizeMessageSequence(sequence)
	if normalized == "" {
		return nil
	}
	return strings.Split(normalized, "\n")
}

func NormalizeMessageSequence(sequence string) string {
	return normalizeLineList(sequence)
}

func UsernameLines(usernames string) []string {
	normalized := NormalizeUsernameList(usernames)
	if normalized == "" {
		return nil
	}
	return strings.Split(normalized, "\n")
}

func NormalizeUsernameList(usernames string) string {
	return normalizeLineList(usernames)
}

func normalizeLineList(value string) string {
	lines := strings.Split(strings.ReplaceAll(value, "\r", ""), "\n")
	result := make([]string, 0, len(lines))

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		result = append(result, line)
	}

	return strings.Join(result, "\n")
}

func LegacyMessageSequence(firstMessage string, replyMessage string) string {
	lines := make([]string, 0, 2)

	firstMessage = strings.TrimSpace(firstMessage)
	if firstMessage != "" {
		lines = append(lines, firstMessage)
	}

	replyMessage = strings.TrimSpace(replyMessage)
	if replyMessage != "" {
		lines = append(lines, replyMessage)
	}

	return strings.Join(lines, "\n")
}
