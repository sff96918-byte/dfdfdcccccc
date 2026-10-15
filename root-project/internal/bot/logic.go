package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"chitchatbot/internal/api"
	"chitchatbot/internal/model"
	"chitchatbot/internal/store"
)

func fetchRelationships(ctx context.Context, client *api.Client, cfg Config) ([]model.Relationship, int, error) {
	resp, err := client.Fetch(ctx, api.FetchRequest{
		Method: "GET",
		URL:    cfg.APIBaseURL + "/users/me/relationships",
		Headers: map[string]string{
			"accept": "*/*",
		},
	})
	if err != nil {
		return nil, 0, err
	}

	if resp.Status >= 400 {
		logf(cfg, "GET relationships failed: %d %s\n", resp.Status, resp.Text)
		return nil, resp.Status, nil
	}

	var relationships []model.Relationship
	if err := json.Unmarshal([]byte(resp.Text), &relationships); err != nil {
		return nil, resp.Status, err
	}

	return relationships, resp.Status, nil
}

func fetchDMConversations(ctx context.Context, client *api.Client, cfg Config) ([]model.DMConversation, int, error) {
	url := fmt.Sprintf("%s/users/me/conversations/dm?limit=%d&offset=%d", cfg.APIBaseURL, cfg.DMLimit, normalizeDMConversationOffset(cfg.DMOffset))
	resp, err := client.Fetch(ctx, api.FetchRequest{
		Method: "GET",
		URL:    url,
		Headers: map[string]string{
			"accept": "*/*",
		},
	})
	if err != nil {
		return nil, 0, err
	}

	if resp.Status >= 400 {
		logf(cfg, "GET dm conversations failed: %d %s\n", resp.Status, resp.Text)
		return nil, resp.Status, nil
	}

	var conversations []model.DMConversation
	if err := json.Unmarshal([]byte(resp.Text), &conversations); err != nil {
		return nil, resp.Status, err
	}

	return conversations, resp.Status, nil
}

func fetchConversationMessages(ctx context.Context, client *api.Client, cfg Config, conversationID string) ([]model.Message, int, error) {
	url := fmt.Sprintf("%s/users/me/conversations/%s/messages?limit=%d&offset=0", cfg.APIBaseURL, conversationID, cfg.MessageFetchLimit)
	resp, err := client.Fetch(ctx, api.FetchRequest{
		Method: "GET",
		URL:    url,
		Headers: map[string]string{
			"accept": "*/*",
		},
	})
	if err != nil {
		return nil, 0, err
	}

	if resp.Status >= 400 {
		logf(cfg, "GET messages failed for %s: %d %s\n", conversationID, resp.Status, resp.Text)
		return nil, resp.Status, nil
	}

	var messages []model.Message
	if err := json.Unmarshal([]byte(resp.Text), &messages); err != nil {
		return nil, resp.Status, err
	}

	return messages, resp.Status, nil
}

func acceptFriend(ctx context.Context, client *api.Client, cfg Config, userID string) error {
	body := fmt.Sprintf(`{"action":%d}`, cfg.ActionAcceptFriend)
	resp, err := client.Fetch(ctx, api.FetchRequest{
		Method: "PUT",
		URL:    fmt.Sprintf("%s/users/me/relationships/%s", cfg.APIBaseURL, userID),
		Headers: map[string]string{
			"accept":       "*/*",
			"content-type": "application/json",
		},
		Body: &body,
	})
	if err != nil {
		return err
	}

	logf(cfg, "PUT relationship %s -> %d\n", userID, resp.Status)
	if resp.Status >= 400 {
		logf(cfg, "Server error: %s\n", resp.Text)
	}
	return nil
}

func sendDMMessage(
	ctx context.Context,
	client *api.Client,
	cfg Config,
	login model.LoginInfo,
	conversationID string,
	message string,
) (int, string, error) {
	if typingStatus, typingText, typingErr := sendTypingIndicator(ctx, client, cfg, conversationID); typingErr != nil {
		logf(cfg, "POST typing failed for %s: %v\n", conversationID, typingErr)
	} else if typingStatus >= 400 {
		logf(cfg, "POST typing failed for %s: %d %s\n", conversationID, typingStatus, typingText)
	}

	nonce := generateNonce(cfg.NonceAlphabet, 21)
	contentType, body := buildMultipartBody(map[string]string{
		"content": message,
		"nonce":   nonce,
	})

	bodyText := body.String()
	resp, err := client.Fetch(ctx, api.FetchRequest{
		Method: "POST",
		URL:    fmt.Sprintf("%s/users/me/conversations/%s/messages", cfg.APIBaseURL, conversationID),
		Headers: map[string]string{
			"accept":             "*/*",
			"content-type":       contentType,
			"transport-encoding": generateTransportEncoding(cfg.TransportKey, login.TransportFingerprint),
		},
		Body: &bodyText,
	})
	if err != nil {
		return 0, "", err
	}

	return resp.Status, resp.Text, nil
}

func sendTypingIndicator(
	ctx context.Context,
	client *api.Client,
	cfg Config,
	conversationID string,
) (int, string, error) {
	body := "{}"
	resp, err := client.Fetch(ctx, api.FetchRequest{
		Method: "POST",
		URL:    fmt.Sprintf("%s/users/me/conversations/%s/typing", cfg.APIBaseURL, conversationID),
		Headers: map[string]string{
			"accept":       "*/*",
			"content-type": "application/json",
		},
		Body: &body,
	})
	if err != nil {
		return 0, "", err
	}

	return resp.Status, resp.Text, nil
}

func trackBotSendStatus(cfg Config, currentUserID string, status int) error {
	markedDead, err := recordBotSendStatus(cfg, currentUserID, status)
	if err != nil {
		return err
	}
	if markedDead {
		logf(cfg, "Bot account %s marked dead after %d consecutive failed send attempts.\n", currentUserID, deadSendFailureThreshold)
	}
	return nil
}

func extractPendingIncomingUserIDs(relationships []model.Relationship, cfg Config) []string {
	userIDs := make([]string, 0)
	friends := 0
	pendingOut := 0
	blocked := 0
	other := 0

	for _, relationship := range relationships {
		switch relationship.Status {
		case cfg.StatusPendingIn:
			if relationship.User.ID != "" {
				userIDs = append(userIDs, relationship.User.ID)
			}
		case cfg.StatusFriend:
			friends++
		case cfg.StatusPendingOut:
			pendingOut++
		case cfg.StatusBlocked:
			blocked++
		default:
			other++
		}
	}

	userIDs = uniqueStrings(userIDs)
	logf(cfg,
		"Relationship summary: pending=%d friends=%d sent=%d blocked=%d other=%d\n",
		len(userIDs), friends, pendingOut, blocked, other,
	)
	return userIDs
}

func extractDMUserIDs(conversations []model.DMConversation, currentUserID string) []string {
	ids := make([]string, 0)
	for _, conversation := range conversations {
		for _, participant := range conversation.Participants {
			userID := participant.Profile.ID
			if userID == "" || userID == currentUserID {
				continue
			}
			ids = append(ids, userID)
		}
	}
	return uniqueStrings(ids)
}

func extractDMTargets(conversations []model.DMConversation, currentUserID string) []model.DMTarget {
	targets := make([]model.DMTarget, 0)
	seen := make(map[string]bool)

	for _, conversation := range conversations {
		if seen[conversation.ID] {
			continue
		}
		botUsername := ""
		target := model.DMTarget{}

		for _, participant := range conversation.Participants {
			userID := participant.Profile.ID
			if userID == "" {
				continue
			}

			if userID == currentUserID {
				botUsername = participant.Profile.Username
				continue
			}

			if target.UserID == "" {
				target = model.DMTarget{
					ConversationID: conversation.ID,
					UserID:         userID,
					Username:       participant.Profile.Username,
					UnreadCount:    conversation.UnreadMessageCount,
					UpdatedAt:      conversation.UpdatedAt,
				}
			}
		}

		if target.UserID != "" {
			target.BotUsername = botUsername
			targets = append(targets, target)
			seen[conversation.ID] = true
		}
	}

	return targets
}

func saveDerivedFiles(p paths, pendingIDs []string, dmUserIDs []string, targets []model.DMTarget) error {
	if err := saveRelationshipIDs(p.RelationshipIDs, pendingIDs); err != nil {
		return err
	}
	return saveDMFiles(p, pendingIDs, dmUserIDs, targets)
}

func saveRelationshipIDs(path string, pendingIDs []string) error {
	return store.SaveJSON(path, pendingIDs)
}

func saveDMFiles(p paths, pendingIDs []string, dmUserIDs []string, targets []model.DMTarget) error {
	allUserIDs := uniqueStrings(append(append([]string{}, pendingIDs...), dmUserIDs...))

	if err := store.SaveJSON(p.DMUserIDs, dmUserIDs); err != nil {
		return err
	}
	if err := store.SaveJSON(p.DMConversations, targets); err != nil {
		return err
	}
	if err := store.SaveJSON(p.AllUserIDs, allUserIDs); err != nil {
		return err
	}

	return nil
}

func processDMAutoResponses(
	ctx context.Context,
	client *api.Client,
	cfg Config,
	p paths,
	login model.LoginInfo,
	currentUserID string,
	targets []model.DMTarget,
	rng *rand.Rand,
) (bool, error) {
	if len(targets) == 0 {
		logln(cfg, "No DM conversations right now.")
		return false, nil
	}

	workDone := false
	state, accountKey, accountState, err := loadBotAccountState(p.BotState, currentUserID)
	if err != nil {
		return false, err
	}

	logf(cfg, "Checking %d DM conversations.\n", len(targets))

	for index, target := range targets {
		sentCount := accountState.SentMessageCounts[target.UserID]
		hasPendingScriptMessage := sentCount < len(cfg.MessageSequence)
		if evaChatEnabled(cfg) {
			session := evaChatSession(&accountState, target.UserID)
			hasPendingScriptMessage = !session.Started && !session.Ended
		}
		needsFirstMessage := sentCount == 0 && hasPendingScriptMessage
		needsBaseline := !accountState.BaselineDone[target.UserID]
		needsReplyCheck := target.UnreadCount > 0

		if !needsFirstMessage && !needsBaseline && !needsReplyCheck {
			continue
		}

		messages, status, err := fetchConversationMessages(ctx, client, cfg, target.ConversationID)
		if err != nil {
			return workDone, err
		}
		if status == 401 {
			return workDone, errLoginExpired
		}
		if status == 403 {
			logf(cfg, "Access to conversation %s with user %s is forbidden. The bot may have been blocked or removed. Skipping.\n", target.ConversationID, target.Username)
			continue
		}
		if messages == nil {
			continue
		}

		currentIncoming := incomingMessages(messages, currentUserID)

		if needsFirstMessage {
			var messageText string
			var hasUsername bool
			if evaChatEnabled(cfg) {
				result := evaChatStart(cfg, &accountState, target.UserID, rng)
				messageText = result.Text
				hasUsername = result.HasUsername
				logf(cfg, "  -> [evachat] opener from %s (%s)\n", result.SourcePool, result.Reason)
			} else if cfg.ChatMode == model.ChatModeKeyword && len(cfg.KeywordRules) > 0 {
				assignedUsername := getOrAssignUsername(cfg, &accountState, target.UserID, rng)
				messageText = RenderKeywordReply(cfg.KeywordRules[0].ReplyTemplate, rng, assignedUsername)
				hasUsername = strings.Contains(cfg.KeywordRules[0].ReplyTemplate, "%username%")
			} else {
				messageText, hasUsername = renderMsg(cfg, &accountState, target.UserID, sentCount, rng, cfg.MessageSequence[sentCount])
			}
			if messageText == "" {
				logf(cfg, "[%d/%d] First message template rendered empty for %s. Skipping send.\n", index+1, len(targets), target.Username)
				accountState.BaselineDone[target.UserID] = true
				markSeen(accountState.IncomingMessages, target.ConversationID, currentIncoming, false)
				state[accountKey] = accountState
				if err := store.SaveJSON(p.BotState, state); err != nil {
					return workDone, err
				}
				continue
			}

			logf(cfg, "[%d/%d] New conversation with %s. Sending first message.\n", index+1, len(targets), target.Username)
			status, _, err := sendDMMessage(ctx, client, cfg, login, target.ConversationID, messageText)
			if err != nil {
				return workDone, err
			}
			// logf(cfg, "  -> POST %d %s\n", status, text)
			if err := trackBotSendStatus(cfg, currentUserID, status); err != nil {
				return workDone, err
			}

			if status < 400 {
				workDone = true
				if hasUsername {
					accountState.SnapTgSentCount++
				}
				accountState.FirstMessagesSent[target.UserID] = model.FirstMessage{
					UserID:   target.UserID,
					Username: target.Username,
					Message:  messageText,
					SentAt:   time.Now().UTC(),
				}
				accountState.SentMessageCounts[target.UserID] = sentCount + 1
				accountState.BaselineDone[target.UserID] = true
				markSeen(accountState.IncomingMessages, target.ConversationID, currentIncoming, false)
				state[accountKey] = accountState
				if err := store.SaveJSON(p.BotState, state); err != nil {
					return workDone, err
				}
				logSendActivity(cfg, currentUserID, target, messageText)
			}

			if err := sleepRandom(ctx, rng, cfg, cfg.ActionMin, cfg.ActionMax, "Waiting before next DM action"); err != nil {
				return workDone, err
			}
			continue
		}

		if needsBaseline {
			count := markSeen(accountState.IncomingMessages, target.ConversationID, currentIncoming, false)
			accountState.BaselineDone[target.UserID] = true
			state[accountKey] = accountState
			if err := store.SaveJSON(p.BotState, state); err != nil {
				return workDone, err
			}
			logf(cfg, "[%d/%d] Baseline saved %d incoming messages for %s.\n", index+1, len(targets), count, target.Username)
			continue
		}

		newIncoming := make([]model.Message, 0)
		for _, message := range currentIncoming {
			if _, seen := accountState.IncomingMessages[message.ID]; !seen {
				newIncoming = append(newIncoming, message)
			}
		}

		if len(newIncoming) == 0 {
			continue
		}

		logf(cfg, "[%d/%d] Found %d new incoming messages from %s.\n", index+1, len(targets), len(newIncoming), target.Username)

		for replyIndex, message := range newIncoming {
			var messageText string
			var hasUsername bool

			var evaExtraLines []string
			if evaChatEnabled(cfg) {
				result := evaChatReply(cfg, &accountState, target.UserID, message.Content, rng)
				messageText = result.Text
				hasUsername = result.HasUsername
				evaExtraLines = result.Extra
				logf(cfg, "  -> [evachat] reply from %s (%s)\n", result.SourcePool, result.Reason)
				if messageText == "" {
					markSeen(accountState.IncomingMessages, target.ConversationID, []model.Message{message}, false)
					state[accountKey] = accountState
					if err := store.SaveJSON(p.BotState, state); err != nil {
						return workDone, err
					}
					continue
				}
			} else if cfg.ChatMode == model.ChatModeKeyword && len(cfg.KeywordRules) > 0 {
				matchedRule, _ := FindMatchingKeywordRule(message.Content, cfg.KeywordRules, nil)
				if matchedRule == nil {
					_ = RecordUnmatchedKeyword(cfg.DataDir, target.Username, target.UserID, message.Content)
					logf(cfg, "  -> [keyword] Unmatched incoming message %q from %s. Saved to unmatched_keywords.txt. Skipping reply.\n", message.Content, target.Username)
					markSeen(accountState.IncomingMessages, target.ConversationID, []model.Message{message}, false)
					state[accountKey] = accountState
					_ = store.SaveJSON(p.BotState, state)
					continue
				}
				assignedUsername := getOrAssignUsername(cfg, &accountState, target.UserID, rng)
				messageText = RenderKeywordReply(matchedRule.ReplyTemplate, rng, assignedUsername)
				hasUsername = strings.Contains(matchedRule.ReplyTemplate, "%username%")
			} else {
				activeSequence := getActiveSequence(cfg, &accountState, target.UserID, sentCount, rng)
				if sentCount >= len(activeSequence) {
					remaining := newIncoming[replyIndex:]
					markSeen(accountState.IncomingMessages, target.ConversationID, remaining, false)
					state[accountKey] = accountState
					if err := store.SaveJSON(p.BotState, state); err != nil {
						return workDone, err
					}
					for _, incoming := range remaining {
						logReceiveActivity(cfg, currentUserID, target, incoming.Content)
					}
					logf(cfg, "  -> Message sequence exhausted for %s. Marked %d incoming messages as seen without reply.\n", target.Username, len(remaining))
					break
				}

				messageText, hasUsername = renderMsg(cfg, &accountState, target.UserID, sentCount, rng, activeSequence[sentCount])
				if messageText == "" {
					markSeen(accountState.IncomingMessages, target.ConversationID, []model.Message{message}, false)
					state[accountKey] = accountState
					if err := store.SaveJSON(p.BotState, state); err != nil {
						return workDone, err
					}
					logReceiveActivity(cfg, currentUserID, target, message.Content)
					logf(cfg, "  -> Sequence line %d rendered empty for %s. Skipped reply to %s.\n", sentCount+1, target.Username, message.ID)
					continue
				}
			}

			var replyDelay time.Duration
			if cfg.ReplyDelayMax > cfg.ReplyDelayMin {
				replyDelay = cfg.ReplyDelayMin + time.Duration(rng.Int63n(int64(cfg.ReplyDelayMax-cfg.ReplyDelayMin)))
			} else {
				replyDelay = cfg.ReplyDelayMin
			}

			if replyDelay > 0 {
				logf(cfg, "  -> Delaying %.0f seconds before sending reply %d/%d to %s\n", replyDelay.Seconds(), replyIndex+1, len(newIncoming), target.Username)
				select {
				case <-ctx.Done():
					return workDone, ctx.Err()
				case <-time.After(replyDelay):
				}
			}

			logf(cfg, "  -> Reply %d/%d to message %s\n", replyIndex+1, len(newIncoming), message.ID)
			status, _, err := sendDMMessage(ctx, client, cfg, login, target.ConversationID, messageText)
			if err != nil {
				return workDone, err
			}
			// logf(cfg, "  -> POST %d %s\n", status, text)
			if err := trackBotSendStatus(cfg, currentUserID, status); err != nil {
				return workDone, err
			}

			if status < 400 {
				workDone = true
				if hasUsername {
					accountState.SnapTgSentCount++
				}
				if err := sendEVAExtraLines(ctx, client, cfg, login, target.ConversationID, evaExtraLines, rng); err != nil {
					return workDone, err
				}
				markSeen(accountState.IncomingMessages, target.ConversationID, []model.Message{message}, true)
				sentCount++
				accountState.SentMessageCounts[target.UserID] = sentCount
				state[accountKey] = accountState
				if err := store.SaveJSON(p.BotState, state); err != nil {
					return workDone, err
				}
				logReceiveActivity(cfg, currentUserID, target, message.Content)
				logSendActivity(cfg, currentUserID, target, messageText)
				if err := sleepRandom(ctx, rng, cfg, cfg.ActionMin, cfg.ActionMax, "Waiting before next reply"); err != nil {
					return workDone, err
				}
			} else if status == 403 || status == 400 {
				break // Conversation is likely closed, don't try to send more replies
			}
		}
	}

	return workDone, nil
}

func loadBotAccountState(path string, currentUserID string) (model.BotState, string, model.AccountState, error) {
	var state model.BotState
	ok, err := store.LoadJSON(path, &state)
	if err != nil {
		return nil, "", model.AccountState{}, err
	}
	if !ok || state == nil {
		state = model.BotState{}
	}

	accountKey := currentUserID
	if strings.TrimSpace(accountKey) == "" {
		accountKey = "unknown-account"
	}

	accountState := state[accountKey]
	normalizeBotAccountState(&accountState)
	return state, accountKey, accountState, nil
}

func normalizeBotAccountState(accountState *model.AccountState) {
	if accountState.FirstMessagesSent == nil {
		accountState.FirstMessagesSent = map[string]model.FirstMessage{}
	}
	if accountState.SentMessageCounts == nil {
		accountState.SentMessageCounts = map[string]int{}
	}
	if accountState.BaselineDone == nil {
		accountState.BaselineDone = map[string]bool{}
	}
	if accountState.IncomingMessages == nil {
		accountState.IncomingMessages = map[string]model.SeenMessage{}
	}
	if accountState.AssignedUsernames == nil {
		accountState.AssignedUsernames = map[string]string{}
	}
	if accountState.EVAChatSessions == nil {
		accountState.EVAChatSessions = map[string]model.EVAChatSession{}
	}
	for conversationID := range accountState.FirstMessagesSent {
		if accountState.SentMessageCounts[conversationID] < 1 {
			accountState.SentMessageCounts[conversationID] = 1
		}
	}
}

func saveBotAccountState(path string, state model.BotState, accountKey string, accountState model.AccountState) error {
	state[accountKey] = accountState
	return store.SaveJSON(path, state)
}

func processRealtimeConversationEvent(
	ctx context.Context,
	client *api.Client,
	cfg Config,
	p paths,
	login model.LoginInfo,
	currentUserID string,
	event realtimeEvent,
	rng *rand.Rand,
) (bool, error) {
	if event.Closed || event.Target.ConversationID == "" || event.Target.UserID == "" {
		return false, nil
	}

	var incoming *model.Message
	if event.Type == "chatMessage" && event.UserID != "" && event.UserID != currentUserID && event.MessageID != "" {
		incoming = &model.Message{
			ID:        event.MessageID,
			Content:   event.Content,
			CreatedAt: event.CreatedAt,
		}
		incoming.Author.ID = event.UserID
	}

	return sendFirstMessageIfNeeded(ctx, client, cfg, p, login, currentUserID, event.Target, incoming, rng)
}

func sendFirstMessageIfNeeded(
	ctx context.Context,
	client *api.Client,
	cfg Config,
	p paths,
	login model.LoginInfo,
	currentUserID string,
	target model.DMTarget,
	incoming *model.Message,
	rng *rand.Rand,
) (bool, error) {
	if target.ConversationID == "" || target.UserID == "" || (!evaChatEnabled(cfg) && len(cfg.MessageSequence) == 0) {
		return false, nil
	}

	state, accountKey, accountState, err := loadBotAccountState(p.BotState, currentUserID)
	if err != nil {
		return false, err
	}

	sentCount := accountState.SentMessageCounts[target.UserID]
	if evaChatEnabled(cfg) {
		session := evaChatSession(&accountState, target.UserID)
		if session.Started || session.Ended {
			return false, nil
		}
	} else if sentCount >= 1 || sentCount >= len(cfg.MessageSequence) {
		return false, nil
	}

	var messageText string
	var hasUsername bool
	if evaChatEnabled(cfg) {
		result := evaChatStart(cfg, &accountState, target.UserID, rng)
		messageText = result.Text
		hasUsername = result.HasUsername
		logf(cfg, "[realtime] EVAChat opener from %s (%s)\n", result.SourcePool, result.Reason)
	} else if cfg.ChatMode == model.ChatModeKeyword && len(cfg.KeywordRules) > 0 {
		assignedUsername := getOrAssignUsername(cfg, &accountState, target.UserID, rng)
		messageText = RenderKeywordReply(cfg.KeywordRules[0].ReplyTemplate, rng, assignedUsername)
		hasUsername = strings.Contains(cfg.KeywordRules[0].ReplyTemplate, "%username%")
	} else {
		messageText, hasUsername = renderMsg(cfg, &accountState, target.UserID, sentCount, rng, cfg.MessageSequence[sentCount])
	}
	if messageText == "" {
		logf(cfg, "[realtime] First message template rendered empty for %s. Skipping immediate send.\n", target.Username)
		if incoming != nil {
			markSeen(accountState.IncomingMessages, target.ConversationID, []model.Message{*incoming}, false)
			accountState.BaselineDone[target.UserID] = true
			if err := saveBotAccountState(p.BotState, state, accountKey, accountState); err != nil {
				return false, err
			}
		}
		return false, nil
	}

	logf(cfg, "[realtime] New conversation with %s. Sending first message immediately.\n", target.Username)
	status, _, err := sendDMMessage(ctx, client, cfg, login, target.ConversationID, messageText)
	if err != nil {
		return false, err
	}
	// logf(cfg, "[realtime] -> POST %d %s\n", status, text)
	if err := trackBotSendStatus(cfg, currentUserID, status); err != nil {
		return false, err
	}
	if status >= 400 {
		return false, nil
	}

	if hasUsername {
		accountState.SnapTgSentCount++
		_ = markUserFlagged(cfg.DataDir, target.UserID)
	}
	accountState.FirstMessagesSent[target.UserID] = model.FirstMessage{
		UserID:   target.UserID,
		Username: target.Username,
		Message:  messageText,
		SentAt:   time.Now().UTC(),
	}
	accountState.SentMessageCounts[target.UserID] = sentCount + 1
	accountState.BaselineDone[target.UserID] = true
	if incoming != nil {
		markSeen(accountState.IncomingMessages, target.ConversationID, []model.Message{*incoming}, false)
		logReceiveActivity(cfg, currentUserID, target, incoming.Content)
	}
	if err := saveBotAccountState(p.BotState, state, accountKey, accountState); err != nil {
		return false, err
	}
	logSendActivity(cfg, currentUserID, target, messageText)
	return true, nil
}

func incomingMessages(messages []model.Message, currentUserID string) []model.Message {
	incoming := make([]model.Message, 0)
	for _, message := range messages {
		if message.ID == "" || message.Author.ID == "" || message.Author.ID == currentUserID {
			continue
		}
		incoming = append(incoming, message)
	}

	sort.Slice(incoming, func(i, j int) bool {
		return incoming[i].CreatedAt < incoming[j].CreatedAt
	})

	return incoming
}

func markSeen(seen map[string]model.SeenMessage, conversationID string, messages []model.Message, replied bool) int {
	marked := 0
	for _, message := range messages {
		if message.ID == "" {
			continue
		}
		if _, exists := seen[message.ID]; !exists {
			marked++
		}
		seen[message.ID] = model.SeenMessage{
			ConversationID: conversationID,
			SeenAt:         time.Now().UTC(),
			Replied:        replied,
			Content:        message.Content,
			CreatedAt:      message.CreatedAt,
		}
	}
	return marked
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func normalizeDMConversationOffset(offset int) int {
	if offset < 1 {
		return 1
	}
	return offset
}

func sleepRandom(ctx context.Context, rng *rand.Rand, cfg Config, minDelay, maxDelay time.Duration, label string) error {
	if maxDelay < minDelay {
		maxDelay = minDelay
	}
	delay := minDelay
	if maxDelay > minDelay {
		delay += time.Duration(rng.Int63n(int64(maxDelay - minDelay)))
	}

	logf(cfg, "%s %s...\n", label, delay.Truncate(time.Second))
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func generateNonce(alphabet string, length int) string {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	var builder strings.Builder
	builder.Grow(length)
	for i := 0; i < length; i++ {
		builder.WriteByte(alphabet[rng.Intn(len(alphabet))])
	}
	return builder.String()
}

func generateTransportEncoding(key string, fingerprint model.TransportFingerprint) string {
	payload, _ := json.Marshal(fingerprint)
	alphabet := "0123456789abcdefghijklmnopqrstuvwxyz"
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	salt := make([]byte, 8)
	for i := range salt {
		salt[i] = alphabet[rng.Intn(len(alphabet))]
	}

	var builder strings.Builder
	builder.Grow(len(salt) + len(payload)*2)
	builder.Write(salt)

	for index, char := range payload {
		value := char ^ key[index%len(key)] ^ salt[index%len(salt)]
		builder.WriteString(fmt.Sprintf("%02x", value))
	}

	return builder.String()
}

func buildMultipartBody(fields map[string]string) (string, bytes.Buffer) {
	boundary := "----WebKitFormBoundary" + generateNonce("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789", 16)
	var body bytes.Buffer

	for name, value := range fields {
		body.WriteString("--" + boundary + "\r\n")
		body.WriteString(fmt.Sprintf("Content-Disposition: form-data; name=\"%s\"\r\n\r\n", name))
		body.WriteString(value)
		body.WriteString("\r\n")
	}

	body.WriteString("--" + boundary + "--\r\n")
	return "multipart/form-data; boundary=" + boundary, body
}

func matchAndChat(
	ctx context.Context,
	client *api.Client,
	cfg Config,
	p paths,
	login model.LoginInfo,
	currentUserID string,
	rng *rand.Rand,
	realtimeEvents <-chan realtimeEvent,
) (bool, error) {
	for {
		if err := match(ctx, client, cfg); err != nil {
			return false, fmt.Errorf("match request failed: %w", err)
		}
		logln(cfg, "Match request sent. Waiting for a new conversation...")

		target, err := waitForNewMatchConversation(ctx, client, cfg, currentUserID, realtimeEvents)
		if err != nil {
			return false, err
		}
		if target.ConversationID == "" {
			logln(cfg, "No new match found within the time limit.")
			return false, nil
		}

		if isUserFlagged(cfg.DataDir, target.UserID) {
			logf(cfg, "User %s (ID: %s) is globally flagged. Hitting match API to skip and request a new user.\n", target.Username, target.UserID)

			if state, accountKey, accountState, err := loadBotAccountState(p.BotState, currentUserID); err == nil {
				accountState.SkippedUsersCount++
				state[accountKey] = accountState
				_ = store.SaveJSON(p.BotState, state)
			}

			continue
		}

		logf(cfg, "New match connected: user %s (ID: %s), conversation %s\n", target.Username, target.UserID, target.ConversationID)

		return executeMatchChatSequence(ctx, client, cfg, p, login, currentUserID, target, rng, realtimeEvents)
	}
}

func waitForNewMatchConversation(
	ctx context.Context,
	client *api.Client,
	cfg Config,
	currentUserID string,
	realtimeEvents <-chan realtimeEvent,
) (model.DMTarget, error) {
	initialConversations, _, err := fetchDMConversations(ctx, client, cfg)
	if err != nil {
		return model.DMTarget{}, fmt.Errorf("failed to fetch initial conversations: %w", err)
	}
	initialConversationIDs := make(map[string]bool)
	for _, conv := range initialConversations {
		initialConversationIDs[conv.ID] = true
	}

	pollingCtx, cancelPolling := context.WithTimeout(ctx, 5*time.Minute)
	defer cancelPolling()

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-pollingCtx.Done():
			return model.DMTarget{}, nil // Timeout, no new match
		case event := <-realtimeEvents:
			if event.Type == "matchUpdate" && event.Target.UserID != "" && !event.Closed {
				return event.Target, nil
			}
		case <-ticker.C:
			latestConversations, _, err := fetchDMConversations(ctx, client, cfg)
			if err != nil {
				logf(cfg, "Error fetching conversations while waiting for match: %v\n", err)
				continue
			}

			for _, conv := range latestConversations {
				if initialConversationIDs[conv.ID] {
					continue
				}

				targets := extractDMTargets([]model.DMConversation{conv}, currentUserID)
				if len(targets) == 0 {
					continue
				}

				claimableTargets, err := claimDMTargets(cfg, currentUserID, targets)
				if err != nil {
					return model.DMTarget{}, err
				}
				if len(claimableTargets) > 0 {
					return claimableTargets[0], nil
				}
			}
		}
	}
}

func getActiveSequence(cfg Config, accountState *model.AccountState, targetConversationID string, sentCount int, rng *rand.Rand) []string {
	if len(cfg.AvailableSequences) > 0 {
		valid := make([][]string, 0, len(cfg.AvailableSequences))
		for _, seq := range cfg.AvailableSequences {
			if len(seq) > sentCount {
				valid = append(valid, seq)
			}
		}
		if len(valid) > 0 {
			return valid[rng.Intn(len(valid))]
		}
		return cfg.AvailableSequences[rng.Intn(len(cfg.AvailableSequences))]
	}
	return cfg.MessageSequence
}

func executeMatchChatSequence(
	ctx context.Context,
	client *api.Client,
	cfg Config,
	p paths,
	login model.LoginInfo,
	currentUserID string,
	target model.DMTarget,
	rng *rand.Rand,
	realtimeEvents <-chan realtimeEvent,
) (bool, error) {
	if evaChatEnabled(cfg) {
		return executeMatchEVAChat(ctx, client, cfg, p, login, currentUserID, target, rng, realtimeEvents)
	}
	if cfg.ChatMode == model.ChatModeKeyword && len(cfg.KeywordRules) > 0 {
		return executeMatchKeywordChatSequence(ctx, client, cfg, p, login, currentUserID, target, rng, realtimeEvents)
	}

	state, accountKey, accountState, err := loadBotAccountState(p.BotState, currentUserID)
	if err != nil {
		return false, err
	}

	sentCount := accountState.SentMessageCounts[target.UserID]

	activeSequence := getActiveSequence(cfg, &accountState, target.UserID, sentCount, rng)
	for sentCount < len(activeSequence) {
		activeSequence = getActiveSequence(cfg, &accountState, target.UserID, sentCount, rng)
		messageText, hasUsername := renderMsg(cfg, &accountState, target.UserID, sentCount, rng, activeSequence[sentCount])
		if messageText == "" {
			logf(cfg, "Message template for sequence %d is empty. Ending chat with %s.\n", sentCount+1, target.Username)
			break
		}

		if sentCount > 0 {
			var replyDelay time.Duration
			if cfg.ReplyDelayMax > cfg.ReplyDelayMin {
				replyDelay = cfg.ReplyDelayMin + time.Duration(rng.Int63n(int64(cfg.ReplyDelayMax-cfg.ReplyDelayMin)))
			} else {
				replyDelay = cfg.ReplyDelayMin
			}

			if replyDelay > 0 {
				logf(cfg, "Delaying %.0f seconds before sending next message to %s\n", replyDelay.Seconds(), target.Username)
				select {
				case <-ctx.Done():
					return true, ctx.Err()
				case <-time.After(replyDelay):
				}
			}
		}

		logf(cfg, "Sending message %d/%d to matched user %s\n", sentCount+1, len(cfg.MessageSequence), target.Username)
		status, _, err := sendDMMessage(ctx, client, cfg, login, target.ConversationID, messageText)
		if err != nil {
			return true, err
		}
		// logf(cfg, "  -> POST %d %s\n", status, text)
		if err := trackBotSendStatus(cfg, currentUserID, status); err != nil {
			return true, err
		}
		if status >= 400 {
			logf(cfg, "Failed to send message to %s. Ending chat.\n", target.Username)
			return true, nil
		}

		if hasUsername {
			accountState.SnapTgSentCount++
			_ = markUserFlagged(cfg.DataDir, target.UserID)
		}
		logSendActivity(cfg, currentUserID, target, messageText)
		sentCount++
		accountState.SentMessageCounts[target.UserID] = sentCount
		accountState.BaselineDone[target.UserID] = true
		if err := saveBotAccountState(p.BotState, state, accountKey, accountState); err != nil {
			return true, err
		}

		var replyTimeout time.Duration
		if cfg.MatchReplyTimeoutMax > cfg.MatchReplyTimeoutMin {
			replyTimeout = cfg.MatchReplyTimeoutMin + time.Duration(rng.Int63n(int64(cfg.MatchReplyTimeoutMax-cfg.MatchReplyTimeoutMin)))
		} else {
			replyTimeout = cfg.MatchReplyTimeoutMin
		}

		logf(cfg, "Waiting for reply from %s (%.0f seconds timeout)...\n", target.Username, replyTimeout.Seconds())
		replyTimer := time.NewTimer(replyTimeout)
		chatActive := true

	waitLoop:
		for {
			select {
			case <-ctx.Done():
				replyTimer.Stop()
				return true, ctx.Err()
			case <-replyTimer.C:
				logf(cfg, "No reply from %s within 1 minute. Leaving conversation and finding new match.\n", target.Username)
				return true, nil
			case event := <-realtimeEvents:
				if event.ConversationID != target.ConversationID {
					continue
				}

				if event.Type == "chatMessage" && event.UserID == target.UserID {
					replyTimer.Stop()
					logf(cfg, "Received reply from %s: %q\n", target.Username, event.Content)
					logReceiveActivity(cfg, currentUserID, target, event.Content)
					markSeen(accountState.IncomingMessages, target.ConversationID, []model.Message{{ID: event.MessageID, Content: event.Content}}, true)
					if err := saveBotAccountState(p.BotState, state, accountKey, accountState); err != nil {
						return true, err
					}
					break waitLoop
				} else if event.Type == "matchUpdate" && event.Closed {
					replyTimer.Stop()
					var wait time.Duration
					if cfg.MatchCloseWaitMax > cfg.MatchCloseWaitMin {
						wait = cfg.MatchCloseWaitMin + time.Duration(rng.Int63n(int64(cfg.MatchCloseWaitMax-cfg.MatchCloseWaitMin)))
					} else {
						wait = cfg.MatchCloseWaitMin
					}
					logf(cfg, "Conversation with %s was closed by user %s. Waiting %.0f seconds before finding new match.\n", target.Username, event.ClosedBy, wait.Seconds())
					if wait > 0 {
						select {
						case <-ctx.Done():
							return true, ctx.Err()
						case <-time.After(wait):
						}
					}
					chatActive = false
					break waitLoop
				}
			}
		}

		if !chatActive {
			return true, nil
		}
	}

	if sentCount >= len(cfg.MessageSequence) {
		logf(cfg, "Message sequence finished for %s. Waiting for conversation to be closed.\n", target.Username)
	}

	closeWaitTimer := time.NewTimer(5 * time.Minute)
	defer closeWaitTimer.Stop()
	for {
		select {
		case <-ctx.Done():
			return true, ctx.Err()
		case <-closeWaitTimer.C:
			logf(cfg, "Conversation with %s was not closed after 5 minutes. Moving on.\n", target.Username)
			return true, nil
		case event := <-realtimeEvents:
			if event.ConversationID == target.ConversationID {
				if event.Type == "matchUpdate" && event.Closed {
					logf(cfg, "Conversation with %s was closed by user %s. Finding new match.\n", target.Username, event.ClosedBy)
					return true, nil
				}
				if event.Type == "chatMessage" && event.UserID == target.UserID {
					logf(cfg, "Received another message from %s after sequence finished: %q\n", target.Username, event.Content)
					logReceiveActivity(cfg, currentUserID, target, event.Content)
				}
			}
		}
	}
}

// executeMatchEVAChat keeps match chat on the same stateful engine used by
// inbox replies. It deliberately reuses the existing send, timeout, activity,
// and account-state mechanisms rather than changing networking behavior.
func executeMatchEVAChat(
	ctx context.Context,
	client *api.Client,
	cfg Config,
	p paths,
	login model.LoginInfo,
	currentUserID string,
	target model.DMTarget,
	rng *rand.Rand,
	realtimeEvents <-chan realtimeEvent,
) (bool, error) {
	state, accountKey, accountState, err := loadBotAccountState(p.BotState, currentUserID)
	if err != nil {
		return false, err
	}

	sentCount := accountState.SentMessageCounts[target.UserID]
	session := evaChatSession(&accountState, target.UserID)
	if !session.Started && !session.Ended {
		result := evaChatStart(cfg, &accountState, target.UserID, rng)
		if result.Text == "" {
			return true, fmt.Errorf("EVAChat opener unavailable: %s", result.Reason)
		}
		logf(cfg, "[evachat] Sending opener to matched user %s from %s\n", target.Username, result.SourcePool)
		status, _, err := sendDMMessage(ctx, client, cfg, login, target.ConversationID, result.Text)
		if err != nil {
			return true, err
		}
		if err := trackBotSendStatus(cfg, currentUserID, status); err != nil {
			return true, err
		}
		if status >= 400 {
			logf(cfg, "[evachat] Failed to send opener to %s. Ending chat.\n", target.Username)
			return true, nil
		}
		if result.HasUsername {
			accountState.SnapTgSentCount++
			_ = markUserFlagged(cfg.DataDir, target.UserID)
		}
		sentCount++
		accountState.SentMessageCounts[target.UserID] = sentCount
		accountState.BaselineDone[target.UserID] = true
		state[accountKey] = accountState
		if err := store.SaveJSON(p.BotState, state); err != nil {
			return true, err
		}
		logSendActivity(cfg, currentUserID, target, result.Text)
	}

	for {
		var replyTimeout time.Duration
		if cfg.MatchReplyTimeoutMax > cfg.MatchReplyTimeoutMin {
			replyTimeout = cfg.MatchReplyTimeoutMin + time.Duration(rng.Int63n(int64(cfg.MatchReplyTimeoutMax-cfg.MatchReplyTimeoutMin)))
		} else {
			replyTimeout = cfg.MatchReplyTimeoutMin
		}

		logf(cfg, "[evachat] Waiting for reply from %s (%.0f seconds timeout)...\n", target.Username, replyTimeout.Seconds())
		replyTimer := time.NewTimer(replyTimeout)
		var incoming model.Message
		chatActive := true

	waitLoop:
		for {
			select {
			case <-ctx.Done():
				replyTimer.Stop()
				return true, ctx.Err()
			case <-replyTimer.C:
				logf(cfg, "[evachat] No reply from %s within %.0f seconds. Finding new match.\n", target.Username, replyTimeout.Seconds())
				return true, nil
			case event := <-realtimeEvents:
				if event.ConversationID != target.ConversationID {
					continue
				}
				if event.Type == "chatMessage" && event.UserID == target.UserID {
					replyTimer.Stop()
					incoming = model.Message{ID: event.MessageID, Content: event.Content, CreatedAt: event.CreatedAt}
					incoming.Author.ID = event.UserID
					logReceiveActivity(cfg, currentUserID, target, incoming.Content)
					break waitLoop
				}
				if event.Type == "matchUpdate" && event.Closed {
					replyTimer.Stop()
					logf(cfg, "[evachat] Conversation closed by %s. Finding new match.\n", event.ClosedBy)
					chatActive = false
					break waitLoop
				}
			}
		}

		if !chatActive {
			return true, nil
		}

		result := evaChatReply(cfg, &accountState, target.UserID, incoming.Content, rng)
		if result.Text == "" {
			markSeen(accountState.IncomingMessages, target.ConversationID, []model.Message{incoming}, false)
			state[accountKey] = accountState
			if err := store.SaveJSON(p.BotState, state); err != nil {
				return true, err
			}
			if result.Ended {
				return true, nil
			}
			continue
		}

		var replyDelay time.Duration
		if cfg.ReplyDelayMax > cfg.ReplyDelayMin {
			replyDelay = cfg.ReplyDelayMin + time.Duration(rng.Int63n(int64(cfg.ReplyDelayMax-cfg.ReplyDelayMin)))
		} else {
			replyDelay = cfg.ReplyDelayMin
		}
		if replyDelay > 0 {
			select {
			case <-ctx.Done():
				return true, ctx.Err()
			case <-time.After(replyDelay):
			}
		}

		logf(cfg, "[evachat] Sending reply to %s from %s (%s)\n", target.Username, result.SourcePool, result.Reason)
		status, _, err := sendDMMessage(ctx, client, cfg, login, target.ConversationID, result.Text)
		if err != nil {
			return true, err
		}
		if err := trackBotSendStatus(cfg, currentUserID, status); err != nil {
			return true, err
		}
		if status >= 400 {
			logf(cfg, "[evachat] Failed to send reply to %s. Ending chat.\n", target.Username)
			return true, nil
		}

		if result.HasUsername {
			accountState.SnapTgSentCount++
			_ = markUserFlagged(cfg.DataDir, target.UserID)
			logf(cfg, "[evachat] Shared snap/tg username with %s.\n", target.Username)
		}
		if err := sendEVAExtraLines(ctx, client, cfg, login, target.ConversationID, result.Extra, rng); err != nil {
			return true, err
		}
		markSeen(accountState.IncomingMessages, target.ConversationID, []model.Message{incoming}, true)
		sentCount++
		accountState.SentMessageCounts[target.UserID] = sentCount
		state[accountKey] = accountState
		if err := store.SaveJSON(p.BotState, state); err != nil {
			return true, err
		}
		logSendActivity(cfg, currentUserID, target, result.Text)
		if result.Ended {
			logf(cfg, "[evachat] Flow ended for %s. Moving to next match.\n", target.Username)
			return true, nil
		}
	}
}

// sendEVAExtraLines sends the follow-up lines of a multi-line EVAChat reply
// (the horny-question answer is 2 lines: horny_answer.txt then horny.txt).
// Failures of the extra line are logged, never fatal: the main line is sent.
func sendEVAExtraLines(ctx context.Context, client *api.Client, cfg Config, login model.LoginInfo, conversationID string, lines []string, rng *rand.Rand) error {
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if err := sleepRandom(ctx, rng, cfg, cfg.ActionMin, cfg.ActionMax, "Waiting before EVAChat follow-up line"); err != nil {
			return err
		}
		status, _, err := sendDMMessage(ctx, client, cfg, login, conversationID, line)
		if err != nil {
			return err
		}
		if status >= 400 {
			logf(cfg, "[evachat] Follow-up line failed with status %d for %s.\n", status, conversationID)
			return nil
		}
		logf(cfg, "[evachat] Sent follow-up line to %s.\n", conversationID)
	}
	return nil
}

func executeMatchKeywordChatSequence(
	ctx context.Context,
	client *api.Client,
	cfg Config,
	p paths,
	login model.LoginInfo,
	currentUserID string,
	target model.DMTarget,
	rng *rand.Rand,
	realtimeEvents <-chan realtimeEvent,
) (bool, error) {
	state, accountKey, accountState, err := loadBotAccountState(p.BotState, currentUserID)
	if err != nil {
		return false, err
	}

	usedRules := make(map[int]bool)
	sentCount := accountState.SentMessageCounts[target.UserID]
	assignedUsername := getOrAssignUsername(cfg, &accountState, target.UserID, rng)

	// Step 1: Send opener if bot has not sent any message to this user yet
	if sentCount == 0 {
		var openerText string
		if len(cfg.KeywordRules) > 0 {
			openerText = RenderKeywordReply(cfg.KeywordRules[0].ReplyTemplate, rng, assignedUsername)
			usedRules[0] = true
		}
		if openerText == "" {
			openerText = "hey"
		}

		logf(cfg, "[keyword] Sending opener to matched user %s: %q\n", target.Username, openerText)
		status, _, err := sendDMMessage(ctx, client, cfg, login, target.ConversationID, openerText)
		if err != nil {
			return true, err
		}
		if err := trackBotSendStatus(cfg, currentUserID, status); err != nil {
			return true, err
		}
		if status >= 400 {
			logf(cfg, "[keyword] Failed to send opener to %s. Ending chat.\n", target.Username)
			return true, nil
		}

		if strings.Contains(openerText, assignedUsername) && assignedUsername != "" {
			accountState.SnapTgSentCount++
			_ = markUserFlagged(cfg.DataDir, target.UserID)
		}
		logSendActivity(cfg, currentUserID, target, openerText)
		sentCount++
		accountState.SentMessageCounts[target.UserID] = sentCount
		accountState.BaselineDone[target.UserID] = true
		if err := saveBotAccountState(p.BotState, state, accountKey, accountState); err != nil {
			return true, err
		}
	}

	// Step 2: Chat loop - process incoming messages and reply based on keyword rules
	for {
		var replyTimeout time.Duration
		if cfg.MatchReplyTimeoutMax > cfg.MatchReplyTimeoutMin {
			replyTimeout = cfg.MatchReplyTimeoutMin + time.Duration(rng.Int63n(int64(cfg.MatchReplyTimeoutMax-cfg.MatchReplyTimeoutMin)))
		} else {
			replyTimeout = cfg.MatchReplyTimeoutMin
		}

		logf(cfg, "[keyword] Waiting for reply from %s (%.0f seconds timeout)...\n", target.Username, replyTimeout.Seconds())
		replyTimer := time.NewTimer(replyTimeout)
		var incomingText string
		chatActive := true

	waitLoop:
		for {
			select {
			case <-ctx.Done():
				replyTimer.Stop()
				return true, ctx.Err()
			case <-replyTimer.C:
				logf(cfg, "[keyword] No reply from %s within timeout. Finding new match.\n", target.Username)
				return true, nil
			case event := <-realtimeEvents:
				if event.ConversationID != target.ConversationID {
					continue
				}

				if event.Type == "chatMessage" && event.UserID == target.UserID {
					replyTimer.Stop()
					incomingText = event.Content
					logf(cfg, "[keyword] Received reply from %s: %q\n", target.Username, incomingText)
					logReceiveActivity(cfg, currentUserID, target, incomingText)
					markSeen(accountState.IncomingMessages, target.ConversationID, []model.Message{{ID: event.MessageID, Content: event.Content}}, true)
					_ = saveBotAccountState(p.BotState, state, accountKey, accountState)
					break waitLoop
				} else if event.Type == "matchUpdate" && event.Closed {
					replyTimer.Stop()
					logf(cfg, "[keyword] Conversation closed by %s. Finding new match.\n", event.ClosedBy)
					chatActive = false
					break waitLoop
				}
			}
		}

		if !chatActive {
			return true, nil
		}

		// Check incomingText against keyword rules
		matchedRule, ruleIdx := FindMatchingKeywordRule(incomingText, cfg.KeywordRules, usedRules)
		if matchedRule == nil {
			// Unmatched: record to notepad file, increment skip counter, skip user
			_ = RecordUnmatchedKeyword(cfg.DataDir, target.Username, target.UserID, incomingText)
			logf(cfg, "[keyword] No matching keyword found for %q from %s. Recorded to unmatched_keywords.txt. Skipping user.\n", incomingText, target.Username)

			accountState.SkippedUsersCount++
			_ = saveBotAccountState(p.BotState, state, accountKey, accountState)
			return true, nil
		}

		usedRules[ruleIdx] = true
		replyText := RenderKeywordReply(matchedRule.ReplyTemplate, rng, assignedUsername)
		if replyText == "" {
			logf(cfg, "[keyword] Matched reply template rendered empty for %s. Skipping user.\n", target.Username)
			return true, nil
		}

		// Configured reply delay
		var replyDelay time.Duration
		if cfg.ReplyDelayMax > cfg.ReplyDelayMin {
			replyDelay = cfg.ReplyDelayMin + time.Duration(rng.Int63n(int64(cfg.ReplyDelayMax-cfg.ReplyDelayMin)))
		} else {
			replyDelay = cfg.ReplyDelayMin
		}
		if replyDelay > 0 {
			logf(cfg, "[keyword] Delaying %.0f seconds before sending keyword reply to %s\n", replyDelay.Seconds(), target.Username)
			select {
			case <-ctx.Done():
				return true, ctx.Err()
			case <-time.After(replyDelay):
			}
		}

		logf(cfg, "[keyword] Sending reply to %s: %q\n", target.Username, replyText)
		status, _, err := sendDMMessage(ctx, client, cfg, login, target.ConversationID, replyText)
		if err != nil {
			return true, err
		}
		if err := trackBotSendStatus(cfg, currentUserID, status); err != nil {
			return true, err
		}
		if status >= 400 {
			logf(cfg, "[keyword] Failed to send reply to %s. Ending chat.\n", target.Username)
			return true, nil
		}

		if strings.Contains(replyText, assignedUsername) && assignedUsername != "" {
			accountState.SnapTgSentCount++
			_ = markUserFlagged(cfg.DataDir, target.UserID)
		}
		logSendActivity(cfg, currentUserID, target, replyText)
		sentCount++
		accountState.SentMessageCounts[target.UserID] = sentCount
		_ = saveBotAccountState(p.BotState, state, accountKey, accountState)
	}
}

func getOrAssignUsername(cfg Config, accountState *model.AccountState, userID string, rng *rand.Rand) string {
	if accountState.AssignedUsernames == nil {
		accountState.AssignedUsernames = make(map[string]string)
	}
	if assigned := accountState.AssignedUsernames[userID]; assigned != "" {
		return assigned
	}
	if len(cfg.MessageUsernames) == 0 {
		return ""
	}
	assigned := chooseUsername(cfg.MessageUsernames, rng)
	accountState.AssignedUsernames[userID] = assigned
	return assigned
}

func renderMsg(cfg Config, accountState *model.AccountState, targetUserID string, sentCount int, rng *rand.Rand, template string) (string, bool) {
	hasUsername := strings.Contains(template, "%username%")
	var assignedUsername string
	if hasUsername {
		assignedUsername = getOrAssignUsername(cfg, accountState, targetUserID, rng)
	}
	return renderMessageTemplate(template, rng, assignedUsername), hasUsername
}

var flaggedUsersMutex sync.Mutex

func markUserFlagged(dataDir string, userID string) error {
	flaggedUsersMutex.Lock()
	defer flaggedUsersMutex.Unlock()

	path := filepath.Join(dataDir, "flagged_users.json")
	var flagged map[string]bool
	ok, err := store.LoadJSON(path, &flagged)
	if err != nil {
		return err
	}
	if !ok || flagged == nil {
		flagged = make(map[string]bool)
	}
	flagged[userID] = true
	return store.SaveJSON(path, flagged)
}

func isUserFlagged(dataDir string, userID string) bool {
	flaggedUsersMutex.Lock()
	defer flaggedUsersMutex.Unlock()

	path := filepath.Join(dataDir, "flagged_users.json")
	var flagged map[string]bool
	ok, err := store.LoadJSON(path, &flagged)
	if err != nil || !ok || flagged == nil {
		return false
	}
	return flagged[userID]
}
