package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
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

	// Atomic replace: a crash, a full disk or a killed process can otherwise
	// leave a truncated file behind, and readers treat invalid JSON as a fatal
	// error (state, login and registry files are all read that way).
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		_ = os.Remove(tempName)
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		_ = os.Remove(tempName)
		return err
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempName)
		return err
	}

	// Windows can refuse the rename while a reader, indexer or antivirus holds
	// the target open; retrying briefly is enough and never leaves a partial file.
	var renameErr error
	for attempt := 0; attempt < 3; attempt++ {
		if renameErr = os.Rename(tempName, path); renameErr == nil {
			return nil
		}
		time.Sleep(time.Duration(attempt+1) * 50 * time.Millisecond)
	}
	_ = os.Remove(tempName)
	return renameErr
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
