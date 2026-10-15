package runtimeassets

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCompareVersionStrings(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		left  string
		right string
		want  int
	}{
		{name: "equal", left: "150.0.7871.182", right: "150.0.7871.182", want: 0},
		{name: "older major", left: "147.0.7727.56", right: "150.0.7871.182", want: -1},
		{name: "newer patch", left: "150.0.7871.183", right: "150.0.7871.182", want: 1},
		{name: "missing treated as older", left: "", right: "150.0.7871.182", want: -1},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := compareVersionStrings(tc.left, tc.right); got != tc.want {
				t.Fatalf("compareVersionStrings(%q, %q) = %d, want %d", tc.left, tc.right, got, tc.want)
			}
		})
	}
}

func TestBundledChromiumPathForProfileSkipsOlderBundle(t *testing.T) {
	tempDir := t.TempDir()
	profileDir := filepath.Join(tempDir, "profile")
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		t.Fatalf("mkdir profile dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(profileDir, "Last Version"), []byte("150.0.7871.182\n"), 0o644); err != nil {
		t.Fatalf("write Last Version: %v", err)
	}

	chromeDir := filepath.Join(tempDir, "chrome-win64")
	if err := os.MkdirAll(chromeDir, 0o755); err != nil {
		t.Fatalf("mkdir chrome dir: %v", err)
	}
	chromePath := filepath.Join(chromeDir, "chrome.exe")
	if err := os.WriteFile(chromePath, []byte(""), 0o644); err != nil {
		t.Fatalf("write chrome.exe: %v", err)
	}
	if err := os.WriteFile(filepath.Join(chromeDir, "147.0.7727.56.manifest"), []byte(""), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	if version := profileChromiumVersion(profileDir); version != "150.0.7871.182" {
		t.Fatalf("profileChromiumVersion() = %q", version)
	}

	if version := bundledChromiumVersion(chromePath); version != "147.0.7727.56" {
		t.Fatalf("bundledChromiumVersion() = %q", version)
	}

	if path, reason := bundledChromiumPathForProfilePath(profileDir, chromePath); path != "" || reason == "" {
		t.Fatalf("bundledChromiumPathForProfilePath() = (%q, %q), want empty path with reason", path, reason)
	}
}
