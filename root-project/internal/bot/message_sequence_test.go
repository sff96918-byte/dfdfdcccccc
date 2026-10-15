package bot

import (
	"math/rand"
	"testing"
)

func TestRenderMessageTemplate(t *testing.T) {
	t.Run("joins adjacent groups with spaces", func(t *testing.T) {
		got := renderMessageTemplate("{hello|hi}{how|hw}", rand.New(rand.NewSource(1)), "")
		if got != "hi hw" {
			t.Fatalf("unexpected rendered message: %q", got)
		}
	})

	t.Run("preserves plain words and normalizes spaces", func(t *testing.T) {
		got := renderMessageTemplate("  hey   {there|bro}  now ", rand.New(rand.NewSource(0)), "")
		if got != "hey there now" {
			t.Fatalf("unexpected rendered message: %q", got)
		}
	})

	t.Run("supports group options with spaces", func(t *testing.T) {
		got := renderMessageTemplate("{how are you|how r u}", rand.New(rand.NewSource(0)), "")
		if got != "how are you" {
			t.Fatalf("unexpected rendered message: %q", got)
		}
	})

	t.Run("replaces username placeholder from pool", func(t *testing.T) {
		got := renderMessageTemplate("add me on snap %username%", rand.New(rand.NewSource(0)), "snap_one")
		if got != "add me on snap snap_one" {
			t.Fatalf("unexpected rendered message: %q", got)
		}
	})

	t.Run("uses one random username for repeated placeholders", func(t *testing.T) {
		got := renderMessageTemplate("%username% follow %username%", rand.New(rand.NewSource(1)), "tg_two")
		if got != "tg_two follow tg_two" {
			t.Fatalf("unexpected rendered message: %q", got)
		}
	})
}
