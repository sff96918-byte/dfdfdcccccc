package bot

import (
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"chitchatbot/internal/model"
	"chitchatbot/internal/store"
)

type sharedRegistry struct {
	BotUsers      map[string]botUserRecord      `json:"bot_users"`
	TrafficOwners map[string]trafficOwnerRecord `json:"traffic_owners"`
}

type botUserRecord struct {
	Profile           string    `json:"profile"`
	SeenAt            time.Time `json:"seen_at"`
	Status            string    `json:"status,omitempty"`
	SendFailureCount  int       `json:"send_failure_count,omitempty"`
	LastFailureStatus int       `json:"last_failure_status,omitempty"`
	LastFailureAt     time.Time `json:"last_failure_at,omitempty"`
	LastSuccessAt     time.Time `json:"last_success_at,omitempty"`
}

type trafficOwnerRecord struct {
	BotUserID  string    `json:"bot_user_id"`
	Profile    string    `json:"profile"`
	ClaimedAt  time.Time `json:"claimed_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
}

type trafficClaimResult struct {
	Allowed bool
	Reason  string
	Owner   trafficOwnerRecord
}

var registryMu sync.Mutex

const (
	botStatusAlive   = "alive"
	botStatusRunning = "running"
	botStatusDead    = "dead"

	deadSendFailureThreshold = 10
)

type BotAccountCounts struct {
	Dead         int
	TrafficUsers int
}

func registerBotUser(cfg Config, currentUserID string) error {
	currentUserID = strings.TrimSpace(currentUserID)
	if currentUserID == "" {
		logln(cfg, "Warning: could not detect current bot user ID from login session.")
		return nil
	}

	return updateSharedRegistry(cfg.DataDir, func(registry *sharedRegistry) error {
		now := time.Now().UTC()
		addKnownBotUsersFromProfiles(cfg.DataDir, registry, now)

		record := registry.BotUsers[currentUserID]
		registry.BotUsers[currentUserID] = botUserRecord{
			Profile:           cfg.Profile,
			SeenAt:            now,
			Status:            botStatusRunning,
			SendFailureCount:  record.SendFailureCount,
			LastFailureStatus: record.LastFailureStatus,
			LastFailureAt:     record.LastFailureAt,
			LastSuccessAt:     record.LastSuccessAt,
		}
		delete(registry.TrafficOwners, currentUserID)
		logf(cfg, "Registered bot user ID %s for profile %s\n", currentUserID, cfg.Profile)
		return nil
	})
}

func recordBotSendStatus(cfg Config, currentUserID string, status int) (bool, error) {
	currentUserID = strings.TrimSpace(currentUserID)
	if currentUserID == "" || status == 0 {
		return false, nil
	}

	markedDead := false
	err := updateSharedRegistry(cfg.DataDir, func(registry *sharedRegistry) error {
		now := time.Now().UTC()
		addKnownBotUsersFromProfiles(cfg.DataDir, registry, now)

		record := registry.BotUsers[currentUserID]
		record.Profile = cfg.Profile
		record.SeenAt = now

		if status < 400 {
			record.Status = botStatusAlive
			record.SendFailureCount = 0
			record.LastFailureStatus = 0
			record.LastSuccessAt = now
			registry.BotUsers[currentUserID] = record
			return nil
		}

		record.SendFailureCount++
		record.LastFailureStatus = status
		record.LastFailureAt = now
		if record.SendFailureCount >= deadSendFailureThreshold {
			markedDead = record.Status != botStatusDead
			record.Status = botStatusDead
		} else if strings.TrimSpace(record.Status) == "" {
			record.Status = botStatusAlive
		}

		registry.BotUsers[currentUserID] = record
		return nil
	})
	return markedDead, err
}

func BotAccountStatusCounts(dataDir string) (BotAccountCounts, error) {
	counts := BotAccountCounts{}
	err := updateSharedRegistry(dataDir, func(registry *sharedRegistry) error {
		addKnownBotUsersFromProfiles(dataDir, registry, time.Now().UTC())
		for _, record := range registry.BotUsers {
			if record.Status == botStatusDead {
				counts.Dead++
			}
		}
		counts.TrafficUsers = len(registry.TrafficOwners)
		return nil
	})
	return counts, err
}

func selectClaimablePendingUser(cfg Config, currentUserID string, userIDs []string) (string, []string, error) {
	return claimablePendingUsers(cfg, currentUserID, userIDs, true)
}

func claimAllPendingUsers(cfg Config, currentUserID string, userIDs []string) ([]string, error) {
	currentUserID = strings.TrimSpace(currentUserID)
	if currentUserID == "" {
		logln(cfg, "Warning: current bot user ID is unknown. Skipping pending users to avoid cross-bot traffic ownership.")
		return nil, nil
	}

	claimed := make([]string, 0, len(userIDs))
	err := updateSharedRegistry(cfg.DataDir, func(registry *sharedRegistry) error {
		now := time.Now().UTC()
		addKnownBotUsersFromProfiles(cfg.DataDir, registry, now)

		for _, userID := range userIDs {
			result := claimTrafficUser(registry, cfg, currentUserID, userID, now)
			if !result.Allowed {
				switch result.Reason {
				case "bot":
					logf(cfg, "Skipping pending user %s because it is one of our bot accounts.\n", userID)
				case "owned":
					logf(cfg, "Skipping pending user %s because profile %s already owns that traffic user.\n", userID, result.Owner.Profile)
				}
				continue
			}
			claimed = append(claimed, userID)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return claimed, nil
}

func filterClaimablePendingUsers(cfg Config, currentUserID string, userIDs []string) ([]string, error) {
	_, claimable, err := claimablePendingUsers(cfg, currentUserID, userIDs, false)
	return claimable, err
}

func claimablePendingUsers(cfg Config, currentUserID string, userIDs []string, claimFirst bool) (string, []string, error) {
	currentUserID = strings.TrimSpace(currentUserID)
	if currentUserID == "" {
		logln(cfg, "Warning: current bot user ID is unknown. Skipping pending users to avoid cross-bot traffic ownership.")
		return "", nil, nil
	}

	selected := ""
	claimable := make([]string, 0, len(userIDs))

	err := updateSharedRegistry(cfg.DataDir, func(registry *sharedRegistry) error {
		now := time.Now().UTC()
		addKnownBotUsersFromProfiles(cfg.DataDir, registry, now)

		for _, userID := range userIDs {
			userID = strings.TrimSpace(userID)
			if userID == "" {
				continue
			}
			if _, isBot := registry.BotUsers[userID]; isBot {
				logf(cfg, "Skipping pending user %s because it is one of our bot accounts.\n", userID)
				continue
			}

			owner, owned := registry.TrafficOwners[userID]
			if owned && owner.BotUserID != currentUserID {
				logf(cfg, "Skipping pending user %s because profile %s already owns that traffic user.\n", userID, owner.Profile)
				continue
			}

			claimable = append(claimable, userID)
			if claimFirst && selected == "" {
				registry.TrafficOwners[userID] = trafficOwnerRecord{
					BotUserID:  currentUserID,
					Profile:    cfg.Profile,
					ClaimedAt:  firstClaimedAt(owner, now),
					LastSeenAt: now,
				}
				selected = userID
			}
		}

		return nil
	})

	return selected, claimable, err
}

func claimDMTargets(cfg Config, currentUserID string, targets []model.DMTarget) ([]model.DMTarget, error) {
	currentUserID = strings.TrimSpace(currentUserID)
	if currentUserID == "" {
		logln(cfg, "Warning: current bot user ID is unknown. Skipping DM targets to avoid cross-bot traffic ownership.")
		return nil, nil
	}

	claimedTargets := make([]model.DMTarget, 0, len(targets))

	err := updateSharedRegistry(cfg.DataDir, func(registry *sharedRegistry) error {
		now := time.Now().UTC()
		addKnownBotUsersFromProfiles(cfg.DataDir, registry, now)

		for _, target := range targets {
			result := claimTrafficUser(registry, cfg, currentUserID, target.UserID, now)
			if !result.Allowed {
				switch result.Reason {
				case "bot":
					logf(cfg, "Skipping DM target %s because it is one of our bot accounts.\n", target.UserID)
				case "owned":
					logf(cfg, "Skipping DM target %s because profile %s already owns that traffic user.\n", target.UserID, result.Owner.Profile)
				}
				continue
			}
			claimedTargets = append(claimedTargets, target)
		}

		return nil
	})

	return claimedTargets, err
}

func claimTrafficUser(registry *sharedRegistry, cfg Config, currentUserID string, trafficUserID string, now time.Time) trafficClaimResult {
	trafficUserID = strings.TrimSpace(trafficUserID)
	if trafficUserID == "" {
		return trafficClaimResult{Allowed: false, Reason: "empty"}
	}
	if _, isBot := registry.BotUsers[trafficUserID]; isBot {
		return trafficClaimResult{Allowed: false, Reason: "bot"}
	}

	owner, owned := registry.TrafficOwners[trafficUserID]
	if owned && owner.BotUserID != currentUserID {
		return trafficClaimResult{Allowed: false, Reason: "owned", Owner: owner}
	}

	registry.TrafficOwners[trafficUserID] = trafficOwnerRecord{
		BotUserID:  currentUserID,
		Profile:    cfg.Profile,
		ClaimedAt:  firstClaimedAt(owner, now),
		LastSeenAt: now,
	}

	return trafficClaimResult{Allowed: true}
}

func updateSharedRegistry(dataDir string, update func(*sharedRegistry) error) error {
	registryMu.Lock()
	defer registryMu.Unlock()

	path := sharedRegistryPath(dataDir)
	registry, err := loadSharedRegistry(path)
	if err != nil {
		return err
	}
	if err := update(&registry); err != nil {
		return err
	}
	return saveSharedRegistry(path, registry)
}

func loadSharedRegistry(path string) (sharedRegistry, error) {
	registry := sharedRegistry{}
	ok, err := store.LoadJSON(path, &registry)
	if err != nil {
		return sharedRegistry{}, err
	}
	if !ok {
		registry = sharedRegistry{}
	}
	ensureSharedRegistry(&registry)
	return registry, nil
}

func saveSharedRegistry(path string, registry sharedRegistry) error {
	ensureSharedRegistry(&registry)
	return store.SaveJSON(path, registry)
}

func ensureSharedRegistry(registry *sharedRegistry) {
	if registry.BotUsers == nil {
		registry.BotUsers = map[string]botUserRecord{}
	}
	if registry.TrafficOwners == nil {
		registry.TrafficOwners = map[string]trafficOwnerRecord{}
	}
}

func addKnownBotUsersFromProfiles(dataDir string, registry *sharedRegistry, now time.Time) {
	profilesDir := filepath.Join(dataDir, "profiles")
	entries := make([]string, 0)
	for _, pattern := range []string{
		filepath.Join(profilesDir, "*", "login_info.json"),
		filepath.Join(profilesDir, "*", "login_info_*.json"),
	} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return
		}
		entries = append(entries, matches...)
	}
	sort.Strings(entries)

	for _, loginPath := range entries {
		var login model.LoginInfo
		ok, err := store.LoadJSON(loginPath, &login)
		if err != nil || !ok {
			continue
		}

		userID := currentUserIDFromLogin(login)
		if userID == "" {
			continue
		}

		profile := filepath.Base(filepath.Dir(loginPath))
		record := registry.BotUsers[userID]
		if record.SeenAt.IsZero() {
			record.SeenAt = now
		}
		if strings.TrimSpace(record.Status) == "" {
			record.Status = botStatusAlive
		}
		if strings.TrimSpace(record.Profile) == "" {
			record.Profile = profile
		}
		registry.BotUsers[userID] = record
		delete(registry.TrafficOwners, userID)
	}
}

func firstClaimedAt(owner trafficOwnerRecord, now time.Time) time.Time {
	if owner.ClaimedAt.IsZero() {
		return now
	}
	return owner.ClaimedAt
}

func sharedRegistryPath(dataDir string) string {
	return filepath.Join(dataDir, "bot_registry.json")
}

func targetUserIDs(targets []model.DMTarget) []string {
	result := make([]string, 0, len(targets))
	for _, target := range targets {
		if strings.TrimSpace(target.UserID) == "" {
			continue
		}
		result = append(result, target.UserID)
	}
	return uniqueStrings(result)
}
