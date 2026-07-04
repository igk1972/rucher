// Package compartment loads a compartment definition from a directory.
package compartment

import (
	"fmt"
	"os"
	"path/filepath"

	"podman-essaim-compartment-manager/internal/fileset"
	"podman-essaim-compartment-manager/internal/manifest"
)

type File struct {
	Name    string
	Content []byte
	Hash    string
	IsUnit  bool
}

type Compartment struct {
	Name     string
	Dir      string
	Manifest manifest.Manifest
	Files    []File
	SopsPath string
}

func Load(dir string) (Compartment, error) {
	mdata, err := os.ReadFile(filepath.Join(dir, "compartment.yml"))
	if err != nil {
		return Compartment{}, fmt.Errorf("read manifest: %w", err)
	}
	m, err := manifest.Load(mdata)
	if err != nil {
		return Compartment{}, err
	}
	if err := m.Validate(); err != nil {
		return Compartment{}, err
	}
	if base := filepath.Base(dir); m.Name != base {
		return Compartment{}, fmt.Errorf("manifest name %q != directory %q", m.Name, base)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return Compartment{}, err
	}
	// service files that must never be materialized into the compartment
	service := map[string]bool{
		"compartment.yml": true,
		m.Secrets.From:    true,
		".sops.yaml":      true,
	}
	c := Compartment{Name: m.Name, Dir: dir, Manifest: m}
	for _, e := range entries {
		if e.IsDir() || service[e.Name()] {
			if e.Name() == m.Secrets.From {
				c.SopsPath = filepath.Join(dir, e.Name())
			}
			continue
		}
		content, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return Compartment{}, err
		}
		c.Files = append(c.Files, File{
			Name:    e.Name(),
			Content: content,
			Hash:    fileset.Hash(content),
			IsUnit:  fileset.IsUnitFile(e.Name()),
		})
	}
	return c, nil
}
