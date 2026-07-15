// SPDX-License-Identifier: AGPL-3.0-or-later

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
	Units        []string           `json:"units"`                  // Quadlet units
	SystemdUnits []string           `json:"systemdUnits,omitempty"` // native .timer/.socket/.path
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
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	// Unique temp name (not a fixed path+".tmp") so two writers can never share and
	// corrupt the same scratch file; rename into place is atomic.
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename below succeeds
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	// fsync before the atomic rename: without it a crash right after Save can leave the target
	// pointing at a zero-length or partially written file (rename is durable, its bytes are not).
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
