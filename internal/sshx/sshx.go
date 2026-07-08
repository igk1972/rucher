// SPDX-License-Identifier: AGPL-3.0-or-later

// Package sshx is a native SSH client (golang.org/x/crypto/ssh) that replaces
// shelling out to the system ssh binary. It runs a single remote command and
// reports stdout/stderr plus the remote exit code, using TOFU accept-new host
// key verification backed by a known_hosts file.
package sshx

// Target describes how to reach a host over SSH.
type Target struct {
	Addr     string // "host:port"
	User     string
	Identity string // path to a private key file; may be empty (then rely on the agent)
}

type Result struct {
	Stdout string
	Stderr string
	Code   int
}

// Runner runs a remote command over SSH and returns its output + exit code.
//
// A non-zero remote exit is reported via Result.Code with a nil error; only
// dial/auth/transport/session-setup failures yield a non-nil error. This lets
// callers treat `err != nil || res.Code != 0` as "unreachable".
type Runner interface {
	Run(t Target, cmd []string, stdin []byte) (Result, error)
}
