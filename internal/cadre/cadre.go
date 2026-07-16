// SPDX-License-Identifier: AGPL-3.0-or-later

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
	IsSystemdUnit bool // routed to ~/.config/systemd/user/ (.timer/.socket/.path, plus synthesized units)
}

type Cadre struct {
	Name     string
	Dir      string
	Manifest manifest.Manifest
	Files    []File
	SopsPath string
}

func Load(dir string) (Cadre, error) {
	// Lstat before read so a symlinked rucher.yml cannot redirect the (root) agent's read at
	// an arbitrary node file; the support-file loop below guards the rest of the directory.
	mpath := filepath.Join(dir, "rucher.yml")
	if fi, err := os.Lstat(mpath); err != nil {
		return Cadre{}, fmt.Errorf("read manifest: %w", err)
	} else if !fi.Mode().IsRegular() {
		return Cadre{}, fmt.Errorf("manifest rucher.yml must be a regular file")
	}
	mdata, err := os.ReadFile(mpath)
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
	// A cadre's identity is its directory name; the manifest carries no name.
	name := filepath.Base(dir)

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
	c := Cadre{Name: name, Dir: dir, Manifest: m}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		// Reject a symlink or any non-regular entry: a malicious store could point one at a
		// root-only node file (e.g. the node's private age key) and the agent reads cadre files
		// as root, so os.ReadFile would follow the link. Error rather than skip — a skipped
		// support file referenced by a unit would fail validation and mask the tampering.
		if !e.Type().IsRegular() {
			return Cadre{}, fmt.Errorf("cadre entry %s must be a regular file", e.Name())
		}
		if service[e.Name()] || isServiceFile(e.Name()) {
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
	// A missing secrets file (SopsPath == "") is normal for a cadre with no secrets, but
	// if the manifest declares secrets it is almost certainly a typo in secrets.from or a
	// file that was not committed. Erroring here beats Apply silently deleting every podman
	// secret (plan sees "no secrets desired") and leaving containers to fail on next start.
	if c.SopsPath == "" {
		if len(c.Manifest.Secrets.Create) > 0 {
			return fmt.Errorf("secrets.create lists keys but the secrets file %q is not present", c.Manifest.Secrets.From)
		}
		if len(c.Manifest.Registries.Login) > 0 {
			return fmt.Errorf("registries.login needs a passwordKey from the secrets file %q, which is not present", c.Manifest.Secrets.From)
		}
	}
	have := map[string]bool{}
	// generated maps each Quadlet-generated .service name back to its source unit, so a
	// hand-written .service that would shadow one is caught below.
	generated := map[string]string{}
	for _, f := range c.Files {
		if fileset.IsReserved(f.Name) {
			return fmt.Errorf("file %s: reserved for the synthesized prune units (configure them via the manifest prune: block)", f.Name)
		}
		// A leading '-' makes the (unit) name look like a flag to systemctl/podman.
		if strings.HasPrefix(f.Name, "-") {
			return fmt.Errorf("file %s: name must not start with '-'", f.Name)
		}
		have[f.Name] = true
		if f.IsUnit {
			gen := fileset.UnitService(f.Name)
			if fileset.IsReserved(gen) {
				return fmt.Errorf("file %s: generates %s, reserved for the synthesized prune units", f.Name, gen)
			}
			generated[gen] = f.Name
		}
	}
	// A cadre .service must not shadow the .service Quadlet generates from one of its units:
	// the user unit dir outranks Quadlet's generator output in systemd's unit load path, so an
	// operator web.service would silently mask the one generated from web.container. The
	// generated map must be complete first — a suffixed name like db-pod.service sorts before
	// its db.pod source.
	for _, f := range c.Files {
		if filepath.Ext(f.Name) != ".service" {
			continue
		}
		if src, ok := generated[f.Name]; ok {
			return fmt.Errorf("file %s: collides with the .service Quadlet generates from %s (rename it or configure that unit instead)", f.Name, src)
		}
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

// requiredSection is the type section the Quadlet generator demands per file
// extension; a unit without it fails generation on the node.
var requiredSection = map[string]string{
	".container": "Container", ".volume": "Volume", ".network": "Network",
	".pod": "Pod", ".kube": "Kube", ".image": "Image", ".build": "Build",
}

// maxUnitLine caps a scanned unit line well above the default 64KB, so a long
// (but legitimate) line does not silently truncate the scan and drop refs.
const maxUnitLine = 1 << 20

func validateUnit(f File, have map[string]bool) error {
	hasSection := false
	sections := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(f.Content))
	sc.Buffer(nil, maxUnitLine)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			hasSection = true
			if name, ok := strings.CutSuffix(line[1:], "]"); ok {
				sections[name] = true
			}
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
	if err := sc.Err(); err != nil {
		return fmt.Errorf("scan unit %s: %w", f.Name, err)
	}
	if !hasSection {
		return fmt.Errorf("unit %s is empty or has no [Section] header", f.Name)
	}
	if req, ok := requiredSection[filepath.Ext(f.Name)]; ok && !sections[req] {
		return fmt.Errorf("unit %s has no [%s] section (required by the Quadlet generator)", f.Name, req)
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
