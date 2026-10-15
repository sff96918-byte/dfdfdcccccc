package uistore

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	badger "github.com/dgraph-io/badger/v4"

	"chitchatbot/internal/model"
	"chitchatbot/internal/store"
)

const (
	keyBrowser = "settings/browser"
	keyMessage = "settings/message"
	keyProxy   = "settings/proxy"
	keyGeneral = "settings/general"
	logPrefix  = "logs/"
)

type Store struct {
	db *badger.DB
}

func Open(baseDir string) (*Store, error) {
	dbDir := filepath.Join(baseDir, "ui", "badger")
	if err := store.EnsureDir(dbDir); err != nil {
		return nil, err
	}

	opts := badger.DefaultOptions(dbDir)
	opts.Logger = nil
	opts.ValueLogFileSize = 64 << 20

	db, err := badger.Open(opts)
	if err != nil {
		return nil, err
	}

	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) LoadBrowserSettings() (model.BrowserSettings, error) {
	settings := model.DefaultBrowserSettings()
	ok, err := s.loadJSON(keyBrowser, &settings)
	if err != nil || !ok {
		return settings, err
	}
	if settings.Profile == "" {
		settings.Profile = "default"
	}
	if settings.Backend != "rod" && settings.Backend != "chromedp" {
		settings.Backend = "rod"
	}
	return settings, nil
}

func (s *Store) SaveBrowserSettings(settings model.BrowserSettings) error {
	if settings.Profile == "" {
		settings.Profile = "default"
	}
	if settings.Backend != "rod" && settings.Backend != "chromedp" {
		settings.Backend = "rod"
	}
	return s.saveJSON(keyBrowser, settings)
}

func (s *Store) LoadMessageSettings() (model.MessageSettings, error) {
	settings := model.DefaultMessageSettings()
	ok, err := s.loadJSON(keyMessage, &settings)
	if err != nil || !ok {
		return settings, err
	}

	if model.NormalizeMessageSequence(settings.Sequence) == "" {
		settings.Sequence = model.LegacyMessageSequence(settings.FirstMessage, settings.ReplyMessage)
	}
	if model.NormalizeMessageSequence(settings.Sequence) == "" {
		return model.DefaultMessageSettings(), nil
	}
	ret := model.NewMessageSettings(settings.Sequence, settings.Usernames)
	mode := settings.EffectiveChatMode()
	ret.ChatMode = mode
	ret.LoadedSequences = settings.LoadedSequences
	ret.UseLoadedSequences = (mode == model.ChatModeFolder)
	ret.KeywordRulesContent = settings.KeywordRulesContent
	ret.KeywordFilePath = settings.KeywordFilePath
	ret.AIEnabled = settings.AIEnabled
	ret.AIBaseURL = settings.AIBaseURL
	ret.AIAPIKey = settings.AIAPIKey
	ret.AIModel = settings.AIModel
	return ret, nil
}

func (s *Store) SaveMessageSettings(settings model.MessageSettings) error {
	sequence := model.NormalizeMessageSequence(settings.Sequence)
	if sequence == "" {
		sequence = model.LegacyMessageSequence(settings.FirstMessage, settings.ReplyMessage)
	}
	if sequence == "" {
		sequence = model.DefaultMessageSettings().Sequence
	}
	ret := model.NewMessageSettings(sequence, settings.Usernames)
	mode := settings.EffectiveChatMode()
	ret.ChatMode = mode
	ret.LoadedSequences = settings.LoadedSequences
	ret.UseLoadedSequences = (mode == model.ChatModeFolder)
	ret.KeywordRulesContent = settings.KeywordRulesContent
	ret.KeywordFilePath = settings.KeywordFilePath
	ret.AIEnabled = settings.AIEnabled
	ret.AIBaseURL = settings.AIBaseURL
	ret.AIAPIKey = settings.AIAPIKey
	ret.AIModel = settings.AIModel
	return s.saveJSON(keyMessage, ret)
}

func (s *Store) LoadProxySettings() (model.ProxySettings, error) {
	settings := model.DefaultProxySettings()
	ok, err := s.loadJSON(keyProxy, &settings)
	if err != nil || !ok {
		return settings, err
	}
	if strings.TrimSpace(settings.Entries) == "" && strings.TrimSpace(settings.Address) != "" {
		address := strings.TrimSpace(settings.Address)
		address = strings.TrimPrefix(address, "http://")
		address = strings.TrimPrefix(address, "https://")

		if strings.TrimSpace(settings.Username) != "" || strings.TrimSpace(settings.Password) != "" {
			address = address + ":" + strings.TrimSpace(settings.Username) + ":" + strings.TrimSpace(settings.Password)
		}

		settings.Entries = address
	}
	return settings, nil
}

func (s *Store) SaveProxySettings(settings model.ProxySettings) error {
	settings.Address = ""
	settings.Username = ""
	settings.Password = ""
	return s.saveJSON(keyProxy, settings)
}

func (s *Store) LoadGeneralSettings() (model.GeneralSettings, error) {
	settings := model.DefaultGeneralSettings()
	ok, err := s.loadJSON(keyGeneral, &settings)
	if err != nil || !ok {
		return settings, err
	}
	if settings.PollMinSeconds <= 0 {
		settings.PollMinSeconds = model.DefaultGeneralSettings().PollMinSeconds
	}
	if settings.PollMaxSeconds < settings.PollMinSeconds {
		settings.PollMaxSeconds = settings.PollMinSeconds
	}
	if settings.ActionMinSeconds <= 0 {
		settings.ActionMinSeconds = model.DefaultGeneralSettings().ActionMinSeconds
	}
	if settings.ActionMaxSeconds < settings.ActionMinSeconds {
		settings.ActionMaxSeconds = settings.ActionMinSeconds
	}
	if settings.MatchReplyTimeoutMinSeconds <= 0 {
		settings.MatchReplyTimeoutMinSeconds = model.DefaultGeneralSettings().MatchReplyTimeoutMinSeconds
	}
	if settings.MatchReplyTimeoutMaxSeconds < settings.MatchReplyTimeoutMinSeconds {
		settings.MatchReplyTimeoutMaxSeconds = settings.MatchReplyTimeoutMinSeconds
	}
	if settings.MatchCloseWaitMaxSeconds < settings.MatchCloseWaitMinSeconds {
		settings.MatchCloseWaitMaxSeconds = settings.MatchCloseWaitMinSeconds
	}
	if settings.ReplyDelayMaxSeconds < settings.ReplyDelayMinSeconds {
		settings.ReplyDelayMaxSeconds = settings.ReplyDelayMinSeconds
	}
	return settings, nil
}

func (s *Store) SaveGeneralSettings(settings model.GeneralSettings) error {
	if settings.PollMinSeconds <= 0 {
		settings.PollMinSeconds = model.DefaultGeneralSettings().PollMinSeconds
	}
	if settings.PollMaxSeconds < settings.PollMinSeconds {
		settings.PollMaxSeconds = settings.PollMinSeconds
	}
	if settings.ActionMinSeconds <= 0 {
		settings.ActionMinSeconds = model.DefaultGeneralSettings().ActionMinSeconds
	}
	if settings.ActionMaxSeconds < settings.ActionMinSeconds {
		settings.ActionMaxSeconds = settings.ActionMinSeconds
	}
	if settings.MatchReplyTimeoutMinSeconds <= 0 {
		settings.MatchReplyTimeoutMinSeconds = model.DefaultGeneralSettings().MatchReplyTimeoutMinSeconds
	}
	if settings.MatchReplyTimeoutMaxSeconds < settings.MatchReplyTimeoutMinSeconds {
		settings.MatchReplyTimeoutMaxSeconds = settings.MatchReplyTimeoutMinSeconds
	}
	if settings.MatchCloseWaitMaxSeconds < settings.MatchCloseWaitMinSeconds {
		settings.MatchCloseWaitMaxSeconds = settings.MatchCloseWaitMinSeconds
	}
	if settings.ReplyDelayMaxSeconds < settings.ReplyDelayMinSeconds {
		settings.ReplyDelayMaxSeconds = settings.ReplyDelayMinSeconds
	}
	return s.saveJSON(keyGeneral, settings)
}

func (s *Store) AppendLog(level string, message string) (model.LogEntry, error) {
	entry := model.LogEntry{
		Time:    time.Now().UTC().Format(time.RFC3339),
		Level:   level,
		Message: message,
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return model.LogEntry{}, err
	}

	key := []byte(fmt.Sprintf("%s%020d", logPrefix, time.Now().UTC().UnixNano()))
	err = s.db.Update(func(txn *badger.Txn) error {
		return txn.Set(key, data)
	})
	if err != nil {
		return model.LogEntry{}, err
	}

	return entry, nil
}

func (s *Store) ListLogs(limit int) ([]model.LogEntry, error) {
	if limit <= 0 {
		limit = 200
	}

	result := make([]model.LogEntry, 0, limit)
	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte(logPrefix)

		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			err := item.Value(func(val []byte) error {
				var entry model.LogEntry
				if err := json.Unmarshal(val, &entry); err != nil {
					return err
				}
				result = append(result, entry)
				return nil
			})
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	if len(result) > limit {
		result = result[len(result)-limit:]
	}

	return result, nil
}

func (s *Store) ClearLogs() error {
	return s.db.Update(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte(logPrefix)

		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Rewind(); it.Valid(); it.Next() {
			if err := txn.Delete(it.Item().KeyCopy(nil)); err != nil {
				return err
			}
		}

		return nil
	})
}

func (s *Store) saveJSON(key string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}

	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte(key), data)
	})
}

func (s *Store) loadJSON(key string, dest any) (bool, error) {
	var data []byte
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(key))
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			data = append([]byte(nil), val...)
			return nil
		})
	})
	if err != nil {
		if err == badger.ErrKeyNotFound {
			return false, nil
		}
		return false, err
	}

	return true, json.Unmarshal(data, dest)
}
