// Package state persists the last-applied state of a cadre (hashes only).
package state

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"rucher/internal/manifest"
)

type State struct {
	Name         string             `json:"name"`
	UID          int                `json:"uid"`
	Files        map[string]string  `json:"files"`
	SecretHashes map[string]string  `json:"secretHashes"`
	Units        []string           `json:"units"`
	Resources    manifest.Resources `json:"resources"`
}

func empty() State {
	return State{
		Files:        map[string]string{},
		SecretHashes: map[string]string{},
	}
}

func Load(path string) (State, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return empty(), nil
	}
	if err != nil {
		return State{}, err
	}
	s := empty()
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}, err
	}
	return s, nil
}

func Save(path string, s State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
