// SPDX-License-Identifier: AGPL-3.0-or-later

// Package fileset provides content hashing and Quadlet unit-file classification.
package fileset

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
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

// systemdUnitExts are native systemd unit types a cadre may ship to schedule or
// activate its Quadlet services (a `.timer` firing a generated `.service`, a
// `.socket`/`.path` activating one). Unlike Quadlet files, systemd does not read
// them from the Quadlet dir, so they are installed into the user unit directory
// (~/.config/systemd/user) and enabled directly.
var systemdUnitExts = map[string]bool{
	".timer": true, ".socket": true, ".path": true,
}

func IsSystemdUnit(name string) bool {
	return systemdUnitExts[filepath.Ext(name)]
}
