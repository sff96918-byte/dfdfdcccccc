package workerplan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"chitchatbot/internal/model"
	"chitchatbot/internal/proxy"
)

type Profile struct {
	Name          string
	LoginInfoPath string
}

type Assignment struct {
	Profile       string
	LoginInfoPath string
	Proxy         proxy.Entry
	HasProxy      bool
}

func DiscoverProfiles(dataDir string, defaultProfile string) ([]string, error) {
	workers, err := DiscoverProfileWorkers(dataDir, defaultProfile)
	if err != nil {
		return nil, err
	}

	profiles := make([]string, 0, len(workers))
	for _, worker := range workers {
		profiles = append(profiles, worker.Name)
	}
	return profiles, nil
}

func DiscoverProfileWorkers(dataDir string, defaultProfile string) ([]Profile, error) {
	profilesDir := filepath.Join(dataDir, "profiles")
	entries, err := os.ReadDir(profilesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []Profile{{Name: normalizeProfile(defaultProfile)}}, nil
		}
		return nil, err
	}

	candidates := make([]profileCandidate, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := strings.TrimSpace(entry.Name())
		if name == "" {
			continue
		}
		profileDir := filepath.Join(profilesDir, name)
		dirCandidates, err := discoverLoginCandidates(profileDir, name)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, dirCandidates...)
	}

	if len(candidates) == 0 {
		return []Profile{{Name: normalizeProfile(defaultProfile)}}, nil
	}

	candidates = dedupeProfileCandidates(candidates)
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Profile.Name < candidates[j].Profile.Name
	})

	profiles := make([]Profile, 0, len(candidates))
	for _, candidate := range candidates {
		profiles = append(profiles, candidate.Profile)
	}
	return profiles, nil
}

func AssignProfiles(profiles []string, proxies []proxy.Entry) []Assignment {
	workerProfiles := make([]Profile, 0, len(profiles))
	for _, profileName := range profiles {
		workerProfiles = append(workerProfiles, Profile{Name: profileName})
	}
	return AssignProfileWorkers(workerProfiles, proxies)
}

func AssignProfileWorkers(profiles []Profile, proxies []proxy.Entry) []Assignment {
	assignments := make([]Assignment, 0, len(profiles))

	for index, profile := range profiles {
		assignment := Assignment{
			Profile:       normalizeProfile(profile.Name),
			LoginInfoPath: profile.LoginInfoPath,
		}

		if len(proxies) > 0 {
			assignment.Proxy = proxies[index%len(proxies)]
			assignment.HasProxy = true
		}

		assignments = append(assignments, assignment)
	}

	return assignments
}

type profileCandidate struct {
	Profile Profile
	UserID  string
	SavedAt time.Time
	ModTime time.Time
}

func discoverLoginCandidates(profileDir string, profileName string) ([]profileCandidate, error) {
	pattern := filepath.Join(profileDir, "login_info*.json")
	loginPaths, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	sort.Strings(loginPaths)

	candidates := make([]profileCandidate, 0, len(loginPaths))
	for _, loginPath := range loginPaths {
		base := filepath.Base(loginPath)
		if base != "login_info.json" && !strings.HasPrefix(base, "login_info_") {
			continue
		}

		login, _ := loadLoginSummary(loginPath)
		userID := strings.TrimSpace(login.CurrentUserID())

		workerName := profileName
		loginInfoPath := ""
		if base != "login_info.json" {
			workerName = workerNameFromLoginFile(profileName, loginPath, userID)
			loginInfoPath = loginPath
		}

		info, statErr := os.Stat(loginPath)
		var modTime time.Time
		if statErr == nil {
			modTime = info.ModTime()
		}

		candidates = append(candidates, profileCandidate{
			Profile: Profile{
				Name:          normalizeProfile(workerName),
				LoginInfoPath: loginInfoPath,
			},
			UserID:  userID,
			SavedAt: login.SavedAt,
			ModTime: modTime,
		})
	}
	return candidates, nil
}

func loadLoginSummary(path string) (model.LoginInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return model.LoginInfo{}, err
	}

	var login model.LoginInfo
	if err := json.Unmarshal(data, &login); err != nil {
		return model.LoginInfo{}, err
	}
	return login, nil
}

func workerNameFromLoginFile(parentProfile string, loginPath string, userID string) string {
	userID = sanitizeProfileSegment(userID)
	if userID != "" {
		return userID
	}

	base := strings.TrimSuffix(filepath.Base(loginPath), filepath.Ext(loginPath))
	base = strings.TrimPrefix(base, "login_info_")
	base = sanitizeProfileSegment(base)
	if base != "" {
		return base
	}
	return parentProfile
}

func dedupeProfileCandidates(candidates []profileCandidate) []profileCandidate {
	byKey := map[string]profileCandidate{}
	for _, candidate := range candidates {
		key := candidate.UserID
		if key == "" {
			key = "path:" + candidate.Profile.LoginInfoPath
			if candidate.Profile.LoginInfoPath == "" {
				key = "profile:" + candidate.Profile.Name
			}
		} else {
			key = "user:" + key
		}

		existing, exists := byKey[key]
		if !exists || newerCandidate(candidate, existing) {
			byKey[key] = candidate
		}
	}

	result := make([]profileCandidate, 0, len(byKey))
	for _, candidate := range byKey {
		result = append(result, candidate)
	}
	return result
}

func newerCandidate(candidate profileCandidate, existing profileCandidate) bool {
	if !candidate.SavedAt.Equal(existing.SavedAt) {
		return candidate.SavedAt.After(existing.SavedAt)
	}
	return candidate.ModTime.After(existing.ModTime)
}

func normalizeProfile(profile string) string {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return "default"
	}
	return profile
}

func sanitizeProfileSegment(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}

	value = strings.ReplaceAll(value, "..", "_")
	value = strings.NewReplacer("\\", "_", "/", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_", " ", "_").Replace(value)
	return strings.Trim(value, "._")
}
