package runtimeassets

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func ExecutableDir() string {
	exePath, err := os.Executable()
	if err != nil {
		return ""
	}

	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return filepath.Dir(exePath)
	}
	return filepath.Dir(exePath)
}

func BundledWebView2Path() string {
	exeDir := ExecutableDir()
	if exeDir == "" {
		return ""
	}

	candidates := []string{
		filepath.Join(exeDir, "runtime", "webview2"),
		filepath.Join(exeDir, "WebView2"),
	}

	for _, candidate := range candidates {
		if path := findDirectoryContaining(candidate, "msedgewebview2.exe"); path != "" {
			return path
		}
	}

	return ""
}

func BundledChromiumPath() string {
	exeDir := ExecutableDir()
	if exeDir == "" {
		return ""
	}

	candidates := []string{
		filepath.Join(exeDir, "runtime", "chromium"),
		filepath.Join(exeDir, "Chromium"),
		filepath.Join(exeDir, "chrome-win64"),
	}

	for _, candidate := range candidates {
		if path := findFile(candidate, "chrome.exe"); path != "" {
			return path
		}
	}

	return ""
}

func BundledChromiumPathForProfile(userDataDir string) (string, string) {
	path := BundledChromiumPath()
	if path == "" {
		return "", ""
	}

	return bundledChromiumPathForProfilePath(userDataDir, path)
}

func bundledChromiumPathForProfilePath(userDataDir string, path string) (string, string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", ""
	}

	profileVersion := profileChromiumVersion(userDataDir)
	if profileVersion == "" {
		return path, ""
	}

	bundledVersion := bundledChromiumVersion(path)
	if bundledVersion == "" {
		return path, ""
	}

	if compareVersionStrings(bundledVersion, profileVersion) < 0 {
		return "", fmt.Sprintf(
			"bundled Chromium %s is older than profile data %s in %s",
			bundledVersion,
			profileVersion,
			userDataDir,
		)
	}

	return path, ""
}

func profileChromiumVersion(userDataDir string) string {
	userDataDir = strings.TrimSpace(userDataDir)
	if userDataDir == "" {
		return ""
	}

	data, err := os.ReadFile(filepath.Join(userDataDir, "Last Version"))
	if err != nil {
		return ""
	}

	return normalizeVersionString(string(data))
}

func bundledChromiumVersion(chromePath string) string {
	chromePath = strings.TrimSpace(chromePath)
	if chromePath == "" {
		return ""
	}

	chromeDir := filepath.Dir(chromePath)
	patterns := []string{
		filepath.Join(chromeDir, "*.manifest"),
		filepath.Join(filepath.Dir(chromeDir), "*.manifest"),
	}

	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}

		for _, match := range matches {
			version := normalizeVersionString(strings.TrimSuffix(filepath.Base(match), filepath.Ext(match)))
			if version != "" {
				return version
			}
		}
	}

	return ""
}

func normalizeVersionString(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}

	parts := strings.Split(value, ".")
	if len(parts) == 0 {
		return ""
	}

	normalized := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return ""
		}
		if _, err := strconv.Atoi(part); err != nil {
			return ""
		}
		normalized = append(normalized, part)
	}

	return strings.Join(normalized, ".")
}

func compareVersionStrings(left string, right string) int {
	left = normalizeVersionString(left)
	right = normalizeVersionString(right)
	if left == "" && right == "" {
		return 0
	}
	if left == "" {
		return -1
	}
	if right == "" {
		return 1
	}

	leftParts := strings.Split(left, ".")
	rightParts := strings.Split(right, ".")
	maxParts := len(leftParts)
	if len(rightParts) > maxParts {
		maxParts = len(rightParts)
	}

	for i := 0; i < maxParts; i++ {
		leftValue := 0
		if i < len(leftParts) {
			leftValue, _ = strconv.Atoi(leftParts[i])
		}

		rightValue := 0
		if i < len(rightParts) {
			rightValue, _ = strconv.Atoi(rightParts[i])
		}

		switch {
		case leftValue < rightValue:
			return -1
		case leftValue > rightValue:
			return 1
		}
	}

	return 0
}

func findDirectoryContaining(root string, filename string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}

	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return ""
	}

	direct := filepath.Join(root, filename)
	if fileExists(direct) {
		return root
	}

	var result string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || result != "" {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if strings.EqualFold(d.Name(), filename) {
			result = filepath.Dir(path)
		}
		return nil
	})

	return result
}

func findFile(root string, filename string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}

	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return ""
	}

	direct := filepath.Join(root, filename)
	if fileExists(direct) {
		return direct
	}

	var result string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || result != "" {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if strings.EqualFold(d.Name(), filename) {
			result = path
		}
		return nil
	})

	return result
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}
