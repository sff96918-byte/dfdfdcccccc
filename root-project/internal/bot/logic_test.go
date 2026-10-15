package bot

import (
	"encoding/json"
	"testing"

	"chitchatbot/internal/model"
)

func TestExtractDMTargetsIncludesBotUsername(t *testing.T) {
	var conversations []model.DMConversation

	raw := []byte(`[
		{
			"id": "conversation-1",
			"unreadMessageCount": 2,
			"updatedAt": "2026-04-20T12:00:00Z",
			"participants": [
				{"profile": {"id": "bot-id", "username": "Bot Name"}},
				{"profile": {"id": "traffic-id", "username": "Traffic Name"}}
			]
		}
	]`)
	if err := json.Unmarshal(raw, &conversations); err != nil {
		t.Fatalf("unmarshal conversations: %v", err)
	}

	targets := extractDMTargets(conversations, "bot-id")
	if len(targets) != 1 {
		t.Fatalf("expected one target, got %d", len(targets))
	}
	if targets[0].BotUsername != "Bot Name" {
		t.Fatalf("expected bot username to be carried, got %q", targets[0].BotUsername)
	}
	if targets[0].Username != "Traffic Name" {
		t.Fatalf("expected traffic username, got %q", targets[0].Username)
	}
}

func TestNormalizeDMConversationOffset(t *testing.T) {
	tests := []struct {
		name   string
		input  int
		expect int
	}{
		{name: "zero becomes first page", input: 0, expect: 1},
		{name: "negative becomes first page", input: -5, expect: 1},
		{name: "positive stays unchanged", input: 3, expect: 3},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := normalizeDMConversationOffset(test.input); got != test.expect {
				t.Fatalf("normalizeDMConversationOffset(%d) = %d, want %d", test.input, got, test.expect)
			}
		})
	}
}
