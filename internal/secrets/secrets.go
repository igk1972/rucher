// Package secrets decrypts a SOPS+age file to an in-memory key/value map.
package secrets

import (
	"encoding/json"
	"fmt"

	"podman-essaim-compartment-manager/internal/fileset"
	"podman-essaim-compartment-manager/internal/host"
)

func Decrypt(r host.Runner, user string, uid int, identityPath string, ciphertext []byte) (map[string]string, error) {
	// Ciphertext is fed on stdin so the compartment user never needs read access
	// to the (root-owned) source directory; only its own age identity decrypts it.
	argv := []string{
		"env", "SOPS_AGE_KEY_FILE=" + identityPath,
		"sops", "-d", "--input-type", "yaml", "--output-type", "json", "/dev/stdin",
	}
	res, err := r.User(user, uid, argv, ciphertext)
	if err != nil {
		return nil, fmt.Errorf("sops decrypt: %w", err)
	}
	if res.Code != 0 {
		return nil, fmt.Errorf("sops decrypt exited %d: %s", res.Code, res.Stderr)
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
