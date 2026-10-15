package bot

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

var unmatchedMu sync.Mutex

// KeywordRule represents one line of a keyword-based chat file.
// Format: (keyword1|keyword2|...){reply_template}
type KeywordRule struct {
	RawLine       string   `json:"raw_line"`
	Keywords      []string `json:"keywords"`       // lower-cased and trimmed
	ReplyTemplate string   `json:"reply_template"` // template to render reply
}

// ParseKeywordRules parses a multi-line notepad string into KeywordRule structs.
func ParseKeywordRules(content string) []KeywordRule {
	lines := strings.Split(content, "\n")
	rules := make([]KeywordRule, 0, len(lines))

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}

		rule, ok := parseKeywordRuleLine(line)
		if ok {
			rules = append(rules, rule)
		}
	}

	return rules
}

func parseKeywordRuleLine(line string) (KeywordRule, bool) {
	parenOpen := strings.IndexByte(line, '(')
	parenClose := strings.IndexByte(line, ')')
	if parenOpen < 0 || parenClose <= parenOpen {
		return KeywordRule{}, false
	}

	braceOpen := strings.IndexByte(line[parenClose:], '{')
	if braceOpen < 0 {
		return KeywordRule{}, false
	}
	braceOpen += parenClose

	lastBraceClose := strings.LastIndexByte(line, '}')
	if lastBraceClose <= braceOpen {
		lastBraceClose = len(line)
	}

	kwPart := line[parenOpen+1 : parenClose]
	replyPart := line[braceOpen+1 : lastBraceClose]

	rawKeywords := strings.Split(kwPart, "|")
	keywords := make([]string, 0, len(rawKeywords))
	for _, kw := range rawKeywords {
		trimmed := strings.ToLower(strings.TrimSpace(kw))
		if trimmed != "" {
			keywords = append(keywords, trimmed)
		}
	}

	if len(keywords) == 0 {
		return KeywordRule{}, false
	}

	return KeywordRule{
		RawLine:       line,
		Keywords:      keywords,
		ReplyTemplate: strings.TrimSpace(replyPart),
	}, true
}

// MatchKeyword checks if incomingText contains or matches the specified keyword.
func MatchKeyword(incomingText string, keyword string) bool {
	incomingLower := strings.ToLower(incomingText)
	kwLower := strings.ToLower(strings.TrimSpace(keyword))
	if kwLower == "" {
		return false
	}

	// For multi-word phrases or keywords with punctuation, do phrase substring match
	if strings.ContainsAny(kwLower, " -_'\"?!,.;:") {
		return strings.Contains(incomingLower, kwLower)
	}

	// For single words, use regex word boundary so "he" does not match "where" or "the"
	re, err := regexp.Compile(`(?i)\b` + regexp.QuoteMeta(kwLower) + `\b`)
	if err != nil {
		return strings.Contains(incomingLower, kwLower)
	}
	return re.MatchString(incomingText)
}

// FindMatchingKeywordRule searches rules for one that matches incomingText.
// If usedRuleIndices is provided, it prioritizes matching rules that haven't been used yet.
func FindMatchingKeywordRule(incomingText string, rules []KeywordRule, usedRuleIndices map[int]bool) (*KeywordRule, int) {
	if len(rules) == 0 || strings.TrimSpace(incomingText) == "" {
		return nil, -1
	}

	// First pass: find a match that has not yet been used in this conversation
	firstMatch := -1
	for i, rule := range rules {
		for _, kw := range rule.Keywords {
			if MatchKeyword(incomingText, kw) {
				if usedRuleIndices == nil || !usedRuleIndices[i] {
					return &rules[i], i
				}
				if firstMatch == -1 {
					firstMatch = i
				}
				break
			}
		}
	}

	// If all matching rules were used, reuse the first matched rule
	if firstMatch >= 0 {
		return &rules[firstMatch], firstMatch
	}

	return nil, -1
}

// RenderKeywordReply renders a reply template into a final message sentence.
// Supports:
// - %username% placeholder
// - [option1|option2|option3] or {option1|option2|option3} random choices
// - Nested outer braces e.g. {[hello|hi|hey][how|hw][are|r][you|u]}
// - Separates tokens with single spaces.
func RenderKeywordReply(template string, rng *rand.Rand, assignedUsername string) string {
	template = strings.TrimSpace(template)
	if template == "" {
		return ""
	}

	if strings.Contains(template, "%username%") {
		template = strings.ReplaceAll(template, "%username%", assignedUsername)
	}

	// Strip outer wrapper braces if they enclose the whole template: e.g. {[a|b][c|d]}
	if strings.HasPrefix(template, "{") && strings.HasSuffix(template, "}") && strings.Count(template, "{") == 1 && strings.Count(template, "}") == 1 {
		template = strings.TrimSpace(template[1 : len(template)-1])
	}

	tokens := make([]string, 0)
	index := 0
	length := len(template)

	for index < length {
		char := template[index]

		if char == '[' || char == '{' {
			closeByte := byte(']')
			if char == '{' {
				closeByte = '}'
			}
			closing := strings.IndexByte(template[index+1:], closeByte)
			if closing >= 0 {
				optionGroup := template[index+1 : index+1+closing]
				chosen := chooseOptionFromGroup(optionGroup, rng)
				if chosen != "" {
					for _, f := range strings.Fields(chosen) {
						tokens = append(tokens, f)
					}
				}
				index += closing + 2
				continue
			}
		}

		nextBracket := strings.IndexAny(template[index:], "[{")
		if nextBracket < 0 {
			for _, f := range strings.Fields(template[index:]) {
				tokens = append(tokens, f)
			}
			break
		}

		for _, f := range strings.Fields(template[index : index+nextBracket]) {
			tokens = append(tokens, f)
		}
		index += nextBracket
	}

	return strings.Join(tokens, " ")
}

func chooseOptionFromGroup(group string, rng *rand.Rand) string {
	options := strings.Split(group, "|")
	cleaned := make([]string, 0, len(options))
	for _, opt := range options {
		opt = strings.TrimSpace(opt)
		if opt != "" {
			cleaned = append(cleaned, opt)
		}
	}

	if len(cleaned) == 0 {
		return ""
	}
	if len(cleaned) == 1 || rng == nil {
		return cleaned[0]
	}
	return cleaned[rng.Intn(len(cleaned))]
}

// RecordUnmatchedKeyword appends an unmatched user message to unmatched_keywords.txt.
func RecordUnmatchedKeyword(dataDir string, username string, userID string, messageText string) error {
	unmatchedMu.Lock()
	defer unmatchedMu.Unlock()

	messageText = strings.TrimSpace(messageText)
	if messageText == "" {
		return nil
	}

	filePath := filepath.Join(dataDir, "unmatched_keywords.txt")
	f, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	entry := fmt.Sprintf("[%s] User: %s (ID: %s) | Text: %s\r\n", timestamp, username, userID, messageText)
	_, err = f.WriteString(entry)
	return err
}
