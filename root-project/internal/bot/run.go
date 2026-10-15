package bot

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"path/filepath"
	"strings"
	"time"

	"chitchatbot/internal/api"
	"chitchatbot/internal/browser"
	"chitchatbot/internal/model"
	"chitchatbot/internal/proxy"
	"chitchatbot/internal/store"
)

type Config struct {
	AppURL               string
	APIBaseURL           string
	DataDir              string
	Profile              string
	LoginInfoPath        string
	Backend              string
	Headless             bool
	MatchWhenIdle        bool
	PollMin              time.Duration
	PollMax              time.Duration
	ActionMin            time.Duration
	ActionMax            time.Duration
	MatchReplyTimeoutMin time.Duration
	MatchReplyTimeoutMax time.Duration
	MatchCloseWaitMin    time.Duration
	MatchCloseWaitMax    time.Duration
	ReplyDelayMin        time.Duration
	ReplyDelayMax        time.Duration
	DMLimit              int
	DMOffset             int
	MessageFetchLimit    int
	MessageSequence      []string // Legacy, kept for backwards compatibility
	AvailableSequences   [][]string
	ChatMode             string
	KeywordRules         []KeywordRule
	EVAChat              *EVAChatConfig
	MessageUsernames     []string
	TransportKey         string
	NonceAlphabet        string
	StatusFriend         int
	StatusPendingOut     int
	StatusPendingIn      int
	StatusBlocked        int
	ActionAcceptFriend   int
	ProxyRotator         *proxy.Rotator
	Logf                 LogFunc
	Activityf            LogFunc
}

type paths struct {
	ProfileDir      string
	LoginInfo       string
	BotState        string
	RelationshipIDs string
	DMUserIDs       string
	DMConversations string
	AllUserIDs      string
}

var errLoginExpired = errors.New("login expired")

func Run(ctx context.Context, cfg Config) error {
	if cfg.Backend != "rod" && cfg.Backend != "chromedp" {
		return fmt.Errorf("backend must be rod or chromedp, got %q", cfg.Backend)
	}

	p := profilePaths(cfg)
	if err := store.EnsureDir(p.ProfileDir); err != nil {
		return err
	}

	login, err := loadSavedLogin(p)
	if err != nil {
		return err
	}
	if login.Token() == "" {
		logln(cfg, "No saved login token found. Opening browser for login.")
		login, err = captureLoginViaBrowser(ctx, cfg, p)
		if err != nil {
			return err
		}
	} else {
		logf(cfg, "Using saved login from %s\n", p.LoginInfo)
	}

	currentUserID := currentUserIDFromLogin(login)
	if currentUserID == "" {
		return fmt.Errorf("could not detect current bot user ID from %s; log in again so user_id can be saved", p.LoginInfo)
	}
	if err := registerBotUser(cfg, currentUserID); err != nil {
		return err
	}
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	realtimeEvents, stopRealtime := startRealtimeListener(ctx, cfg, login, currentUserID)
	defer func() {
		if stopRealtime != nil {
			stopRealtime()
		}
	}()

	logln(cfg, "Login ready. Starting bot loop.")
	logf(cfg, "Cycle delay: %s to %s\n", cfg.PollMin, cfg.PollMax)
	logf(cfg, "Action delay: %s to %s\n", cfg.ActionMin, cfg.ActionMax)

	for {
		client := api.New(login, cfg.AppURL, cfg.ProxyRotator)

		workDone, err := runCycle(ctx, client, cfg, p, login, currentUserID, rng)
		if err != nil {
			if errors.Is(err, errLoginExpired) {
				// If login is expired, stop the worker instead of trying to refresh via browser.
				logln(cfg, "Saved login expired or missing. Stopping worker.")
				return err // This will cause the worker to stop.
			}
			return err
		}

		if cfg.MatchWhenIdle && !workDone {
			logln(cfg, "No new messages or relationships to process, attempting to find a new chat partner.")
			chatted, err := matchAndChat(ctx, client, cfg, p, login, currentUserID, rng, realtimeEvents)
			if err != nil {
				logf(cfg, "Match and chat failed: %v\n", err)
				// Don't return, just log and continue to next cycle wait.
			}
			if chatted {
				// If we had a match and chatted, we can immediately try for another match
				// instead of waiting for the poll delay.
				continue
			}
		}

		if err := waitForNextCycle(ctx, rng, cfg, currentUserID, realtimeEvents); err != nil {
			return err
		}
	}
}

func runCycle(
	ctx context.Context,
	client *api.Client,
	cfg Config,
	p paths,
	login model.LoginInfo,
	currentUserID string,
	rng *rand.Rand,
) (bool, error) {
	if login.Token() == "" {
		return false, errLoginExpired
	}

	workDone := false
	relationships, status, err := fetchRelationships(ctx, client, cfg)
	if err != nil {
		return false, err
	}
	if status == 401 || status == 403 {
		return false, errLoginExpired
	}

	pendingUserIDs := extractPendingIncomingUserIDs(relationships, cfg)
	claimablePendingUserIDs, err := claimAllPendingUsers(cfg, currentUserID, pendingUserIDs)
	if err != nil {
		return workDone, err
	}
	acceptedFriend := false

	for index, pendingUserID := range claimablePendingUserIDs {
		logf(cfg, "Accepting pending friend request %d/%d: %s\n", index+1, len(claimablePendingUserIDs), pendingUserID)
		if err := acceptFriend(ctx, client, cfg, pendingUserID); err != nil {
			return workDone, err
		}
		acceptedFriend = true
		workDone = true

		if index < len(claimablePendingUserIDs)-1 {
			if err := sleepRandom(ctx, rng, cfg, cfg.ActionMin, cfg.ActionMax, "Waiting before next friend accept"); err != nil {
				return workDone, err
			}
		}
	}

	if acceptedFriend {
		relationships, status, err = fetchRelationships(ctx, client, cfg)
		if err != nil {
			return workDone, err
		}
		if status == 401 || status == 403 {
			return workDone, errLoginExpired
		}
		pendingUserIDs = extractPendingIncomingUserIDs(relationships, cfg)
		claimablePendingUserIDs, err = filterClaimablePendingUsers(cfg, currentUserID, pendingUserIDs)
		if err != nil {
			return workDone, err
		}
	}

	conversations, status, err := fetchDMConversations(ctx, client, cfg)
	if err != nil {
		return workDone, err
	}
	if status == 401 || status == 403 {
		return workDone, errLoginExpired
	}

	dmTargets := extractDMTargets(conversations, currentUserID)
	dmTargets, err = claimDMTargets(cfg, currentUserID, dmTargets)
	if err != nil {
		return workDone, err
	}
	dmUserIDs := targetUserIDs(dmTargets)

	if err := saveDerivedFiles(p, claimablePendingUserIDs, dmUserIDs, dmTargets); err != nil {
		return workDone, err
	}

	logf(cfg, "Pending incoming IDs: %d\n", len(claimablePendingUserIDs))
	logf(cfg, "DM user IDs: %d\n", len(dmUserIDs))
	logf(cfg, "DM conversations: %d\n", len(dmTargets))

	dmWorkDone, err := processDMAutoResponses(ctx, client, cfg, p, login, currentUserID, dmTargets, rng)
	if err != nil {
		return workDone, err
	}
	if dmWorkDone {
		workDone = true
	}

	if len(claimablePendingUserIDs) == 0 {
		logln(cfg, "No pending incoming friend requests right now.")
	} else if acceptedFriend {
		logf(cfg, "%d pending friend requests remain.\n", len(claimablePendingUserIDs))
	}
	return workDone, nil
}

func loadSavedLogin(p paths) (model.LoginInfo, error) {
	var login model.LoginInfo
	ok, err := store.LoadJSON(p.LoginInfo, &login)
	if err != nil {
		return model.LoginInfo{}, err
	}
	if !ok {
		return model.LoginInfo{}, nil
	}
	return login, nil
}

func match(ctx context.Context, client *api.Client, cfg Config) error {
	body := `{"withInterests":false,"restart":true}`
	resp, err := client.Fetch(ctx, api.FetchRequest{
		Method: "POST",
		URL:    cfg.APIBaseURL + "/match",
		Headers: map[string]string{
			"Content-Type": "application/json",
		},
		Body: &body,
	})
	if err != nil {
		return fmt.Errorf("match request failed: %w", err)
	}

	if !resp.OK {
		return fmt.Errorf("match request returned non-OK status %d: %s", resp.Status, resp.Text)
	}

	logf(cfg, "Match request successful: %d %s\n", resp.Status, resp.Text)
	return nil
}

func currentUserIDFromLogin(login model.LoginInfo) string {
	return login.CurrentUserID()
}

func waitForNextCycle(
	ctx context.Context,
	rng *rand.Rand,
	cfg Config,
	currentUserID string,
	realtimeEvents <-chan realtimeEvent,
) error {
	if realtimeEvents == nil {
		return sleepRandom(ctx, rng, cfg, cfg.PollMin, cfg.PollMax, "Waiting before next cycle")
	}

	if cfg.PollMax < cfg.PollMin {
		cfg.PollMax = cfg.PollMin
	}

	delay := cfg.PollMin
	if cfg.PollMax > cfg.PollMin {
		delay += time.Duration(rng.Int63n(int64(cfg.PollMax - cfg.PollMin)))
	}

	logf(cfg, "Waiting before next cycle %s...\n", delay.Truncate(time.Second))
	timer := time.NewTimer(delay)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		case event := <-realtimeEvents:
			if shouldWakeForRealtimeEvent(currentUserID, event) {
				logf(cfg, "[realtime] waking cycle for conversation=%s event=%s\n", event.ConversationID, event.Type)
				return nil
			}
		}
	}
}

func captureLoginViaBrowser(ctx context.Context, cfg Config, p paths) (model.LoginInfo, error) {
	backend, err := browser.New(cfg.Backend)
	if err != nil {
		return model.LoginInfo{}, err
	}
	defer backend.Close()

	userDataDir := filepath.Join(p.ProfileDir, "browser-data", cfg.Backend)
	if err := store.EnsureDir(userDataDir); err != nil {
		return model.LoginInfo{}, err
	}

	logf(cfg, "Opening %s browser for login with profile %q\n", cfg.Backend, cfg.Profile)
	logf(cfg, "Browser data: %s\n", userDataDir)

	var selectedProxy *proxy.Entry
	if cfg.ProxyRotator != nil {
		if nextProxy, ok := cfg.ProxyRotator.Next(); ok {
			selectedProxy = &nextProxy
			logf(cfg, "Using proxy for browser login: %s\n", nextProxy.Label())
		}
	}

	if err := backend.Start(ctx, browser.Options{
		StartURL:    cfg.AppURL,
		UserDataDir: userDataDir,
		Headless:    cfg.Headless,
		Proxy:       selectedProxy,
	}); err != nil {
		return model.LoginInfo{}, err
	}

	return waitForLogin(ctx, backend, cfg, p)
}

func waitForLogin(ctx context.Context, backend browser.Backend, cfg Config, p paths) (model.LoginInfo, error) {
	waitingForTokenLogged := false
	waitingForSessionLogged := false

	for {
		login, err := backend.CaptureSession(ctx)
		if err != nil {
			return model.LoginInfo{}, err
		}

		token := login.Token()
		if token != "" {
			ready, authErr := browser.SessionAuthenticated(ctx, backend, cfg.APIBaseURL)
			if authErr == nil && ready {
				login.UserID = login.CurrentUserID()
				if login.UserID == "" {
					return model.LoginInfo{}, fmt.Errorf("login detected, but current bot user ID could not be read from token or browser storage")
				}
				login.Profile = cfg.Profile
				login.Backend = cfg.Backend
				login.SavedAt = time.Now().UTC()
				if err := store.SaveJSON(p.LoginInfo, login); err != nil {
					return model.LoginInfo{}, err
				}
				logf(cfg, "Login detected. Session saved to %s\n", p.LoginInfo)
				return login, nil
			}
		}

		if token == "" && !waitingForTokenLogged {
			logln(cfg, "Waiting for token cookie. Finish login in the browser window.")
			waitingForTokenLogged = true
		}

		if token != "" && !waitingForSessionLogged {
			logln(cfg, "Token detected, but the session is still unauthorized. Finish the full login in the browser window.")
			waitingForSessionLogged = true
		}

		select {
		case <-ctx.Done():
			return model.LoginInfo{}, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func profilePaths(cfg Config) paths {
	profileDir := filepath.Join(cfg.DataDir, "profiles", safeName(cfg.Profile))
	loginInfoPath := filepath.Join(profileDir, "login_info.json")
	if strings.TrimSpace(cfg.LoginInfoPath) != "" {
		loginInfoPath = cfg.LoginInfoPath
	}
	return paths{
		ProfileDir:      profileDir,
		LoginInfo:       loginInfoPath,
		BotState:        filepath.Join(profileDir, "bot_state.json"),
		RelationshipIDs: filepath.Join(profileDir, "relationships_ids.json"),
		DMUserIDs:       filepath.Join(profileDir, "dm_user_ids.json"),
		DMConversations: filepath.Join(profileDir, "dm_conversations.json"),
		AllUserIDs:      filepath.Join(profileDir, "all_user_ids.json"),
	}
}

func safeName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "default"
	}
	name = strings.ReplaceAll(name, "..", "_")
	name = strings.NewReplacer("\\", "_", "/", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_").Replace(name)
	return name
}
