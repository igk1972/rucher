// Package secrets decrypts a SOPS+age file to an in-memory key/value map.
package secrets

import (
	"encoding/json"
	"fmt"

	"rucher/internal/fileset"
	"rucher/internal/node"
)

func Decrypt(r node.Runner, identityPath, sopsPath string) (map[string]string, error) {
	// Decrypt as root (the agent): root can read both the root-owned source file and
	// the compartment user's age identity. Plaintext stays in the agent's memory and
	// is fed to podman via stdin. The per-compartment identity scopes at-rest access
	// in the store, not runtime access on the host (root already sees all secrets).
	argv := []string{
		"env", "SOPS_AGE_KEY_FILE=" + identityPath,
		"sops", "-d", "--output-type", "json", sopsPath,
	}
	res, err := r.Root(argv, nil)
	if err != nil {
		return nil, fmt.Errorf("sops decrypt: %w", err)
	}
	if res.Code != 0 {
		return nil, fmt.Errorf("sops decrypt %s exited %d: %s", sopsPath, res.Code, res.Stderr)
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(res.Stdout), &m); err != nil {
		return nil, fmt.Errorf("parse decrypted secrets: %w", err)
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
