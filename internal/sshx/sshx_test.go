package sshx

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

func TestClientRunParsesBadKey(t *testing.T) {
	c := NewClient(filepath.Join(t.TempDir(), "known_hosts"), 0)
	// Identity points at a nonexistent file: Run must error before dialing.
	_, err := c.Run(Target{Addr: "127.0.0.1:0", User: "x", Identity: "/no/such/key"}, []string{"true"}, nil)
	if err == nil {
		t.Fatal("expected error for unreadable identity")
	}
}
