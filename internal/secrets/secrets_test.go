// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

import (
	"testing"
)

// TestDecryptFixture decrypts a real secrets.sops.yaml (produced by the sops
// CLI, age backend) with its committed identity — fully in-process, no sops
// binary. The codec itself is exhaustively tested in internal/sopsage.
func TestDecryptFixture(t *testing.T) {
	got, err := Decrypt("testdata/identity.txt", "testdata/secrets.sops.yaml")
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if got["db_password"] != "s3cr3t" || got["ghcr_token"] != "tok" {
		t.Fatalf("decoded = %v", got)
	}
}

func TestDecryptMissingIdentity(t *testing.T) {
	if _, err := Decrypt("testdata/nope.txt", "testdata/secrets.sops.yaml"); err == nil {
		t.Fatal("expected an error for a missing identity file")
	}
}

func TestHashes(t *testing.T) {
	h := Hashes(map[string]string{"a": "x"})
	if h["a"] == "" {
		t.Fatal("expected hash for key a")
	}
}
