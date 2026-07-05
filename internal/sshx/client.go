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

// Client is the real SSH Runner.
type Client struct {
	KnownHosts string        // path to a known_hosts file (TOFU accept-new); created if missing
	Timeout    time.Duration // dial + handshake timeout
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
	var out, errb bytes.Buffer
	session.Stdout = &out
	session.Stderr = &errb

	// The remote shell splits the joined string; this matches how the old
	// system-ssh path passed the joined argv.
	err = session.Run(strings.Join(cmd, " "))
	res := Result{Stdout: out.String(), Stderr: errb.String()}

	var ee *ssh.ExitError
	if errors.As(err, &ee) {
		res.Code = ee.ExitStatus()
		return res, nil // non-zero remote exit is not a Go error
	}
	return res, err
}

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
