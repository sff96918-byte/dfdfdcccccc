package model

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

type Cookie struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path"`
	Expires  float64 `json:"expires,omitempty"`
	Secure   bool    `json:"secure"`
	HTTPOnly bool    `json:"httpOnly"`
}

type TransportFingerprint struct {
	UserAgent           string `json:"userAgent"`
	Language            string `json:"language"`
	ColorDepth          string `json:"colorDepth"`
	ScreenResolution    string `json:"screenResolution"`
	TimeZone            string `json:"timeZone"`
	AddBehavior         string `json:"addBehavior"`
	OpenDatabase        string `json:"openDatabase"`
	Plugins             string `json:"plugins"`
	Canvas              string `json:"canvas"`
	WebGL               string `json:"webgl"`
	TouchSupport        string `json:"touchSupport"`
	HardwareConcurrency string `json:"hardwareConcurrency"`
}

type LoginInfo struct {
	UserID               string               `json:"user_id,omitempty"`
	Profile              string               `json:"profile"`
	Backend              string               `json:"backend"`
	Cookies              []Cookie             `json:"cookies"`
	UserAgent            string               `json:"user_agent"`
	LocalStorage         map[string]string    `json:"local_storage"`
	SessionStorage       map[string]string    `json:"session_storage"`
	TransportFingerprint TransportFingerprint `json:"transport_fingerprint"`
	SavedAt              time.Time            `json:"saved_at"`
}

func (l LoginInfo) Token() string {
	for _, cookie := range l.Cookies {
		if cookie.Name == "token" {
			return cookie.Value
		}
	}
	return ""
}

func (l LoginInfo) CookieHeader() string {
	var parts []string
	for _, cookie := range l.Cookies {
		name := strings.TrimSpace(cookie.Name)
		val := strings.TrimSpace(cookie.Value)
		if name != "" && val != "" {
			parts = append(parts, name+"="+val)
		}
	}
	if len(parts) == 0 {
		token := l.Token()
		if token != "" {
			return "token=" + token
		}
		return ""
	}
	return strings.Join(parts, "; ")
}

func (l LoginInfo) CurrentUserID() string {
	if userID := strings.TrimSpace(l.UserID); userID != "" {
		return userID
	}
	if userID := CurrentUserIDFromToken(l.Token()); userID != "" {
		return userID
	}
	if l.LocalStorage != nil {
		if userID := strings.TrimSpace(l.LocalStorage["cachedUserId"]); userID != "" {
			return userID
		}
	}
	if l.SessionStorage != nil {
		if userID := strings.TrimSpace(l.SessionStorage["cachedUserId"]); userID != "" {
			return userID
		}
	}
	return ""
}

func CurrentUserIDFromToken(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return ""
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}

	var claims struct {
		UserID string `json:"userId"`
		Sub    string `json:"sub"`
		ID     string `json:"id"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}

	if strings.TrimSpace(claims.UserID) != "" {
		return strings.TrimSpace(claims.UserID)
	}
	if strings.TrimSpace(claims.Sub) != "" {
		return strings.TrimSpace(claims.Sub)
	}
	if strings.TrimSpace(claims.ID) != "" {
		return strings.TrimSpace(claims.ID)
	}
	return ""
}

type Relationship struct {
	ID     string `json:"id"`
	Status int    `json:"status"`
	User   struct {
		ID       string `json:"id"`
		Username string `json:"username"`
	} `json:"user"`
	Since string `json:"since"`
}

type DMConversation struct {
	ID                 string `json:"id"`
	UnreadMessageCount int    `json:"unreadMessageCount"`
	UpdatedAt          string `json:"updatedAt"`
	Participants       []struct {
		Profile struct {
			ID       string `json:"id"`
			Username string `json:"username"`
		} `json:"profile"`
	} `json:"participants"`
}

type DMTarget struct {
	ConversationID string `json:"conversation_id"`
	UserID         string `json:"user_id"`
	Username       string `json:"username"`
	BotUsername    string `json:"bot_username,omitempty"`
	UnreadCount    int    `json:"unread_count"`
	UpdatedAt      string `json:"updated_at"`
}

type Message struct {
	ID        string `json:"id"`
	Content   string `json:"content"`
	CreatedAt string `json:"createdAt"`
	Author    struct {
		ID string `json:"id"`
	} `json:"author"`
}

type SeenMessage struct {
	ConversationID string    `json:"conversation_id"`
	SeenAt         time.Time `json:"seen_at"`
	Replied        bool      `json:"replied"`
	Content        string    `json:"content"`
	CreatedAt      string    `json:"created_at"`
}

type FirstMessage struct {
	UserID   string    `json:"user_id"`
	Username string    `json:"username"`
	Message  string    `json:"message"`
	SentAt   time.Time `json:"sent_at"`
}

// EVAChatSession is the serializable, per-user state for EVAChat Flow. It is
// deliberately kept in the existing bot state file, so a polling cycle or app
// restart does not reset the conversation back to its greeting stage.
type EVAChatSession struct {
	Started       bool   `json:"started"`
	Ended         bool   `json:"ended"`
	Stage         string `json:"stage"`
	SequenceStep  int    `json:"sequence_step,omitempty"`
	UserAge       int    `json:"user_age,omitempty"`
	HasUserAge    bool   `json:"has_user_age,omitempty"`
	UserGender    string `json:"user_gender,omitempty"`
	UserCountry   string `json:"user_country,omitempty"`
	Blocked       bool   `json:"blocked,omitempty"`
	BotReplyCount int    `json:"bot_reply_count"`
	// MiddleReplies counts bot replies sent while in the MIDDLE stage. It
	// drives the proactive ask_snap.txt turn when the user never gets flirty.
	MiddleReplies int `json:"middle_replies,omitempty"`
	// AskNext arms the strict ask chain: after a horny.txt or
	// middle_chat/flirty_reply.txt line the next bot turn is ask_snap.txt.
	AskNext bool `json:"ask_next,omitempty"`
	// AskedSnap is set once ask_snap.txt was sent; only then do bare
	// yes-words trigger the share.
	AskedSnap bool `json:"asked_snap,omitempty"`
	// SnapNudges counts retry_nudge.txt lines sent after a refusal.
	SnapNudges int `json:"snap_nudges,omitempty"`
	// HornyDetected / SnapPivoted mirror the reference engine flags.
	HornyDetected bool                `json:"horny_detected,omitempty"`
	SnapPivoted   bool                `json:"snap_pivoted,omitempty"`
	Misses        int                 `json:"misses,omitempty"`
	PoolsUsed     map[string]bool     `json:"pools_used,omitempty"`
	UsedReplies   map[string][]string `json:"used_replies,omitempty"`
	LastPool      string              `json:"last_pool,omitempty"`
	// Transcript is the bounded line-by-line memory (user + bot lines).
	Transcript []EVATranscriptLine `json:"transcript,omitempty"`
	// PersonaAge / PersonaCountry are the bot's own identity for this user
	// (randomised per conversation by the AI compose layer).
	PersonaAge     int    `json:"persona_age,omitempty"`
	PersonaCountry string `json:"persona_country,omitempty"`
}

// EVATranscriptLine is one remembered line of an EVAChat conversation.
type EVATranscriptLine struct {
	N    int    `json:"n"`
	Who  string `json:"who"`
	Text string `json:"text"`
}

type AccountState struct {
	FirstMessagesSent map[string]FirstMessage   `json:"first_messages_sent"`
	SentMessageCounts map[string]int            `json:"sent_message_counts"`
	BaselineDone      map[string]bool           `json:"baseline_done"`
	IncomingMessages  map[string]SeenMessage    `json:"incoming_messages_seen"`
	ChosenSequence    map[string]int            `json:"chosen_sequence,omitempty"`
	AssignedUsernames map[string]string         `json:"assigned_usernames,omitempty"`
	EVAChatSessions   map[string]EVAChatSession `json:"eva_chat_sessions,omitempty"`
	SnapTgSentCount   int                       `json:"snap_tg_sent_count,omitempty"`
	SkippedUsersCount int                       `json:"skipped_users_count,omitempty"`
}

type BotState map[string]AccountState
