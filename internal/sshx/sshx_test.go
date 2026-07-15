// SPDX-License-Identifier: AGPL-3.0-or-later

package sshx

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// newTestHostKey generates a fresh ed25519 key pair and returns its ssh.PublicKey.
func newTestHostKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("new public key: %v", err)
	}
	return sshPub
}

func TestAcceptNewTOFU(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "known_hosts")
	const host = "127.0.0.1:22"
	remote := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 22}
	key1 := newTestHostKey(t)

	// First contact: unknown host -> accept and persist (TOFU accept-new).
	if err := acceptNewHostKey(path)(host, remote, key1); err != nil {
		t.Fatalf("first contact should accept, got %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read known_hosts: %v", err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		t.Fatal("known_hosts is empty after accept-new")
	}

	// Second contact with the SAME key: verified from file (fresh callback re-reads).
	if err := acceptNewHostKey(path)(host, remote, key1); err != nil {
		t.Fatalf("same key should verify, got %v", err)
	}

	// Third contact with a DIFFERENT key: mismatch -> reject.
	key2 := newTestHostKey(t)
	if err := acceptNewHostKey(path)(host, remote, key2); err == nil {
		t.Fatal("mismatched key should be rejected")
	}
}

func TestPinnedHostKeyAlgorithmsFirstContact(t *testing.T) {
	// A nonexistent known_hosts path: the host is not yet pinned, so the
	// algorithm list must be empty and the caller leaves HostKeyAlgorithms
	// unset (otherwise TOFU accept-new on first contact would break).
	missing := filepath.Join(t.TempDir(), "sub", "known_hosts")
	if algos := pinnedHostKeyAlgorithms(missing, "127.0.0.1:2222"); len(algos) != 0 {
		t.Fatalf("first contact must yield no algorithms, got %v", algos)
	}

	// An existing but empty known_hosts file: still no pinned key -> empty.
	empty := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatalf("write empty known_hosts: %v", err)
	}
	if algos := pinnedHostKeyAlgorithms(empty, "127.0.0.1:2222"); len(algos) != 0 {
		t.Fatalf("empty known_hosts must yield no algorithms, got %v", algos)
	}
}

func TestPinnedHostKeyAlgorithmsAfterPin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known_hosts")
	const host = "127.0.0.1:2222"
	remote := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 2222}
	key := newTestHostKey(t)

	// Pin the host key via the TOFU accept-new path.
	if err := acceptNewHostKey(path)(host, remote, key); err != nil {
		t.Fatalf("pin host key: %v", err)
	}

	algos := pinnedHostKeyAlgorithms(path, host)
	if len(algos) == 0 {
		t.Fatal("a pinned host must yield a non-empty algorithm list")
	}
	found := false
	for _, a := range algos {
		if a == key.Type() {
			found = true
		}
	}
	if !found {
		t.Fatalf("algorithms %v must contain the pinned key type %q", algos, key.Type())
	}
}

func TestPinnedHostKeyAlgorithmsRSAExpansion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known_hosts")
	const host = "127.0.0.1:2200"
	remote := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 2200}

	// Pin an RSA host key via the TOFU accept-new path.
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	pub, err := ssh.NewPublicKey(&rsaKey.PublicKey)
	if err != nil {
		t.Fatalf("new rsa public key: %v", err)
	}
	if err := acceptNewHostKey(path)(host, remote, pub); err != nil {
		t.Fatalf("pin rsa host key: %v", err)
	}

	// A pinned bare "ssh-rsa" must expand to the SHA-2 algorithms (offered first)
	// so a modern server that disabled the SHA-1 ssh-rsa algorithm still
	// negotiates on reconnect.
	got := pinnedHostKeyAlgorithms(path, host)
	want := []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA}
	if !slices.Equal(got, want) {
		t.Fatalf("rsa expansion = %v, want %v", got, want)
	}
}

func TestFakeRunKeyed(t *testing.T) {
	tgt := Target{Addr: "h:22"}
	f := &Fake{Responses: map[string]Result{
		Key(tgt, []string{"cat", "/x"}): {Stdout: "ok"},
	}}

	res, err := f.Run(tgt, []string{"cat", "/x"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Stdout != "ok" {
		t.Fatalf("Stdout = %q, want %q", res.Stdout, "ok")
	}
	if len(f.Calls) != 1 || f.Calls[0].Target != tgt {
		t.Fatalf("call not recorded: %+v", f.Calls)
	}

	// Missing key -> zero Result, nil error.
	miss, err := f.Run(tgt, []string{"missing"}, nil)
	if err != nil {
		t.Fatalf("unexpected error on miss: %v", err)
	}
	if miss != (Result{}) {
		t.Fatalf("missing key should give zero Result, got %+v", miss)
	}
}

func TestWaitRunReturnsResult(t *testing.T) {
	sentinel := errors.New("remote failed")
	done := make(chan error, 1)
	done <- sentinel // available immediately

	err, timedOut := waitRun(done, time.Second)
	if timedOut {
		t.Fatal("should not time out when done receives")
	}
	if err != sentinel {
		t.Fatalf("err = %v, want %v", err, sentinel)
	}
}

func TestWaitRunTimesOut(t *testing.T) {
	done := make(chan error) // never receives

	err, timedOut := waitRun(done, 20*time.Millisecond)
	if !timedOut {
		t.Fatal("should time out when done never receives")
	}
	if err != nil {
		t.Fatalf("timeout err should be nil, got %v", err)
	}
}

func TestCappedBufferErrorsPastCap(t *testing.T) {
	c := &cappedBuffer{max: 10}

	// A write within the cap is accepted verbatim.
	if n, err := c.Write([]byte("hello")); err != nil || n != 5 {
		t.Fatalf("under-cap write = (%d, %v), want (5, nil)", n, err)
	}

	// A write crossing the cap fails with a clear error and keeps only what fit.
	n, err := c.Write([]byte("world!!!")) // 5 + 8 = 13 > 10
	if err == nil {
		t.Fatal("write past the cap must error")
	}
	if !strings.Contains(err.Error(), "output exceeded 10 bytes") {
		t.Fatalf("err = %v, want it to mention the byte cap", err)
	}
	if n != 5 {
		t.Fatalf("accepted %d bytes past the cap, want the 5 that still fit", n)
	}
	if got := c.String(); len(got) > 10 || got != "helloworld" {
		t.Fatalf("captured = %q, want it truncated at the 10-byte cap", got)
	}
}

func TestCappedBufferStopsIOCopy(t *testing.T) {
	// io.Copy from a large source (a session's stdout stream is exactly this)
	// must terminate with the cap error rather than reading to EOF, and never
	// hold more than the cap in memory.
	const cap = 1 << 10
	c := &cappedBuffer{max: cap}
	src := bytes.NewReader(make([]byte, 1<<20)) // 1 MiB into a 1 KiB cap
	if _, err := io.Copy(c, src); err == nil {
		t.Fatal("io.Copy into a capped buffer must stop with an error")
	}
	if got := len(c.String()); got != cap {
		t.Fatalf("captured %d bytes, want exactly the %d-byte cap", got, cap)
	}
}

func TestKeyDistinguishesUserAndIdentity(t *testing.T) {
	cmd := []string{"cat", "/x"}
	base := Target{Addr: "h:22", User: "root", Identity: "/k"}

	if Key(base, cmd) == Key(Target{Addr: "h:22", User: "app", Identity: "/k"}, cmd) {
		t.Fatal("same Addr but different User must produce different keys")
	}
	if Key(base, cmd) == Key(Target{Addr: "h:22", User: "root", Identity: "/other"}, cmd) {
		t.Fatal("same Addr but different Identity must produce different keys")
	}
	if Key(base, cmd) != Key(base, cmd) {
		t.Fatal("identical targets must produce identical keys")
	}
}

func TestClientRunMissingIdentity(t *testing.T) {
	c := NewClient(filepath.Join(t.TempDir(), "known_hosts"), 0)
	// Identity points at a nonexistent file: Run must error before dialing.
	_, err := c.Run(Target{Addr: "127.0.0.1:0", User: "x", Identity: "/no/such/key"}, []string{"true"}, nil)
	if err == nil {
		t.Fatal("expected error for unreadable identity")
	}
	// Pin the failure to the read-identity path (before dial), not any error.
	if !strings.Contains(err.Error(), "read identity") {
		t.Fatalf("err = %v, want it to mention the read-identity wrap", err)
	}
}

func TestClientRunMalformedIdentity(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "id_garbage")
	if err := os.WriteFile(keyPath, []byte("this is not a private key"), 0o600); err != nil {
		t.Fatalf("write garbage identity: %v", err)
	}
	c := NewClient(filepath.Join(t.TempDir(), "known_hosts"), 0)
	// A readable but unparseable identity: ssh.ParsePrivateKey must fail and Run
	// must error before dialing any real host.
	_, err := c.Run(Target{Addr: "127.0.0.1:0", User: "x", Identity: keyPath}, []string{"true"}, nil)
	if err == nil {
		t.Fatal("expected error for malformed identity")
	}
	// Pin the failure to the parse-identity path (before dial), not any error.
	if !strings.Contains(err.Error(), "parse identity") {
		t.Fatalf("err = %v, want it to mention the parse-identity wrap", err)
	}
}
