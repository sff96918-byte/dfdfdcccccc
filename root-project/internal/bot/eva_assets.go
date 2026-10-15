package bot

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// evaAssets is the EVA chat TXT database (input triggers + output reply
// pools + countries). It is copied only when a file does not already exist in
// data/eva_chat, so user edits are never overwritten.
//
//go:embed eva_assets/README.txt eva_assets/countries.txt eva_assets/persona.txt eva_assets/persona_countries.txt eva_assets/input/*.txt eva_assets/input/middle_chat/*.txt eva_assets/output/*.txt eva_assets/output/middle_chat/*.txt
var evaAssets embed.FS

// EnsureEVAChatData creates the editable EVAChat Flow data directory when it
// is absent and returns its absolute runtime path.
func EnsureEVAChatData(dataDir string) (string, error) {
	root := filepath.Join(dataDir, "eva_chat")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("create EVAChat data directory: %w", err)
	}

	err := fs.WalkDir(evaAssets, "eva_assets", func(assetPath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if assetPath == "eva_assets" {
			return nil
		}

		rel := strings.TrimPrefix(assetPath, "eva_assets/")
		destination := filepath.Join(root, filepath.FromSlash(rel))
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o755)
		}
		if _, err := os.Stat(destination); err == nil {
			return nil
		} else if !os.IsNotExist(err) {
			return err
		}

		data, err := evaAssets.ReadFile(assetPath)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return err
		}
		return os.WriteFile(destination, data, 0o644)
	})
	if err != nil {
		return "", fmt.Errorf("bootstrap EVAChat starter data: %w", err)
	}
	return root, nil
}
