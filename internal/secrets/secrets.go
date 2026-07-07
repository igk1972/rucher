// Package secrets decrypts a SOPS+age file to an in-memory key/value map.
package secrets

import (
	"fmt"
	"os"

	"rucher/internal/fileset"
	"rucher/internal/sopsage"
)

// Decrypt decrypts a cadre's SOPS+age file into an in-memory key/value map,
// entirely in-process (no external `sops` binary). It runs on the node as root,
// which can read both the root-owned SOPS file and the cadre user's 0600 age
// identity. Plaintext stays in the agent's memory and is fed to podman over
// stdin; the per-cadre identity scopes at-rest access in the store, not runtime
// access on the host (root already sees every cadre's secrets).
func Decrypt(identityPath, sopsPath string) (map[string]string, error) {
	idData, err := os.ReadFile(identityPath)
	if err != nil {
		return nil, fmt.Errorf("read cadre age identity: %w", err)
	}
	sopsData, err := os.ReadFile(sopsPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", sopsPath, err)
	}
	m, err := sopsage.Decrypt(idData, sopsData)
	if err != nil {
		return nil, fmt.Errorf("decrypt %s: %w", sopsPath, err)
	}
	return m, nil
}

func Hashes(values map[string]string) map[string]string {
	out := make(map[string]string, len(values))
	for k, v := range values {
		out[k] = fileset.Hash([]byte(v))
	}
	return out
}
