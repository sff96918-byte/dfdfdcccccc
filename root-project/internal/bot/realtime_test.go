package bot

import "testing"

func TestParseRealtimeEventChatMessage(t *testing.T) {
	packet := `42["chatMessage",{"message":{"id":"6a67580dbeeae21c58d93977","conversationId":"6a6757ebae6efd6705cd6bb8","author":{"id":"69a809ba3618fdb2c4c11ce2","username":"Mysterious star"},"content":"From"}}]`

	event, ok, err := parseRealtimeEvent(packet, "bot-id")
	if err != nil {
		t.Fatalf("parseRealtimeEvent() error = %v", err)
	}
	if !ok {
		t.Fatalf("parseRealtimeEvent() did not recognize chatMessage")
	}
	if event.Type != "chatMessage" {
		t.Fatalf("event.Type = %q", event.Type)
	}
	if event.ConversationID != "6a6757ebae6efd6705cd6bb8" {
		t.Fatalf("event.ConversationID = %q", event.ConversationID)
	}
	if event.UserID != "69a809ba3618fdb2c4c11ce2" {
		t.Fatalf("event.UserID = %q", event.UserID)
	}
	if event.Username != "Mysterious star" {
		t.Fatalf("event.Username = %q", event.Username)
	}
	if event.Content != "From" {
		t.Fatalf("event.Content = %q", event.Content)
	}
	if event.Target.UserID != "69a809ba3618fdb2c4c11ce2" {
		t.Fatalf("event.Target.UserID = %q", event.Target.UserID)
	}
}

func TestParseRealtimeEventTyping(t *testing.T) {
	packet := `42["typing",{"userId":"69a809ba3618fdb2c4c11ce2","conversationId":"6a6757ebae6efd6705cd6bb8"}]`

	event, ok, err := parseRealtimeEvent(packet, "bot-id")
	if err != nil {
		t.Fatalf("parseRealtimeEvent() error = %v", err)
	}
	if !ok {
		t.Fatalf("parseRealtimeEvent() did not recognize typing")
	}
	if event.Type != "typing" {
		t.Fatalf("event.Type = %q", event.Type)
	}
	if event.ConversationID != "6a6757ebae6efd6705cd6bb8" {
		t.Fatalf("event.ConversationID = %q", event.ConversationID)
	}
	if event.UserID != "69a809ba3618fdb2c4c11ce2" {
		t.Fatalf("event.UserID = %q", event.UserID)
	}
}

func TestParseRealtimeEventMatchUpdate(t *testing.T) {
	packet := `42["matchUpdate",{"match":{"conversation":{"id":"6a6757ebae6efd6705cd6bb8"},"closure":{"closed":true,"closedBy":"69a809ba3618fdb2c4c11ce2"}}}]`

	event, ok, err := parseRealtimeEvent(packet, "bot-id")
	if err != nil {
		t.Fatalf("parseRealtimeEvent() error = %v", err)
	}
	if !ok {
		t.Fatalf("parseRealtimeEvent() did not recognize matchUpdate")
	}
	if event.Type != "matchUpdate" {
		t.Fatalf("event.Type = %q", event.Type)
	}
	if event.ConversationID != "6a6757ebae6efd6705cd6bb8" {
		t.Fatalf("event.ConversationID = %q", event.ConversationID)
	}
	if !event.Closed {
		t.Fatalf("event.Closed = false, want true")
	}
	if event.ClosedBy != "69a809ba3618fdb2c4c11ce2" {
		t.Fatalf("event.ClosedBy = %q", event.ClosedBy)
	}
}

func TestParseRealtimeEventMatchQueueUpdate(t *testing.T) {
	packet := `42["matchUpdate",{"inQueue":true}]`

	event, ok, err := parseRealtimeEvent(packet, "bot-id")
	if err != nil {
		t.Fatalf("parseRealtimeEvent() error = %v", err)
	}
	if !ok {
		t.Fatalf("parseRealtimeEvent() did not recognize queue-only matchUpdate")
	}
	if !event.HasQueueState || !event.InQueue {
		t.Fatalf("expected inQueue=true, got has=%t value=%t", event.HasQueueState, event.InQueue)
	}
	if event.ConversationID != "" {
		t.Fatalf("expected empty conversation id, got %q", event.ConversationID)
	}
}

func TestParseRealtimeEventMatchUpdateTarget(t *testing.T) {
	packet := `42["matchUpdate",{"match":{"conversation":{"id":"conversation-1","participants":[{"profile":{"id":"bot-id","username":"Bot User"}},{"profile":{"id":"traffic-id","username":"Traffic User"}}]},"closure":{"closed":false}},"inQueue":false}]`

	event, ok, err := parseRealtimeEvent(packet, "bot-id")
	if err != nil {
		t.Fatalf("parseRealtimeEvent() error = %v", err)
	}
	if !ok {
		t.Fatalf("parseRealtimeEvent() did not recognize target-bearing matchUpdate")
	}
	if event.Target.ConversationID != "conversation-1" {
		t.Fatalf("event.Target.ConversationID = %q", event.Target.ConversationID)
	}
	if event.Target.UserID != "traffic-id" {
		t.Fatalf("event.Target.UserID = %q", event.Target.UserID)
	}
	if event.Target.Username != "Traffic User" {
		t.Fatalf("event.Target.Username = %q", event.Target.Username)
	}
	if event.Target.BotUsername != "Bot User" {
		t.Fatalf("event.Target.BotUsername = %q", event.Target.BotUsername)
	}
}

func TestShouldWakeForRealtimeEvent(t *testing.T) {
	currentUserID := "6a5f7e5c0a1074307571f69d"

	if shouldWakeForRealtimeEvent(currentUserID, realtimeEvent{
		Type:           "chatMessage",
		UserID:         currentUserID,
		ConversationID: "c1",
	}) {
		t.Fatalf("expected own chat message not to wake")
	}

	if !shouldWakeForRealtimeEvent(currentUserID, realtimeEvent{
		Type:           "chatMessage",
		UserID:         "69a809ba3618fdb2c4c11ce2",
		ConversationID: "c1",
	}) {
		t.Fatalf("expected foreign chat message to wake")
	}

	if !shouldWakeForRealtimeEvent(currentUserID, realtimeEvent{
		Type:           "matchUpdate",
		ConversationID: "c1",
	}) {
		t.Fatalf("expected match update to wake")
	}

	if shouldWakeForRealtimeEvent(currentUserID, realtimeEvent{
		Type:           "typing",
		UserID:         "69a809ba3618fdb2c4c11ce2",
		ConversationID: "c1",
	}) {
		t.Fatalf("expected typing not to wake")
	}
}
