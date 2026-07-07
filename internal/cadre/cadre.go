// Package cadre loads a cadre definition from a directory.
package cadre

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"rucher/internal/fileset"
	"rucher/internal/manifest"
)

type File struct {
	Name          string
	Content       []byte
	Hash          string
	IsUnit        bool // Quadlet unit -> ~/.config/containers/systemd/
	IsSystemdUnit bool // native systemd unit (.timer/.socket/.path) -> ~/.config/systemd/user/
}

type Cadre struct {
	Name     string
	Dir      string
	Manifest manifest.Manifest
	Files    []File
	SopsPath string
}

func Load(dir string) (Cadre, error) {
	mdata, err := os.ReadFile(filepath.Join(dir, "rucher.yml"))
	if err != nil {
		return Cadre{}, fmt.Errorf("read manifest: %w", err)
	}
	m, err := manifest.Load(mdata)
	if err != nil {
		return Cadre{}, err
	}
	if err := m.Validate(); err != nil {
		return Cadre{}, err
	}
	if base := filepath.Base(dir); m.Name != base {
		return Cadre{}, fmt.Errorf("manifest name %q != directory %q", m.Name, base)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return Cadre{}, err
	}
	// service files that must never be materialized into the cadre
	service := map[string]bool{
		"rucher.yml":   true,
		m.Secrets.From: true,
	}
	// A SOPS file (anything ending .sops.yaml) and a sealed age identity
	// (identity.age / identity.<node>.age) are service files, not support files, so they
	// must never be materialized onto the node.
	isServiceFile := func(name string) bool {
		if strings.HasSuffix(name, ".sops.yaml") {
			return true
		}
		return strings.HasPrefix(name, "identity.") && strings.HasSuffix(name, ".age")
	}
	c := Cadre{Name: m.Name, Dir: dir, Manifest: m}
	for _, e := range entries {
		if e.IsDir() || service[e.Name()] || isServiceFile(e.Name()) {
			if e.Name() == m.Secrets.From {
				c.SopsPath = filepath.Join(dir, e.Name())
			}
			continue
		}
		content, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return Cadre{}, err
		}
		c.Files = append(c.Files, File{
			Name:          e.Name(),
			Content:       content,
			Hash:          fileset.Hash(content),
			IsUnit:        fileset.IsUnitFile(e.Name()),
			IsSystemdUnit: fileset.IsSystemdUnit(e.Name()),
		})
	}
	if err := c.Validate(); err != nil {
		return Cadre{}, err
	}
	return c, nil
}

// systemdUnitDir is the per-user Quadlet drop-in directory that a cadre's
// support files are materialized into; an EnvironmentFile value under this
// prefix must resolve to a file the cadre ships.
const systemdUnitDir = "%h/.config/containers/systemd/"

// Validate rejects only the subset of problems that cannot false-positive:
// a broken unit file, or an EnvironmentFile referencing a cadre-local
// file that is not present. It deliberately does not check secret keys (need
// decrypted secrets) or resource-limit formats (systemd accepts many forms).
func (c Cadre) Validate() error {
	have := map[string]bool{}
	for _, f := range c.Files {
		have[f.Name] = true
	}
	for _, f := range c.Files {
		if !f.IsUnit && !f.IsSystemdUnit {
			continue
		}
		if err := validateUnit(f, have); err != nil {
			return err
		}
	}
	return nil
}

func validateUnit(f File, have map[string]bool) error {
	hasSection := false
	sc := bufio.NewScanner(bytes.NewReader(f.Content))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			hasSection = true
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != "EnvironmentFile" {
			continue
		}
		name, ok := localEnvFile(strings.TrimSpace(val))
		if !ok {
			continue
		}
		if !have[name] {
			return fmt.Errorf("unit %s references missing EnvironmentFile %s", f.Name, name)
		}
	}
	if !hasSection {
		return fmt.Errorf("unit %s is empty or has no [Section] header", f.Name)
	}
	return nil
}

// localEnvFile resolves an EnvironmentFile value to a cadre-local
// basename. It reports false when the value cannot be validated: an optional
// ("-"-prefixed) reference, or a path outside the cadre's unit dir.
func localEnvFile(val string) (string, bool) {
	if val == "" || strings.HasPrefix(val, "-") {
		return "", false
	}
	if rest, ok := strings.CutPrefix(val, systemdUnitDir); ok {
		return filepath.Base(rest), true
	}
	if !strings.Contains(val, "/") {
		return val, true // bare relative filename lands in the unit dir
	}
	return "", false // absolute or other path: can't validate
}
