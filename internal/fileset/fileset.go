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
