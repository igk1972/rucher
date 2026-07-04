// Package secrets decrypts a SOPS+age file to an in-memory key/value map.
package secrets

import (
	"encoding/json"
	"fmt"

	"podman-essaim-compartment-manager/internal/fileset"
	"podman-essaim-compartment-manager/internal/host"
)

func Decrypt(r host.Runner, user string, uid int, identityPath, sopsPath string) (map[string]string, error) {
	// Pass the age identity via env; sops reads it, plaintext returns on stdout only.
	argv := []string{
		"env", "SOPS_AGE_KEY_FILE=" + identityPath,
		"sops", "-d", "--output-type", "json", sopsPath,
	}
	res, err := r.User(user, uid, argv, nil)
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
