// Package node manages the node's own age key (born on the node, private key never leaves).
package node

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"rucher/internal/age"
)

const IdentityPath = "/etc/rucher/node/identity.txt"

func Identity(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func Recipient(path string) (string, error) {
	id, err := Identity(path)
	if err != nil {
		return "", err
	}
	return age.RecipientFor(id)
}

// EnsureIdentity creates the node key on first use and returns its recipient.
func EnsureIdentity(path string) (string, error) {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		id, rcpt, err := age.GenerateIdentity()
		if err != nil {
			return "", err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return "", err
		}
		if err := os.WriteFile(path, []byte(id+"\n"), 0o600); err != nil {
			return "", err
		}
		return rcpt, nil
	}
	return Recipient(path)
}
