package bot

import (
	"fmt"
	"strings"

	"chitchatbot/internal/model"
)

func logSendActivity(cfg Config, currentUserID string, target model.DMTarget, message string) {
	activityf(cfg, "%s\n", formatSendActivity(cfg, currentUserID, target, message))
}

func logReceiveActivity(cfg Config, currentUserID string, target model.DMTarget, message string) {
	activityf(cfg, "%s\n", formatReceiveActivity(cfg, currentUserID, target, message))
}

func formatSendActivity(cfg Config, currentUserID string, target model.DMTarget, message string) string {
	return fmt.Sprintf(
		"SEND | %s -> %s | message: %s",
		botActivityLabel(cfg, currentUserID, target),
		trafficActivityLabel(target),
		activityMessage(message),
	)
}

func formatReceiveActivity(cfg Config, currentUserID string, target model.DMTarget, message string) string {
	return fmt.Sprintf(
		"RECEIVE | %s -> %s | message: %s",
		trafficActivityLabel(target),
		botActivityLabel(cfg, currentUserID, target),
		activityMessage(message),
	)
}

func botActivityLabel(cfg Config, currentUserID string, target model.DMTarget) string {
	username := strings.TrimSpace(target.BotUsername)
	profile := strings.TrimSpace(cfg.Profile)
	currentUserID = strings.TrimSpace(currentUserID)

	label := ""
	switch {
	case username != "":
		label = "bot " + username
	case profile != "":
		label = "bot " + profile
	default:
		label = "bot unknown"
	}
	if currentUserID != "" {
		label += fmt.Sprintf(" (%s)", currentUserID)
	}
	return label
}

func trafficActivityLabel(target model.DMTarget) string {
	username := strings.TrimSpace(target.Username)
	userID := strings.TrimSpace(target.UserID)

	label := ""
	switch {
	case username != "":
		label = "traffic " + username
	default:
		label = "traffic unknown"
	}
	if userID != "" {
		label += fmt.Sprintf(" (%s)", userID)
	}
	return label
}

func activityMessage(message string) string {
	message = strings.Join(strings.Fields(message), " ")
	if message == "" {
		return "(empty)"
	}
	if len(message) > 500 {
		return message[:500] + "..."
	}
	return message
}
