package workerplan

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"chitchatbot/internal/proxy"
)

func TestDiscoverProfiles(t *testing.T) {
	t.Run("uses default when profiles dir is missing", func(t *testing.T) {
		got, err := DiscoverProfiles(t.TempDir(), "account1")
		if err != nil {
			t.Fatalf("discover profiles: %v", err)
		}

		want := []string{"account1"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("profiles mismatch: got %v want %v", got, want)
		}
	})

	t.Run("returns sorted profile directories", func(t *testing.T) {
		dataDir := t.TempDir()
		profilesDir := filepath.Join(dataDir, "profiles")
		for _, name := range []string{"zeta", "alpha", "middle"} {
			if err := os.MkdirAll(filepath.Join(profilesDir, name), 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", name, err)
			}
			writeLoginInfo(t, filepath.Join(profilesDir, name, "login_info.json"), name+"-user", time.Now().UTC())
		}
		if err := os.WriteFile(filepath.Join(profilesDir, "ignored.json"), []byte("{}"), 0o644); err != nil {
			t.Fatalf("write file: %v", err)
		}

		got, err := DiscoverProfiles(dataDir, "default")
		if err != nil {
			t.Fatalf("discover profiles: %v", err)
		}

		want := []string{"alpha", "middle", "zeta"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("profiles mismatch: got %v want %v", got, want)
		}
	})
}

func TestDiscoverProfileWorkersFromUniqueLoginFiles(t *testing.T) {
	dataDir := t.TempDir()
	profileDir := filepath.Join(dataDir, "profiles", "default")
	writeLoginInfo(t, filepath.Join(profileDir, "login_info_user-a_old_01.json"), "user-a", time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))
	writeLoginInfo(t, filepath.Join(profileDir, "login_info_user-a_new_02.json"), "user-a", time.Date(2026, 4, 2, 0, 0, 0, 0, time.UTC))
	writeLoginInfo(t, filepath.Join(profileDir, "login_info_user-b_03.json"), "user-b", time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))

	got, err := DiscoverProfileWorkers(dataDir, "default")
	if err != nil {
		t.Fatalf("discover profile workers: %v", err)
	}

	want := []Profile{
		{
			Name:          "user-a",
			LoginInfoPath: filepath.Join(profileDir, "login_info_user-a_new_02.json"),
		},
		{
			Name:          "user-b",
			LoginInfoPath: filepath.Join(profileDir, "login_info_user-b_03.json"),
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("workers mismatch: got %v want %v", got, want)
	}
}

func TestAssignProfiles(t *testing.T) {
	assignments := AssignProfiles(
		[]string{"p1", "p2", "p3", "p4", "p5"},
		[]proxy.Entry{
			{Scheme: "http", Host: "h1", Port: "1"},
			{Scheme: "http", Host: "h2", Port: "2"},
		},
	)

	got := make([]string, 0, len(assignments))
	for _, assignment := range assignments {
		if !assignment.HasProxy {
			got = append(got, "none")
			continue
		}
		got = append(got, assignment.Proxy.Label())
	}

	want := []string{
		"http://h1:1",
		"http://h2:2",
		"http://h1:1",
		"http://h2:2",
		"http://h1:1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("proxy assignment mismatch: got %v want %v", got, want)
	}
}

func writeLoginInfo(t *testing.T, path string, userID string, savedAt time.Time) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir login dir: %v", err)
	}

	data := []byte(`{"user_id":"` + userID + `","saved_at":"` + savedAt.Format(time.RFC3339Nano) + `"}`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write login info: %v", err)
	}
}
