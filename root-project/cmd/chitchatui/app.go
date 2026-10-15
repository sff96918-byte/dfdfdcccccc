package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"chitchatbot/internal/browser"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"chitchatbot/internal/bot"
	"chitchatbot/internal/model"
	"chitchatbot/internal/proxy"
	"chitchatbot/internal/store"
	"chitchatbot/internal/uistore"
	"chitchatbot/internal/workerplan"
)

type App struct {
	ctx           context.Context
	dataDir       string
	uiStore       *uistore.Store
	mu            sync.Mutex
	running       bool
	runID         uint64
	botCancel     context.CancelFunc
	activeWorkers map[string]bool
}

func NewApp(dataDir string) (*App, error) {
	uiStore, err := uistore.Open(dataDir)
	if err != nil {
		return nil, err
	}

	app := &App{
		dataDir:       dataDir,
		uiStore:       uiStore,
		activeWorkers: map[string]bool{},
	}

	if err := app.seedDefaults(); err != nil {
		_ = uiStore.Close()
		return nil, err
	}

	return app, nil
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.log("info", "Wails UI ready.")
}

func (a *App) shutdown(ctx context.Context) {
	_ = a.StopBot()
	if a.uiStore != nil {
		_ = a.uiStore.Close()
	}
}

func (a *App) GetInitialState() (model.UISnapshot, error) {
	browserSettings, err := a.uiStore.LoadBrowserSettings()
	if err != nil {
		return model.UISnapshot{}, err
	}

	messageSettings, err := a.uiStore.LoadMessageSettings()
	if err != nil {
		return model.UISnapshot{}, err
	}

	proxySettings, err := a.uiStore.LoadProxySettings()
	if err != nil {
		return model.UISnapshot{}, err
	}

	generalSettings, err := a.uiStore.LoadGeneralSettings()
	if err != nil {
		return model.UISnapshot{}, err
	}

	logs, err := a.uiStore.ListLogs(200)
	if err != nil {
		return model.UISnapshot{}, err
	}

	a.mu.Lock()
	running := a.running
	runningAccounts := len(a.activeWorkers)
	a.mu.Unlock()

	homeStats, err := a.homeStats(browserSettings, messageSettings, runningAccounts)
	if err != nil {
		return model.UISnapshot{}, err
	}

	return model.UISnapshot{
		Running:        running,
		Home:           homeStats,
		Browser:        browserSettings,
		Message:        messageSettings,
		Proxy:          proxySettings,
		General:        generalSettings,
		BrowserOptions: model.DefaultBrowserOptions(),
		Logs:           logs,
	}, nil
}

func (a *App) homeStats(
	browserSettings model.BrowserSettings,
	messageSettings model.MessageSettings,
	runningAccounts int,
) (model.HomeStats, error) {
	workers, err := workerplan.DiscoverProfileWorkers(a.dataDir, browserSettings.Profile)
	if err != nil {
		return model.HomeStats{}, err
	}

	accountCounts, err := bot.BotAccountStatusCounts(a.dataDir)
	if err != nil {
		return model.HomeStats{}, err
	}

	messagesSent, trafficUsers, snapTgSentCount, skippedAccounts, err := a.countSentMessages()
	if err != nil {
		return model.HomeStats{}, err
	}

	botAccounts := len(workers)
	if runningAccounts+accountCounts.Dead > botAccounts {
		botAccounts = runningAccounts + accountCounts.Dead
	}

	return model.HomeStats{
		BotAccounts:     botAccounts,
		RunningAccounts: runningAccounts,
		UserSendCount:   len(messageSettings.UsernameLines()),
		DeadAccounts:    accountCounts.Dead,
		TrafficUsers:    trafficUsers,
		MessagesSent:    messagesSent,
		SnapTgSentCount: snapTgSentCount,
		SkippedAccounts: skippedAccounts,
	}, nil
}

func (a *App) countSentMessages() (int, int, int, int, error) {
	statePaths, err := filepath.Glob(filepath.Join(a.dataDir, "profiles", "*", "bot_state.json"))
	if err != nil {
		return 0, 0, 0, 0, err
	}

	counts := map[string]int{}
	uniqueUsers := map[string]bool{}
	snapTgSentCount := 0
	skippedUsersCount := 0
	for _, statePath := range statePaths {
		var state model.BotState
		ok, err := store.LoadJSON(statePath, &state)
		if err != nil {
			return 0, 0, 0, 0, err
		}
		if !ok {
			continue
		}

		for accountID, accountState := range state {
			for userID, count := range accountState.SentMessageCounts {
				if count <= 0 {
					continue
				}

				key := accountID + "\x00" + userID
				if count > counts[key] {
					counts[key] = count
				}
				uniqueUsers[userID] = true
			}
			snapTgSentCount += accountState.SnapTgSentCount
			skippedUsersCount += accountState.SkippedUsersCount
		}
	}

	total := 0
	for _, count := range counts {
		total += count
	}
	return total, len(uniqueUsers), snapTgSentCount, skippedUsersCount, nil
}

func (a *App) setWorkerRunning(profile string, running bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.activeWorkers == nil {
		a.activeWorkers = map[string]bool{}
	}
	if running {
		a.activeWorkers[profile] = true
		return
	}
	delete(a.activeWorkers, profile)
}

func (a *App) clearActiveWorkers() {
	a.mu.Lock()
	a.activeWorkers = map[string]bool{}
	a.mu.Unlock()
}

func (a *App) SaveBrowserSettings(settings model.BrowserSettings) error {
	if strings.TrimSpace(settings.Profile) == "" {
		settings.Profile = "default"
	}
	if err := a.uiStore.SaveBrowserSettings(settings); err != nil {
		return err
	}
	a.log("info", fmt.Sprintf("Browser settings saved. Backend=%s Profile=%s", settings.Backend, settings.Profile))
	return nil
}

func (a *App) RunFreshBrowsers(count int) error {
	if count < 1 {
		return fmt.Errorf("fresh browser count must be at least 1")
	}
	if count > 5 {
		return fmt.Errorf("fresh browser count cannot be more than 5")
	}

	browserSettings, err := a.uiStore.LoadBrowserSettings()
	if err != nil {
		return err
	}

	proxyRotator, proxyCount, err := a.loadProxyRotator()
	if err != nil {
		return err
	}

	loginDir := a.freshLoginInfoDir()
	if err := store.EnsureDir(loginDir); err != nil {
		return err
	}

	a.log("info", fmt.Sprintf(
		"Starting %d fresh %s browser(s). Temporary browser data will be new and isolated for this run only.",
		count,
		browserSettings.Backend,
	))
	a.log("info", fmt.Sprintf("Fresh login JSON files will be saved under %s", loginDir))
	if proxyCount > 0 {
		a.log("info", fmt.Sprintf("Fresh browsers will use %d proxy line(s) in rotation.", proxyCount))
	}

	for i := 1; i <= count; i++ {
		index := i
		var selectedProxy *proxy.Entry
		if proxyRotator != nil {
			if nextProxy, ok := proxyRotator.Next(); ok {
				selectedProxy = &nextProxy
			}
		}
		go a.runFreshBrowser(loginDir, browserSettings, selectedProxy, index, count)
	}

	return nil
}

func (a *App) SaveMessageSettings(settings model.MessageSettings) error {
	existing, _ := a.uiStore.LoadMessageSettings()
	if len(settings.LoadedSequences) == 0 && len(existing.LoadedSequences) > 0 {
		settings.LoadedSequences = existing.LoadedSequences
	}
	if err := a.uiStore.SaveMessageSettings(settings); err != nil {
		return err
	}
	a.log("info", "Message settings saved.")
	return nil
}

func (a *App) SaveProxySettings(settings model.ProxySettings) error {
	if settings.Enabled {
		entries, err := proxy.ParseList(settings.Entries)
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			return fmt.Errorf("proxy list is enabled but empty")
		}
	}

	if err := a.uiStore.SaveProxySettings(settings); err != nil {
		return err
	}

	if settings.Enabled {
		entries, _ := proxy.ParseList(settings.Entries)
		a.log("info", fmt.Sprintf("Proxy settings saved. %d proxy line(s) enabled for Rod, ChromeDP, and Go request rotation.", len(entries)))
	} else {
		a.log("info", "Proxy settings saved. Proxy use is disabled.")
	}
	return nil
}

func (a *App) SaveGeneralSettings(settings model.GeneralSettings) error {
	if err := a.uiStore.SaveGeneralSettings(settings); err != nil {
		return err
	}
	a.log("info", fmt.Sprintf(
		"Timing settings saved. Poll=%ds-%ds Action=%ds-%ds",
		settings.PollMinSeconds,
		settings.PollMaxSeconds,
		settings.ActionMinSeconds,
		settings.ActionMaxSeconds,
	))
	return nil
}

func (a *App) ClearLogs() error {
	if err := a.uiStore.ClearLogs(); err != nil {
		return err
	}
	a.emit("ui:logs_cleared", true)
	a.log("info", "Logs cleared.")
	return nil
}

func (a *App) CleanProfiles() error {
	a.mu.Lock()
	running := a.running
	a.mu.Unlock()

	if running {
		return fmt.Errorf("stop the bot before cleaning profile data")
	}

	if err := a.removeProfileData(); err != nil {
		return err
	}

	a.clearActiveWorkers()

	a.log("info", "Profile data cleaned.")
	return nil
}

func (a *App) StartBot() error {
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		a.log("info", "Bot is already running.")
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	a.runID++
	runID := a.runID
	a.running = true
	a.botCancel = cancel
	a.mu.Unlock()

	a.emitStatus(true)
	a.log("activity", "bot started wait to process")

	configs, err := a.buildBotConfigs()
	if err != nil {
		a.mu.Lock()
		a.running = false
		a.botCancel = nil
		if a.runID == runID {
			a.runID++
		}
		a.mu.Unlock()
		a.emitStatus(false)
		return err
	}

	a.log("info", fmt.Sprintf("Starting %d worker(s) across all discovered profiles.", len(configs)))

	var wg sync.WaitGroup
	wg.Add(len(configs))

	for _, cfg := range configs {
		workerCfg := cfg
		profileName := workerCfg.Profile
		a.setWorkerRunning(profileName, true)
		go func() {
			defer func() {
				a.setWorkerRunning(profileName, false)
				wg.Done()
			}()

			err := bot.Run(ctx, workerCfg)
			if errors.Is(err, context.Canceled) {
				a.log("info", fmt.Sprintf("[%s] Worker stopped.", profileName))
				return
			}
			if err != nil {
				a.log("error", fmt.Sprintf("[%s] Worker stopped with error: %v", profileName, err))
				return
			}
			a.log("info", fmt.Sprintf("[%s] Worker finished.", profileName))
		}()
	}

	go func() {
		wg.Wait()

		a.mu.Lock()
		if a.runID == runID {
			a.running = false
			a.botCancel = nil
			a.runID++
			a.mu.Unlock()
			a.log("info", "All bot workers stopped.")
			a.emitStatus(false)
			return
		}
		a.mu.Unlock()
	}()

	return nil
}

func (a *App) StopBot() error {
	a.mu.Lock()
	cancel := a.botCancel
	if cancel == nil {
		a.mu.Unlock()
		return nil
	}
	a.botCancel = nil
	a.running = false
	a.mu.Unlock()

	cancel()
	a.clearActiveWorkers()
	a.log("info", "Stopping all bot workers.")
	a.emitStatus(false)
	return nil
}

func (a *App) seedDefaults() error {
	if _, err := a.uiStore.LoadBrowserSettings(); err != nil {
		return err
	}
	if _, err := a.uiStore.LoadMessageSettings(); err != nil {
		return err
	}
	if _, err := a.uiStore.LoadProxySettings(); err != nil {
		return err
	}
	if _, err := a.uiStore.LoadGeneralSettings(); err != nil {
		return err
	}
	return nil
}

func (a *App) removeProfileData() error {
	dataDir, err := filepath.Abs(a.dataDir)
	if err != nil {
		return err
	}

	targets := []string{
		filepath.Join(a.dataDir, "profiles"),
		filepath.Join(a.dataDir, "tmp"),
		filepath.Join(a.dataDir, "fresh-runs"),
		filepath.Join(a.dataDir, "bot_registry.json"),
	}

	for _, target := range targets {
		targetAbs, err := filepath.Abs(target)
		if err != nil {
			return err
		}
		if !pathInside(dataDir, targetAbs) {
			return fmt.Errorf("refusing to clean path outside data directory: %s", targetAbs)
		}
		if err := os.RemoveAll(targetAbs); err != nil {
			return err
		}
	}

	return nil
}

func pathInside(parent string, child string) bool {
	parent = filepath.Clean(parent)
	child = filepath.Clean(child)
	if parent == child {
		return false
	}
	return strings.HasPrefix(child, parent+string(os.PathSeparator))
}

func (a *App) buildBotConfigs() ([]bot.Config, error) {
	browserSettings, err := a.uiStore.LoadBrowserSettings()
	if err != nil {
		return nil, err
	}

	messageSettings, err := a.uiStore.LoadMessageSettings()
	if err != nil {
		return nil, err
	}

	generalSettings, err := a.uiStore.LoadGeneralSettings()
	if err != nil {
		return nil, err
	}

	proxyEntries, err := a.loadProxyEntries()
	if err != nil {
		return nil, err
	}

	profiles, err := workerplan.DiscoverProfileWorkers(a.dataDir, browserSettings.Profile)
	if err != nil {
		return nil, err
	}

	assignments := workerplan.AssignProfileWorkers(profiles, proxyEntries)
	configs := make([]bot.Config, 0, len(assignments))

	for _, assignment := range assignments {
		cfg := bot.DefaultConfig(
			a.dataDir,
			assignment.Profile,
			browserSettings.Backend,
			browserSettings.Headless,
		)
		cfg.LoginInfoPath = assignment.LoginInfoPath
		cfg.MessageSequence = messageSettings.SequenceLines()
		cfg.MessageUsernames = messageSettings.UsernameLines()
		cfg.MatchWhenIdle = true
		cfg.PollMin = time.Duration(generalSettings.PollMinSeconds) * time.Second
		cfg.PollMax = time.Duration(generalSettings.PollMaxSeconds) * time.Second
		cfg.ActionMin = time.Duration(generalSettings.ActionMinSeconds) * time.Second
		cfg.ActionMax = time.Duration(generalSettings.ActionMaxSeconds) * time.Second

		profileName := assignment.Profile
		proxyLabel := "direct"
		if assignment.HasProxy {
			assignedProxy := assignment.Proxy
			cfg.ProxyRotator = proxy.NewRotator([]proxy.Entry{assignedProxy})
			proxyLabel = assignedProxy.Label()
		}

		cfg.Logf = func(format string, args ...any) {
			message := fmt.Sprintf(strings.TrimSuffix(format, "\n"), args...)
			a.log("info", fmt.Sprintf("[%s] %s", profileName, message))
		}
		cfg.Activityf = func(format string, args ...any) {
			message := fmt.Sprintf(strings.TrimSuffix(format, "\n"), args...)
			a.log("activity", message)
		}

		if assignment.LoginInfoPath != "" {
			a.log("info", fmt.Sprintf("Prepared worker for user/profile %s using %s from %s.", profileName, proxyLabel, assignment.LoginInfoPath))
		} else {
			a.log("info", fmt.Sprintf("Prepared worker for profile %s using %s.", profileName, proxyLabel))
		}
		configs = append(configs, cfg)
	}

	return configs, nil
}

func (a *App) runFreshBrowser(loginDir string, settings model.BrowserSettings, selectedProxy *proxy.Entry, index int, total int) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Keep disposable browser data outside the repo so wails dev file watching
	// doesn't restart the app when a fresh browser run creates a new directory.
	browserTempDir, err := os.MkdirTemp("", fmt.Sprintf("chitchat-fresh-browser-%02d-", index))
	if err != nil {
		a.log("error", fmt.Sprintf("[fresh %d/%d] Failed to create temp browser dir: %v", index, total, err))
		return
	}

	userDataDir := filepath.Join(browserTempDir, "user-data")
	if err := store.EnsureDir(userDataDir); err != nil {
		a.log("error", fmt.Sprintf("[fresh %d/%d] Failed to create temp browser dir: %v", index, total, err))
		return
	}

	backend, err := browser.New(settings.Backend)
	if err != nil {
		a.log("error", fmt.Sprintf("[fresh %d/%d] Failed to create browser backend: %v", index, total, err))
		return
	}
	defer func() {
		_ = backend.Close()
		a.removeFreshBrowserData(browserTempDir, index, total)
	}()

	a.log("info", fmt.Sprintf(
		"[fresh %d/%d] Opening brand-new %s browser. Old profile data is not used.",
		index,
		total,
		settings.Backend,
	))
	if selectedProxy != nil {
		a.log("info", fmt.Sprintf("[fresh %d/%d] Using proxy %s", index, total, selectedProxy.Label()))
	}

	if err := backend.Start(ctx, browser.Options{
		StartURL:            "https://app.chitchat.gg/",
		UserDataDir:         userDataDir,
		Headless:            settings.Headless,
		DisablePageRecovery: true,
		Proxy:               selectedProxy,
	}); err != nil {
		a.log("error", fmt.Sprintf("[fresh %d/%d] Browser start failed: %v", index, total, err))
		return
	}

	login, err := a.captureFreshLogin(ctx, backend, settings.Backend, index, total)
	if err != nil {
		if errors.Is(err, browser.ErrPageUnavailable) || errors.Is(err, context.Canceled) {
			a.log("info", fmt.Sprintf("[fresh %d/%d] Fresh browser was closed before login was saved.", index, total))
			return
		}
		a.log("error", fmt.Sprintf("[fresh %d/%d] Fresh login capture failed: %v", index, total, err))
		return
	}

	login.Profile = "default"
	login.Backend = settings.Backend
	login.UserID = login.CurrentUserID()
	login.SavedAt = time.Now().UTC()
	loginPath := a.freshLoginInfoPath(loginDir, login, index)

	if err := store.SaveJSON(loginPath, login); err != nil {
		a.log("error", fmt.Sprintf("[fresh %d/%d] Failed to save login info: %v", index, total, err))
		return
	}

	a.log("info", fmt.Sprintf(
		"[fresh %d/%d] Login saved to %s. Temporary browser data will be removed after close.",
		index,
		total,
		loginPath,
	))
}

func (a *App) captureFreshLogin(
	ctx context.Context,
	backend browser.Backend,
	backendName string,
	index int,
	total int,
) (model.LoginInfo, error) {
	waitingForTokenLogged := false
	waitingForSessionLogged := false

	for {
		login, err := backend.CaptureSession(ctx)
		if err != nil {
			return model.LoginInfo{}, err
		}

		token := login.Token()
		if token != "" {
			ready, authErr := browser.SessionAuthenticated(ctx, backend, "https://api.chitchat.gg")
			if authErr == nil && ready {
				login.UserID = login.CurrentUserID()
				if login.UserID == "" {
					return model.LoginInfo{}, fmt.Errorf("login detected, but current bot user ID could not be read from token or browser storage")
				}
				return login, nil
			}
		}

		if token == "" && !waitingForTokenLogged {
			a.log("info", fmt.Sprintf(
				"[fresh %d/%d] Waiting for login token in the new %s browser window.",
				index,
				total,
				backendName,
			))
			waitingForTokenLogged = true
		}

		if token != "" && !waitingForSessionLogged {
			a.log("info", fmt.Sprintf(
				"[fresh %d/%d] Token detected, but the session is still unauthorized. Finish the full login in the %s browser window.",
				index,
				total,
				backendName,
			))
			waitingForSessionLogged = true
		}

		select {
		case <-ctx.Done():
			return model.LoginInfo{}, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func (a *App) freshLoginInfoDir() string {
	return filepath.Join(a.dataDir, "profiles", "default")
}

func (a *App) freshLoginInfoPath(loginDir string, login model.LoginInfo, index int) string {
	userID := sanitizeFreshFileSegment(login.CurrentUserID())
	if userID == "" {
		userID = fmt.Sprintf("browser-%02d", index)
	}

	stamp := fmt.Sprintf("%s-%09d", login.SavedAt.UTC().Format("20060102-150405"), login.SavedAt.UTC().Nanosecond())
	filename := fmt.Sprintf("login_info_%s_%s_%02d.json", userID, stamp, index)
	return filepath.Join(loginDir, filename)
}

func sanitizeFreshFileSegment(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}

	value = strings.ReplaceAll(value, "..", "_")
	value = strings.NewReplacer("\\", "_", "/", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_", " ", "_").Replace(value)
	return strings.Trim(value, "._")
}

func (a *App) removeFreshBrowserData(tempDir string, index int, total int) {
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		if err := os.RemoveAll(tempDir); err == nil {
			a.log("info", fmt.Sprintf(
				"[fresh %d/%d] Removed temporary browser data from %s",
				index,
				total,
				tempDir,
			))
			return
		} else {
			lastErr = err
			time.Sleep(300 * time.Millisecond)
		}
	}

	if lastErr != nil {
		a.log("warn", fmt.Sprintf(
			"[fresh %d/%d] Could not remove temporary browser data from %s: %v",
			index,
			total,
			tempDir,
			lastErr,
		))
	}
}

func (a *App) loadProxyRotator() (*proxy.Rotator, int, error) {
	entries, err := a.loadProxyEntries()
	if err != nil {
		return nil, 0, err
	}
	if len(entries) == 0 {
		return nil, 0, nil
	}
	return proxy.NewRotator(entries), len(entries), nil
}

func (a *App) loadProxyEntries() ([]proxy.Entry, error) {
	proxySettings, err := a.uiStore.LoadProxySettings()
	if err != nil {
		return nil, err
	}
	if !proxySettings.Enabled {
		return nil, nil
	}

	entries, err := proxy.ParseList(proxySettings.Entries)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("proxy list is enabled but empty")
	}

	return entries, nil
}

func (a *App) emitStatus(running bool) {
	a.emit("ui:running", running)
}

func (a *App) emit(name string, data any) {
	if a.ctx == nil {
		return
	}
	runtime.EventsEmit(a.ctx, name, data)
}

func (a *App) log(level string, message string) {
	entry, err := a.uiStore.AppendLog(level, message)
	if err == nil {
		a.emit("ui:log", entry)
	}
}
