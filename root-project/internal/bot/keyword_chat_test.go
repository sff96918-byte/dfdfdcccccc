package bot

import (
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseKeywordRules(t *testing.T) {
	input := `
# Comment line
(he|hello| how are you ){[hello|hi|hey][how|hw][are|r][you|u]}
(age|how old are you){[i am 22|21][u|and u?]}
(snap|telegram){add me on snap %username%}
`
	rules := ParseKeywordRules(input)
	if len(rules) != 3 {
		t.Fatalf("expected 3 rules, got %d", len(rules))
	}

	r0 := rules[0]
	if len(r0.Keywords) != 3 {
		t.Fatalf("expected 3 keywords in rule 0, got %v", r0.Keywords)
	}
	if r0.Keywords[0] != "he" || r0.Keywords[1] != "hello" || r0.Keywords[2] != "how are you" {
		t.Fatalf("unexpected keywords: %v", r0.Keywords)
	}
	if r0.ReplyTemplate != "[hello|hi|hey][how|hw][are|r][you|u]" {
		t.Fatalf("unexpected template: %q", r0.ReplyTemplate)
	}
}

func TestMatchKeyword(t *testing.T) {
	tests := []struct {
		incoming string
		keyword  string
		want     bool
	}{
		{"he", "he", true},
		{"he how are you", "he", true},
		{"where are you", "he", false}, // "where" should NOT match single-word "he"
		{"the person", "he", false},    // "the" should NOT match "he"
		{"HELLO there", "hello", true},
		{"hi how are you today", "how are you", true},
		{"what is your age", "age", true},
		{"vintage car", "age", false}, // "vintage" should not match "age"
	}

	for _, tt := range tests {
		got := MatchKeyword(tt.incoming, tt.keyword)
		if got != tt.want {
			t.Errorf("MatchKeyword(%q, %q) = %v; want %v", tt.incoming, tt.keyword, got, tt.want)
		}
	}
}

func TestRenderKeywordReply(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	template := "{[hello|hi|hey][how|hw][are|r][you|u]}"
	got := RenderKeywordReply(template, rng, "")
	words := strings.Fields(got)
	if len(words) != 4 {
		t.Fatalf("expected 4 words, got %d in %q", len(words), got)
	}

	// Test username replacement
	userTemplate := "add me on snap %username%"
	userGot := RenderKeywordReply(userTemplate, rng, "cool_snap_123")
	if userGot != "add me on snap cool_snap_123" {
		t.Fatalf("username substitution failed: %q", userGot)
	}
}

func TestFindMatchingKeywordRule(t *testing.T) {
	rules := []KeywordRule{
		{
			Keywords:      []string{"he", "hello"},
			ReplyTemplate: "hi there",
		},
		{
			Keywords:      []string{"age"},
			ReplyTemplate: "22",
		},
	}

	// First match
	rule, idx := FindMatchingKeywordRule("hello stranger", rules, nil)
	if rule == nil || idx != 0 {
		t.Fatalf("expected rule 0, got %v (idx %d)", rule, idx)
	}

	// Unmatched
	rule, idx = FindMatchingKeywordRule("what is the weather?", rules, nil)
	if rule != nil || idx != -1 {
		t.Fatalf("expected no match, got %v (idx %d)", rule, idx)
	}
}

func TestRecordUnmatchedKeyword(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "unmatched-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	err = RecordUnmatchedKeyword(tempDir, "testuser", "user-123", "what color is the sky?")
	if err != nil {
		t.Fatalf("RecordUnmatchedKeyword failed: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(tempDir, "unmatched_keywords.txt"))
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(content), "what color is the sky?") {
		t.Fatalf("expected file to contain unmatched text, got %s", string(content))
	}
}
