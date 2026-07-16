// SPDX-License-Identifier: AGPL-3.0-or-later

// Package fileset provides content hashing and Quadlet unit-file classification.
package fileset

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
)

func Hash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// unitExts are the Quadlet file extensions the podman generator understands.
var unitExts = map[string]bool{
	".container": true, ".volume": true, ".network": true,
	".pod": true, ".kube": true, ".image": true, ".build": true,
}

func IsUnitFile(name string) bool {
	return unitExts[filepath.Ext(name)]
}

// systemdUnitExts are native systemd unit types a cadre may ship. Unlike Quadlet
// files, systemd does not read them from the Quadlet dir, so they are installed
// into the user unit directory (~/.config/systemd/user) where its user manager
// looks for them. `.timer`/`.socket`/`.path` schedule or activate a cadre's
// services; a `.service` lets a cadre ship its own unit (e.g. a oneshot fired by a
// companion timer, like the synthesized prune). Membership here is about routing
// only — whether such a unit is *enabled* is decided separately by ShouldEnable.
var systemdUnitExts = map[string]bool{
	".timer": true, ".socket": true, ".path": true, ".service": true,
}

func IsSystemdUnit(name string) bool {
	return systemdUnitExts[filepath.Ext(name)]
}

// ShouldEnable reports whether rucher enables this native systemd unit directly
// (`enable --now`, restart on change, `disable --now` on removal). Activator units
// (.timer/.socket/.path) are always enabled: they carry an [Install] section and
// fire or activate a companion service. A .service is enabled only when it ships an
// [Install] section; without one it is an install-only oneshot — written and
// daemon-reloaded but never enabled, activated by a companion unit (exactly how the
// synthesized [Install]-less prune .service works).
func ShouldEnable(name string, content []byte) bool {
	switch filepath.Ext(name) {
	case ".timer", ".socket", ".path":
		return true
	case ".service":
		return hasSection(content, "Install")
	}
	return false
}

// hasSection reports whether content has a [name] section header. systemd section
// names are case-sensitive, so name is matched exactly.
func hasSection(content []byte, name string) bool {
	sc := bufio.NewScanner(bytes.NewReader(content))
	// Cap a scanned line well above the default 64KB so a long (but legitimate)
	// line does not silently truncate the scan and hide a later [Install].
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if s, ok := strings.CutPrefix(line, "["); ok {
			if s, ok := strings.CutSuffix(s, "]"); ok && s == name {
				return true
			}
		}
	}
	return false
}

// UnitService maps a Quadlet unit filename to its generated .service name.
func UnitService(unit string) string {
	dot := strings.LastIndex(unit, ".")
	if dot < 0 {
		return unit // already a bare/service-style name; nothing to map
	}
	stem, ext := unit[:dot], unit[dot+1:]
	switch ext {
	case "container", "kube":
		// Quadlet names a .container's and a .kube's service after the bare stem
		// (foo.service); .volume/.network/.pod/.image/.build get the -<ext> suffix.
		return stem + ".service"
	default:
		return stem + "-" + ext + ".service"
	}
}

// Per-cadre unit names rucher synthesizes for image GC (see internal/prune),
// reserved so operator-shipped files cannot collide with them.
const (
	PruneTimer   = "rucher-prune.timer"
	PruneService = "rucher-prune.service"
)

// IsReserved reports whether name is claimed by a synthesized unit.
func IsReserved(name string) bool {
	return name == PruneTimer || name == PruneService
}
