// SPDX-License-Identifier: AGPL-3.0-or-later

package sshx

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// defaultExecTimeout bounds a single remote command when Client.ExecTimeout is
// left unset, so a host that connects but stalls mid-command cannot hang a whole
// status sweep.
const defaultExecTimeout = 30 * time.Second

// maxCapturedOutput caps each captured stream (stdout, stderr). The exec timeout
// bounds time but not bytes, so without this a malicious node could stream
// gigabytes and OOM the operator during a parallel sweep. rucher's commands emit
// tiny output, so a few MiB is generous.
const maxCapturedOutput = 4 << 20 // 4 MiB per stream

// Client is the real SSH Runner.
type Client struct {
	KnownHosts  string        // path to a known_hosts file (TOFU accept-new); created if missing
	Timeout     time.Duration // dial + handshake timeout
	ExecTimeout time.Duration // per-command run timeout; zero -> defaultExecTimeout
}

func NewClient(knownHosts string, timeout time.Duration) *Client {
	return &Client{KnownHosts: knownHosts, Timeout: timeout}
}

// Run dials the target, opens a session, runs the joined command and captures
// its output. See Runner for the exit-code contract.
func (c *Client) Run(t Target, cmd []string, stdin []byte) (Result, error) {
	var methods []ssh.AuthMethod

	// Explicit identity: read + parse the key. A broken identity the caller
	// asked for is a hard error (returned before any dial).
	if t.Identity != "" {
		signer, err := loadIdentity(t.Identity)
		if err != nil {
			return Result{}, err
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}

	// Agent-backed auth, if an agent socket is exposed. The connection stays
	// open for the lifetime of the dial (the signers use it lazily) and is
	// closed on return.
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		if agConn, err := net.Dial("unix", sock); err == nil {
			defer agConn.Close()
			methods = append(methods, ssh.PublicKeysCallback(agent.NewClient(agConn).Signers))
		}
	}

	config := &ssh.ClientConfig{
		User:            t.User,
		Auth:            methods,
		HostKeyCallback: acceptNewHostKey(c.KnownHosts),
		Timeout:         c.Timeout,
	}

	// Prefer the key type already pinned for this host so negotiation lands on
	// the pinned key. Only constrain when the host IS pinned: on first contact
	// the list is empty, and setting an empty HostKeyAlgorithms would break
	// negotiation and defeat TOFU accept-new.
	if algos := pinnedHostKeyAlgorithms(c.KnownHosts, t.Addr); len(algos) > 0 {
		config.HostKeyAlgorithms = algos
	}

	conn, err := ssh.Dial("tcp", t.Addr, config)
	if err != nil {
		return Result{}, fmt.Errorf("ssh dial %s: %w", t.Addr, err)
	}
	defer conn.Close()

	session, err := conn.NewSession()
	if err != nil {
		return Result{}, fmt.Errorf("ssh session: %w", err)
	}
	defer session.Close()

	if stdin != nil {
		session.Stdin = bytes.NewReader(stdin)
	}
	out := &cappedBuffer{max: maxCapturedOutput}
	errb := &cappedBuffer{max: maxCapturedOutput}
	session.Stdout = out
	session.Stderr = errb

	execTO := c.ExecTimeout
	if execTO <= 0 {
		execTO = defaultExecTimeout
	}

	// The remote shell splits the joined string; this matches how the old
	// system-ssh path passed the joined argv. Run it in a goroutine so a host
	// that connects but never returns cannot block indefinitely.
	done := make(chan error, 1)
	go func() {
		done <- session.Run(strings.Join(cmd, " "))
	}()

	err, timedOut := waitRun(done, execTO)
	if timedOut {
		// Close the session and connection to unblock the pending session.Run;
		// the timeout is a transport-style failure (non-nil error) so callers
		// treat the host as unreachable.
		session.Close()
		conn.Close()
		return Result{}, fmt.Errorf("command timed out after %s", execTO)
	}

	res := Result{Stdout: out.String(), Stderr: errb.String()}

	var ee *ssh.ExitError
	if errors.As(err, &ee) {
		res.Code = ee.ExitStatus()
		return res, nil // non-zero remote exit is not a Go error
	}
	return res, err
}

// waitRun waits for a remote command to finish or for the timeout to elapse.
// It reports the run error (nil on success) and whether the wait timed out;
// both are decoupled from any real SSH server so the wait is unit-testable.
func waitRun(done <-chan error, timeout time.Duration) (err error, timedOut bool) {
	// NewTimer + Stop (rather than time.After) so the timer is released promptly
	// on the common done-first path instead of lingering until it fires.
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case err = <-done:
		return err, false
	case <-t.C:
		return nil, true
	}
}

// cappedBuffer is an io.Writer that accumulates at most max bytes and then fails
// the write, bounding the memory a single remote command's output can consume.
// The error propagates out of session.Run so the caller sees the host as failed;
// returning it also stops the io.Copy draining the ssh channel, throttling the
// remote instead of reading its stream to EOF.
type cappedBuffer struct {
	buf bytes.Buffer
	max int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if room := c.max - c.buf.Len(); len(p) > room {
		if room > 0 {
			c.buf.Write(p[:room]) // keep a truncated prefix for diagnostics
		}
		return room, fmt.Errorf("output exceeded %d bytes", c.max)
	}
	return c.buf.Write(p)
}

func (c *cappedBuffer) String() string { return c.buf.String() }

func loadIdentity(path string) (ssh.Signer, error) {
	keyBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read identity %s: %w", path, err)
	}
	signer, err := ssh.ParsePrivateKey(keyBytes)
	if err != nil {
		return nil, fmt.Errorf("parse identity %s: %w", path, err)
	}
	return signer, nil
}
