package sshx

import (
	"errors"
	"net"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// acceptNewHostKey returns a TOFU accept-new host-key callback backed by the
// known_hosts file at path: an unknown host is trusted and pinned on first
// contact, a later key change against a pinned entry is rejected.
func acceptNewHostKey(path string) ssh.HostKeyCallback {
	// Set up eagerly; the signature carries no error, so any failure is
	// surfaced when the callback is invoked.
	setupErr := ensureFile(path)
	var inner ssh.HostKeyCallback
	if setupErr == nil {
		inner, setupErr = knownhosts.New(path)
	}

	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		if setupErr != nil {
			return setupErr
		}
		err := inner(hostname, remote, key)
		if err == nil {
			return nil // key matches a pinned entry
		}
		var keyErr *knownhosts.KeyError
		if errors.As(err, &keyErr) {
			if len(keyErr.Want) == 0 {
				// Unknown host: pin it and accept (TOFU accept-new).
				return appendKnownHost(path, hostname, key)
			}
			return err // mismatch against a pinned key: reject
		}
		return err
	}
}

// ensureFile makes sure the known_hosts file (and its parent dir) exists so
// knownhosts.New does not fail on a fresh setup.
func ensureFile(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	return f.Close()
}

// appendKnownHost pins the presented key for hostname in the known_hosts file.
func appendKnownHost(path, hostname string, key ssh.PublicKey) error {
	line := knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(line + "\n")
	return err
}
