package bot

import (
	"path/filepath"
	"testing"

	"chitchatbot/internal/model"
	"chitchatbot/internal/store"
)

func TestSharedRegistryBlocksBotUsersAndOwnedTraffic(t *testing.T) {
	dataDir := t.TempDir()
	cfgA := testRegistryConfig(dataDir, "bot-a")
	cfgB := testRegistryConfig(dataDir, "bot-b")

	if err := registerBotUser(cfgA, "bot-a-id"); err != nil {
		t.Fatalf("register bot A: %v", err)
	}
	if err := registerBotUser(cfgB, "bot-b-id"); err != nil {
		t.Fatalf("register bot B: %v", err)
	}

	botTargets, err := claimDMTargets(cfgA, "bot-a-id", []model.DMTarget{
		{ConversationID: "conv-bot", UserID: "bot-b-id", Username: "bot-b"},
	})
	if err != nil {
		t.Fatalf("claim bot target: %v", err)
	}
	if len(botTargets) != 0 {
		t.Fatalf("expected bot target to be skipped, got %v", botTargets)
	}

	trafficTargets, err := claimDMTargets(cfgA, "bot-a-id", []model.DMTarget{
		{ConversationID: "conv-traffic-1", UserID: "traffic-1", Username: "traffic"},
	})
	if err != nil {
		t.Fatalf("claim traffic target for bot A: %v", err)
	}
	if len(trafficTargets) != 1 || trafficTargets[0].UserID != "traffic-1" {
		t.Fatalf("expected bot A to claim traffic-1, got %v", trafficTargets)
	}

	trafficTargets, err = claimDMTargets(cfgB, "bot-b-id", []model.DMTarget{
		{ConversationID: "conv-traffic-1", UserID: "traffic-1", Username: "traffic"},
	})
	if err != nil {
		t.Fatalf("claim traffic target for bot B: %v", err)
	}
	if len(trafficTargets) != 0 {
		t.Fatalf("expected bot B to skip bot A owned traffic, got %v", trafficTargets)
	}

	selected, claimable, err := selectClaimablePendingUser(cfgB, "bot-b-id", []string{"traffic-1", "traffic-2", "bot-a-id"})
	if err != nil {
		t.Fatalf("select pending user for bot B: %v", err)
	}
	if selected != "traffic-2" {
		t.Fatalf("expected bot B to select unowned traffic-2, got %q", selected)
	}
	if len(claimable) != 1 || claimable[0] != "traffic-2" {
		t.Fatalf("expected only traffic-2 to be claimable, got %v", claimable)
	}

	claimed, err := claimAllPendingUsers(cfgB, "bot-b-id", []string{"traffic-1", "traffic-3", "traffic-4", "bot-a-id"})
	if err != nil {
		t.Fatalf("claim all pending users for bot B: %v", err)
	}
	if len(claimed) != 2 || claimed[0] != "traffic-3" || claimed[1] != "traffic-4" {
		t.Fatalf("expected traffic-3 and traffic-4 to be claimed, got %v", claimed)
	}

	var registry sharedRegistry
	ok, err := store.LoadJSON(sharedRegistryPath(dataDir), &registry)
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	if !ok {
		t.Fatal("expected registry file to be saved")
	}
	if _, ok := registry.BotUsers["bot-a-id"]; !ok {
		t.Fatalf("expected bot-a-id in registry: %+v", registry.BotUsers)
	}
	if _, ok := registry.BotUsers["bot-b-id"]; !ok {
		t.Fatalf("expected bot-b-id in registry: %+v", registry.BotUsers)
	}
	if owner := registry.TrafficOwners["traffic-1"]; owner.BotUserID != "bot-a-id" {
		t.Fatalf("expected traffic-1 owner bot-a-id, got %+v", owner)
	}
	if owner := registry.TrafficOwners["traffic-2"]; owner.BotUserID != "bot-b-id" {
		t.Fatalf("expected traffic-2 owner bot-b-id, got %+v", owner)
	}
	if owner := registry.TrafficOwners["traffic-3"]; owner.BotUserID != "bot-b-id" {
		t.Fatalf("expected traffic-3 owner bot-b-id, got %+v", owner)
	}
	if owner := registry.TrafficOwners["traffic-4"]; owner.BotUserID != "bot-b-id" {
		t.Fatalf("expected traffic-4 owner bot-b-id, got %+v", owner)
	}

	counts, err := BotAccountStatusCounts(dataDir)
	if err != nil {
		t.Fatalf("count registry statuses: %v", err)
	}
	if counts.TrafficUsers != 4 {
		t.Fatalf("expected 4 traffic users, got %+v", counts)
	}
}

func TestSharedRegistryScansSavedProfileUserIDs(t *testing.T) {
	dataDir := t.TempDir()
	cfgA := testRegistryConfig(dataDir, "bot-a")

	loginPath := filepath.Join(dataDir, "profiles", "bot-b", "login_info.json")
	if err := store.SaveJSON(loginPath, model.LoginInfo{UserID: "bot-b-id"}); err != nil {
		t.Fatalf("save profile login: %v", err)
	}
	if err := registerBotUser(cfgA, "bot-a-id"); err != nil {
		t.Fatalf("register bot A: %v", err)
	}

	targets, err := claimDMTargets(cfgA, "bot-a-id", []model.DMTarget{
		{ConversationID: "conv-bot", UserID: "bot-b-id", Username: "bot-b"},
	})
	if err != nil {
		t.Fatalf("claim saved-profile bot target: %v", err)
	}
	if len(targets) != 0 {
		t.Fatalf("expected saved-profile bot target to be skipped, got %v", targets)
	}
}

func TestRecordBotSendStatusMarksDeadAfterTenFailuresAndClearsOnSuccess(t *testing.T) {
	dataDir := t.TempDir()
	cfg := testRegistryConfig(dataDir, "bot-a")

	if err := registerBotUser(cfg, "bot-a-id"); err != nil {
		t.Fatalf("register bot: %v", err)
	}

	for attempt := 1; attempt <= deadSendFailureThreshold; attempt++ {
		markedDead, err := recordBotSendStatus(cfg, "bot-a-id", 403)
		if err != nil {
			t.Fatalf("record failed send %d: %v", attempt, err)
		}
		if attempt < deadSendFailureThreshold && markedDead {
			t.Fatalf("marked dead too early on attempt %d", attempt)
		}
		if attempt == deadSendFailureThreshold && !markedDead {
			t.Fatal("expected account to be marked dead on threshold")
		}
	}

	var registry sharedRegistry
	ok, err := store.LoadJSON(sharedRegistryPath(dataDir), &registry)
	if err != nil || !ok {
		t.Fatalf("load registry after failures: ok=%v err=%v", ok, err)
	}
	record := registry.BotUsers["bot-a-id"]
	if record.Status != botStatusDead || record.SendFailureCount != deadSendFailureThreshold {
		t.Fatalf("unexpected dead record: %+v", record)
	}

	markedDead, err := recordBotSendStatus(cfg, "bot-a-id", 200)
	if err != nil {
		t.Fatalf("record success: %v", err)
	}
	if markedDead {
		t.Fatal("success should not mark dead")
	}

	ok, err = store.LoadJSON(sharedRegistryPath(dataDir), &registry)
	if err != nil || !ok {
		t.Fatalf("load registry after success: ok=%v err=%v", ok, err)
	}
	record = registry.BotUsers["bot-a-id"]
	if record.Status != botStatusAlive || record.SendFailureCount != 0 {
		t.Fatalf("unexpected recovered record: %+v", record)
	}
}

func testRegistryConfig(dataDir string, profile string) Config {
	return Config{
		DataDir: dataDir,
		Profile: profile,
		Logf:    func(string, ...any) {},
	}
}
