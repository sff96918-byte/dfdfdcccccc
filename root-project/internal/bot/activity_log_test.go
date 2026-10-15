package bot

import (
	"testing"

	"chitchatbot/internal/model"
)

func TestFormatActivityLogsIncludeBotTrafficAndMessage(t *testing.T) {
	cfg := Config{Profile: "worker-1"}
	target := model.DMTarget{
		UserID:      "traffic-id",
		Username:    "Traffic Name",
		BotUsername: "Bot Name",
	}

	got := formatSendActivity(cfg, "bot-id", target, "hello\nthere")
	want := "SEND | bot Bot Name (bot-id) -> traffic Traffic Name (traffic-id) | message: hello there"
	if got != want {
		t.Fatalf("send activity mismatch\nwant: %q\n got: %q", want, got)
	}

	got = formatReceiveActivity(cfg, "bot-id", target, "hi   bot")
	want = "RECEIVE | traffic Traffic Name (traffic-id) -> bot Bot Name (bot-id) | message: hi bot"
	if got != want {
		t.Fatalf("receive activity mismatch\nwant: %q\n got: %q", want, got)
	}
}
