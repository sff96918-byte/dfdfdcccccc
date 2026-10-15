package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

func EnsureDir(path string) error {
	return os.MkdirAll(path, 0o755)
}

func SaveJSON(path string, v any) error {
	if err := EnsureDir(filepath.Dir(path)); err != nil {
		return err
	}

	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0o644)
}

func LoadJSON(path string, dest any) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}

	if err := json.Unmarshal(data, dest); err != nil {
		return false, err
	}

	return true, nil
}
