package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"chitchatbot/internal/applog"
	"chitchatbot/internal/browser"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"chitchatbot/internal/bot"
	"chitchatbot/internal/license"
	"chitchatbot/internal/model"
	"chitchatbot/internal/proxy"
	"chitchatbot/internal/store"
	"chitchatbot/internal/uistore"
	"chitchatbot/internal/workerplan"
)

type App struct {
	ctx            context.Context
	dataDir        string
	uiStore        *uistore.Store
	mu             sync.Mutex
	chatFeed       *homeChatFeed
	running        bool
	runID          uint64
	botCancel      context.CancelFunc
	activeWorkers  map[string]bool
	warnedStates   map[string]bool
	licenseKey     string
	licenseInvalid bool
	licenseReason  string
	licenseTimer   *time.Ticker
	licenseDone    chan bool
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

	key, err := license.GenerateHardwareKey()
	if err != nil {
		app.licenseInvalid = true
		app.licenseReason = "Could not read hardware identifiers."
	} else {
		valid, reason := license.VerifyLicense(key)
		if !valid {
			app.licenseInvalid = true
			app.licenseKey = key
			app.licenseReason = reason
		} else {
			app.licenseKey = key
		}
	}

	return app, nil
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	if a.licenseInvalid {
		runtime.WindowSetSize(a.ctx, 500, 350)
		runtime.WindowCenter(a.ctx)
		return
	}

	a.licenseTimer = time.NewTicker(24 * time.Hour)
	a.licenseDone = make(chan bool)

	go func() {
		for {
			select {
			case <-a.licenseDone:
				return
			case <-a.licenseTimer.C:
				v, r := license.VerifyLicense(a.licenseKey)
				if !v {
					a.mu.Lock()
					a.licenseInvalid = true
					a.licenseReason = r
					a.mu.Unlock()
					a.emit("ui:license_invalid", map[string]string{"key": a.licenseKey, "reason": r})
					runtime.WindowSetSize(a.ctx, 500, 350)
					runtime.WindowCenter(a.ctx)
					return
				}
			}
		}
	}()

	a.log("info", "Wails UI ready.")
}

func (a *App) shutdown(ctx context.Context) {
	_ = a.StopBot()
	if a.licenseTimer != nil {
		a.licenseTimer.Stop()
	}
	if a.licenseDone != nil {
		close(a.licenseDone)
	}
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
	isLicenseInvalid := a.licenseInvalid
	licenseKey := a.licenseKey
	licenseReason := a.licenseReason
	a.mu.Unlock()

	homeStats, err := a.homeStats(browserSettings, messageSettings, runningAccounts)
	if err != nil {
		return model.UISnapshot{}, err
	}

	return model.UISnapshot{
		Running:        running,
		LicenseInvalid: isLicenseInvalid,
		LicenseKey:     licenseKey,
		LicenseReason:  licenseReason,
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

// warnUnreadableState reports a broken state file once per path, so the 3-second
// dashboard refresh cannot flood logs.txt while the dashboard keeps loading.
func (a *App) warnUnreadableState(path string, err error) {
	a.mu.Lock()
	if a.warnedStates == nil {
		a.warnedStates = map[string]bool{}
	}
	seen := a.warnedStates[path]
	a.warnedStates[path] = true
	a.mu.Unlock()
	if seen {
		return
	}
	a.log("warn", fmt.Sprintf("Skipped unreadable bot state %s: %v", path, err))
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
			// A single unreadable state file must never blank the dashboard (it
			// refreshes every 3s, hence the once-per-path warning).
			a.warnUnreadableState(statePath, err)
			continue
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

// RunFreshBrowsers preserves compatibility for callers using saved settings.
func (a *App) RunFreshBrowsers(count int) error {
	settings, err := a.uiStore.LoadBrowserSettings()
	if err != nil {
		return err
	}
	return a.RunBrowser(settings, count)
}

// RunBrowser uses the clicked row and current form values, never a second
// read of shared saved settings. Accepted settings become the next defaults.
func (a *App) RunBrowser(settings model.BrowserSettings, count int) error {
	return a.runBrowser(settings, count, func(dir, profile string, request model.BrowserSettings, entry *proxy.Entry, index, total int) {
		go a.runFreshBrowser(dir, profile, request, entry, index, total)
	})
}

func validateBrowserRun(settings model.BrowserSettings, count int) (model.BrowserSettings, error) {
	if count < 1 || count > 5 {
		return settings, fmt.Errorf("browser count must be between 1 and 5")
	}
	supported := false
	for _, option := range model.DefaultBrowserOptions() {
		if settings.Backend == option.Key {
			supported = true
			break
		}
	}
	if !supported {
		return settings, fmt.Errorf("unsupported browser backend: %q", settings.Backend)
	}
	// Show Mode is a visible Rod launch preset, not a third browser driver.
	// Normalize before persistence/dispatch so saved sessions use a real backend.
	if settings.Backend == "show_mode" {
		settings.Backend = "rod"
		settings.Headless = false
		settings.LiveChat = true
	}
	settings.Profile = safeProfileName(settings.Profile)
	return settings, nil
}

// The dispatch callback keeps request isolation and preflight failures testable
// without opening a real browser or accessing an account.
func (a *App) runBrowser(settings model.BrowserSettings, count int, launch func(string, string, model.BrowserSettings, *proxy.Entry, int, int)) error {
	browserSettings, err := validateBrowserRun(settings, count)
	if err != nil {
		return err
	}

	proxyRotator, proxyCount, err := a.loadProxyRotator()
	if err != nil {
		return err
	}

	profileName := browserSettings.Profile
	profileDir := filepath.Join(a.dataDir, "profiles", profileName)
	if err := store.EnsureDir(profileDir); err != nil {
		return err
	}

	if err := a.uiStore.SaveBrowserSettings(browserSettings); err != nil {
		return err
	}

	a.log("info", fmt.Sprintf(
		"Starting %d fresh %s browser(s) for profile '%s'.",
		count,
		browserSettings.Backend,
		profileName,
	))
	a.log("info", fmt.Sprintf("Fresh login JSON files will be saved under %s", profileDir))
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
		launch(profileDir, profileName, browserSettings, selectedProxy, index, count)
	}

	return nil
}

func (a *App) SaveMessageSettings(settings model.MessageSettings) error {
	existing, _ := a.uiStore.LoadMessageSettings()
	if len(settings.LoadedSequences) == 0 && len(existing.LoadedSequences) > 0 {
		settings.LoadedSequences = existing.LoadedSequences
	}
	// Keyword fields are retained in the stored JSON only for backward
	// compatibility. The selectable third mode now uses EVAChat Flow.
	switch settings.ChatMode {
	case model.ChatModeEVAChat, model.ChatModeKeyword:
		settings.ChatMode = model.ChatModeEVAChat
		settings.UseLoadedSequences = false
	case model.ChatModeFolder:
		settings.UseLoadedSequences = true
	case model.ChatModeSingle:
		settings.UseLoadedSequences = false
	default:
		if settings.UseLoadedSequences && len(settings.LoadedSequences) > 0 {
			settings.ChatMode = model.ChatModeFolder
			settings.UseLoadedSequences = true
		} else {
			settings.ChatMode = model.ChatModeSingle
			settings.UseLoadedSequences = false
		}
	}
	if err := a.uiStore.SaveMessageSettings(settings); err != nil {
		return err
	}
	a.log("info", fmt.Sprintf("Message settings saved. Mode=%s", settings.EffectiveChatMode()))
	return nil
}

// TestProxyEntries tests the current form without saving or exposing credentials.
func (a *App) TestProxyEntries(raw string) ([]proxy.TestResult, error) {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 65*time.Second)
	defer cancel()
	return proxy.TestList(ctx, raw, "https://app.chitchat.gg/")
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
	// First=... is the normalized Show Mode first-message mode, so a log reader
	// can confirm what the next chat will actually use.
	a.log("info", fmt.Sprintf(
		"General settings saved. Poll=%ds-%ds Action=%ds-%ds First=%s",
		settings.PollMinSeconds,
		settings.PollMaxSeconds,
		settings.ActionMinSeconds,
		settings.ActionMaxSeconds,
		settings.EffectiveFirstMessageMode(),
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

func (a *App) ClearLoadedMessages() error {
	settings, err := a.uiStore.LoadMessageSettings()
	if err != nil {
		return err
	}
	settings.LoadedSequences = nil
	settings.UseLoadedSequences = false
	if settings.ChatMode == model.ChatModeFolder {
		settings.ChatMode = model.ChatModeSingle
	}
	return a.uiStore.SaveMessageSettings(settings)
}

func (a *App) LoadMessageFolder() ([]string, error) {
	dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Select Folder with Chat Patterns",
	})
	if err != nil {
		return nil, err
	}
	if dir == "" {
		return nil, nil
	}

	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var sequences []string
	for _, f := range files {
		if !f.IsDir() && strings.HasSuffix(strings.ToLower(f.Name()), ".txt") {
			content, err := os.ReadFile(filepath.Join(dir, f.Name()))
			if err != nil {
				continue
			}
			seq := strings.TrimSpace(string(content))
			if seq != "" {
				sequences = append(sequences, seq)
			}
		}
	}

	if len(sequences) == 0 {
		return nil, errors.New("no valid .txt files found in the selected folder")
	}

	settings, err := a.uiStore.LoadMessageSettings()
	if err != nil {
		return nil, err
	}

	settings.ChatMode = model.ChatModeFolder
	settings.LoadedSequences = sequences
	settings.UseLoadedSequences = true
	if err := a.uiStore.SaveMessageSettings(settings); err != nil {
		return nil, err
	}

	return sequences, nil
}

// OpenEVAChatDataFolder bootstraps the editable EVAChat data pack, validates
// it, and opens the local folder. It never imports arbitrary keyword files.
func (a *App) OpenEVAChatDataFolder() (string, error) {
	dataRoot, err := bot.EnsureEVAChatData(a.dataDir)
	if err != nil {
		return "", err
	}
	engine, err := bot.LoadEVAChatEngine(dataRoot)
	if err != nil {
		return "", err
	}
	if err := exec.Command("explorer.exe", dataRoot).Start(); err != nil {
		return "", err
	}
	summary := fmt.Sprintf("EVAChat data folder ready: %s (%d trigger categories, %d reply pools)", dataRoot, len(engine.InputCategories()), len(engine.OutputPools()))
	a.log("info", summary)
	return summary, nil
}

// TestEVAChatAI sends one small request to the configured AI API (unsaved
// dashboard values are used) and returns the model's answer.
func (a *App) TestEVAChatAI(settings model.MessageSettings) (string, error) {
	settings.AIEnabled = true
	dataRoot, err := bot.EnsureEVAChatData(a.dataDir)
	if err != nil {
		return "", err
	}
	ai, err := bot.LoadEVAAI(dataRoot, settings)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	answer, err := ai.Test(ctx)
	if err != nil {
		a.log("error", fmt.Sprintf("EVAChat AI test failed: %v", err))
		return "", err
	}
	summary := fmt.Sprintf("AI OK (%s / %s) -> %q", ai.BaseURL, ai.Model, answer)
	a.log("info", summary)
	return summary, nil
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

	var evaChatConfig *bot.EVAChatConfig
	if messageSettings.EffectiveChatMode() == model.ChatModeEVAChat {
		dataRoot, err := bot.EnsureEVAChatData(a.dataDir)
		if err != nil {
			return nil, err
		}
		engine, err := bot.LoadEVAChatEngine(dataRoot)
		if err != nil {
			return nil, err
		}
		ai, err := bot.LoadEVAAI(dataRoot, messageSettings)
		if err != nil {
			return nil, err
		}
		evaChatConfig = &bot.EVAChatConfig{DataRoot: dataRoot, Engine: engine, AI: ai}
		a.log("info", fmt.Sprintf("EVAChat Flow ready: %d trigger categories and %d reply pools loaded from %s. %s", len(engine.InputCategories()), len(engine.OutputPools()), dataRoot, ai.Describe()))
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
		cfg.ChatMode = messageSettings.EffectiveChatMode()
		if cfg.ChatMode == model.ChatModeEVAChat {
			cfg.EVAChat = evaChatConfig
			// EVAChat has its own persisted stage state. Keep MessageSequence
			// empty so sequence code cannot accidentally drive this mode.
			cfg.MessageSequence = nil
			cfg.AvailableSequences = nil
		} else if cfg.ChatMode == model.ChatModeFolder && len(messageSettings.LoadedSequences) > 0 {
			cfg.AvailableSequences = make([][]string, len(messageSettings.LoadedSequences))
			for i, seqStr := range messageSettings.LoadedSequences {
				cfg.AvailableSequences[i] = model.MessageSequenceLines(seqStr)
			}
		} else {
			cfg.AvailableSequences = [][]string{messageSettings.SequenceLines()}
		}
		cfg.MessageUsernames = messageSettings.UsernameLines()
		cfg.MatchWhenIdle = true
		cfg.PollMin = time.Duration(generalSettings.PollMinSeconds) * time.Second
		cfg.PollMax = time.Duration(generalSettings.PollMaxSeconds) * time.Second
		cfg.ActionMin = time.Duration(generalSettings.ActionMinSeconds) * time.Second
		cfg.ActionMax = time.Duration(generalSettings.ActionMaxSeconds) * time.Second
		cfg.MatchReplyTimeoutMin = time.Duration(generalSettings.MatchReplyTimeoutMinSeconds) * time.Second
		cfg.MatchReplyTimeoutMax = time.Duration(generalSettings.MatchReplyTimeoutMaxSeconds) * time.Second
		cfg.MatchCloseWaitMin = time.Duration(generalSettings.MatchCloseWaitMinSeconds) * time.Second
		cfg.MatchCloseWaitMax = time.Duration(generalSettings.MatchCloseWaitMaxSeconds) * time.Second
		cfg.ReplyDelayMin = time.Duration(generalSettings.ReplyDelayMinSeconds) * time.Second
		cfg.ReplyDelayMax = time.Duration(generalSettings.ReplyDelayMaxSeconds) * time.Second

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

func safeProfileName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, "..", "_")
	name = strings.NewReplacer("\\", "_", "/", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_").Replace(name)
	name = strings.Map(func(r rune) rune {
		if r < 32 {
			return '_'
		}
		return r
	}, name)
	// Win32 ignores trailing spaces/dots and reserves device names even when
	// they have an extension (for example CON.txt). Keep profiles unambiguous.
	name = strings.TrimRight(name, ". ")
	if name == "" {
		return "default"
	}
	stem := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	reserved := stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL"
	if len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '1' && stem[3] <= '9' {
		reserved = true
	}
	if reserved {
		name = "_" + name
	}
	return name
}

func (a *App) runFreshBrowser(profileDir string, profileName string, settings model.BrowserSettings, selectedProxy *proxy.Entry, index int, total int) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.runFreshBrowserWithBackend(ctx, profileDir, profileName, settings, selectedProxy, index, total, browser.New)
}

// Production and browser-free tests share the entire capture/save/cleanup path.
// Only backend construction is injected; no global factory or fake production mode.
func (a *App) runFreshBrowserWithBackend(ctx context.Context, profileDir string, profileName string, settings model.BrowserSettings, selectedProxy *proxy.Entry, index int, total int, newBackend func(string) (browser.Backend, error)) {
	// Keep disposable browser data outside the repo so wails dev file watching
	// doesn't restart the app when a fresh browser run creates a new directory.
	browserTempDir, err := os.MkdirTemp("", fmt.Sprintf("chitchat-fresh-browser-%s-%02d-", profileName, index))
	if err != nil {
		a.log("error", fmt.Sprintf("[%s fresh %d/%d] Failed to create temp browser dir: %v", profileName, index, total, err))
		return
	}

	// Register cleanup immediately, including failures before backend creation.
	defer a.removeFreshBrowserData(browserTempDir, profileName, index, total)

	userDataDir := filepath.Join(browserTempDir, "user-data")
	if err := store.EnsureDir(userDataDir); err != nil {
		a.log("error", fmt.Sprintf("[%s fresh %d/%d] Failed to create temp browser dir: %v", profileName, index, total, err))
		return
	}

	backend, err := newBackend(settings.Backend)
	if err != nil {
		a.log("error", fmt.Sprintf("[%s fresh %d/%d] Failed to create browser backend: %v", profileName, index, total, err))
		return
	}
	// LIFO defers close the browser before removing its files.
	defer func() { _ = backend.Close() }()

	a.log("info", fmt.Sprintf(
		"[%s fresh %d/%d] Opening brand-new %s browser. Old profile data is not used.",
		profileName,
		index,
		total,
		settings.Backend,
	))
	if selectedProxy != nil {
		a.log("info", fmt.Sprintf("[%s fresh %d/%d] Using proxy %s", profileName, index, total, selectedProxy.Label()))
	}

	if err := backend.Start(ctx, browser.Options{
		StartURL:            "https://app.chitchat.gg/",
		UserDataDir:         userDataDir,
		Headless:            settings.Headless,
		DisablePageRecovery: true,
		Proxy:               selectedProxy,
	}); err != nil {
		a.log("error", fmt.Sprintf("[%s fresh %d/%d] Browser start failed: %v", profileName, index, total, err))
		return
	}

	login, err := a.captureFreshLogin(ctx, backend, settings.Backend, profileName, index, total)
	if err != nil {
		if errors.Is(err, browser.ErrPageUnavailable) || errors.Is(err, context.Canceled) {
			a.log("info", fmt.Sprintf("[%s fresh %d/%d] Fresh browser was closed before login was saved.", profileName, index, total))
			return
		}
		a.log("error", fmt.Sprintf("[%s fresh %d/%d] Fresh login capture failed: %v", profileName, index, total, err))
		return
	}

	login.Profile = profileName
	login.Backend = settings.Backend
	login.UserID = login.CurrentUserID()
	login.SavedAt = time.Now().UTC()
	loginPath := a.freshLoginInfoPath(profileDir, login, index)

	if err := store.SaveJSON(loginPath, login); err != nil {
		a.log("error", fmt.Sprintf("[%s fresh %d/%d] Failed to save login info: %v", profileName, index, total, err))
		return
	}

	a.log("info", fmt.Sprintf(
		"[%s fresh %d/%d] Login saved to %s. Temporary browser data will be removed after close.",
		profileName,
		index,
		total,
		loginPath,
	))

	// Show Mode: the browser stays open and chats in place. This blocks until
	// the user closes the browser window or the run is cancelled; the deferred
	// backend.Close above then performs the usual cleanup.
	if settings.LiveChat {
		a.runLiveChat(ctx, backend, login, profileName, index, total)
	}
}

func (a *App) captureFreshLogin(
	ctx context.Context,
	backend browser.Backend,
	backendName string,
	profileName string,
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
				"[%s fresh %d/%d] Waiting for login token in the new %s browser window.",
				profileName,
				index,
				total,
				backendName,
			))
			waitingForTokenLogged = true
		}

		if token != "" && !waitingForSessionLogged {
			a.log("info", fmt.Sprintf(
				"[%s fresh %d/%d] Token detected, but the session is still unauthorized. Finish the full login in the %s browser window.",
				profileName,
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

func (a *App) removeFreshBrowserData(tempDir string, profileName string, index int, total int) {
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		if err := os.RemoveAll(tempDir); err == nil {
			a.log("info", fmt.Sprintf(
				"[%s fresh %d/%d] Removed temporary browser data from %s",
				profileName,
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
			"[%s fresh %d/%d] Could not remove temporary browser data from %s: %v",
			profileName,
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
	// Persist first, independently of Badger/UI events; no Home visibility filter.
	disk := applog.Logger{Path: applog.Path(a.dataDir), Source: "app"}
	disk.Log(level, message)
	entry, err := a.uiStore.AppendLog(level, message)
	if err == nil {
		a.emit("ui:log", entry) // Browser status still consumes this event.
	} else {
		disk.Error(fmt.Sprintf("UI log persistence failed: %v", err))
	}
}

// Separate read-only display API; settings/stats/logs contracts are unchanged.
func (a *App) GetLiveChatSnapshot(sessionID string) homeChatSnapshot {
	return a.homeChatFeed().snapshot(sessionID)
}
