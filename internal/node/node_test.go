package node

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureIdentityCreatesOnceAndIsStable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node", "identity.txt")
	r1, err := EnsureIdentity(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(r1, "age1") {
		t.Fatalf("recipient = %q", r1)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("identity perm = %v, want 0600", info.Mode().Perm())
	}
	r2, err := EnsureIdentity(path) // must not regenerate
	if err != nil {
		t.Fatal(err)
	}
	if r1 != r2 {
		t.Fatal("EnsureIdentity regenerated the key")
	}
}
