// SPDX-License-Identifier: AGPL-3.0-or-later

package sshx

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// knownHostsMu serializes access to the known_hosts file so concurrent per-node
// SSH (operator plane) cannot corrupt it on first-contact TOFU pinning. It
// guards only the short file read/write sections below — never an ssh.Dial, so
// handshakes still run in parallel.
var knownHostsMu sync.Mutex

// acceptNewHostKey returns a TOFU accept-new host-key callback backed by the
// known_hosts file at path: an unknown host is trusted and pinned on first
// contact, a later key change against a pinned entry is rejected.
func acceptNewHostKey(path string) ssh.HostKeyCallback {
	// Set up eagerly; the signature carries no error, so any failure is
	// surfaced when the callback is invoked. The setup reads/creates the file,
	// so hold the lock for it — but not for the returned callback, which runs
	// during the (concurrent) handshake and only locks its brief append.
	knownHostsMu.Lock()
	setupErr := ensureFile(path)
	var inner ssh.HostKeyCallback
	if setupErr == nil {
		inner, setupErr = knownhosts.New(path)
	}
	knownHostsMu.Unlock()

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

// probeAddr is a net.Addr backed by a raw "host:port" string, used to query the
// known_hosts callback for an address whose host may be a name (not an IP).
type probeAddr string

func (a probeAddr) Network() string { return "tcp" }
func (a probeAddr) String() string  { return string(a) }

// pinnedHostKeyAlgorithms returns the host-key algorithms already pinned for
// addr ("host:port") in the known_hosts file, so a reconnect can constrain
// negotiation to the pinned key type.
//
// x/crypto's knownhosts (v0.24) exposes no direct "algorithms for addr" call,
// so we ask its callback: probing with a throwaway key that cannot match any
// pinned entry yields a *knownhosts.KeyError whose Want holds every host key
// already pinned for addr (one KnownKey per algorithm). This reuses the
// package's own host matching.
//
// The result is EMPTY when the host is not yet pinned (first contact). Callers
// MUST leave ssh.ClientConfig.HostKeyAlgorithms unset in that case: an empty
// constraint would break negotiation and defeat TOFU accept-new.
func pinnedHostKeyAlgorithms(knownHostsPath, addr string) []string {
	knownHostsMu.Lock()
	defer knownHostsMu.Unlock()
	if err := ensureFile(knownHostsPath); err != nil {
		return nil
	}
	cb, err := knownhosts.New(knownHostsPath)
	if err != nil {
		return nil
	}
	probe := throwawayHostKey()
	if probe == nil {
		return nil
	}

	err = cb(addr, probeAddr(addr), probe)
	if err == nil {
		return nil // improbable key collision: treat as no constraint
	}
	var keyErr *knownhosts.KeyError
	if !errors.As(err, &keyErr) || len(keyErr.Want) == 0 {
		return nil // unknown host (first contact) or an unexpected error
	}

	seen := make(map[string]bool, len(keyErr.Want))
	var algos []string
	add := func(a string) {
		if !seen[a] {
			seen[a] = true
			algos = append(algos, a)
		}
	}
	for _, k := range keyErr.Want {
		switch typ := k.Key.Type(); typ {
		case ssh.KeyAlgoRSA:
			// A pinned RSA host key also serves the SHA-2 signature algorithms;
			// offer those first so a modern server that disables the legacy
			// SHA-1 "ssh-rsa" algorithm still negotiates on reconnect.
			add(ssh.KeyAlgoRSASHA512)
			add(ssh.KeyAlgoRSASHA256)
			add(ssh.KeyAlgoRSA)
		default:
			add(typ)
		}
	}
	return algos
}

// throwawayHostKey returns a fresh ed25519 public key used only to probe the
// known_hosts callback; it is never persisted and (statistically) never matches
// a pinned entry. Returns nil if key generation fails.
func throwawayHostKey() ssh.PublicKey {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil
	}
	return sshPub
}

// appendKnownHost pins the presented key for hostname in the known_hosts file.
func appendKnownHost(path, hostname string, key ssh.PublicKey) error {
	line := knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key)
	knownHostsMu.Lock()
	defer knownHostsMu.Unlock()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(line + "\n")
	return err
}
