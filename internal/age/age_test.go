package age

import (
	"bytes"
	"strings"
	"testing"
)

func TestGenerateSealUnsealRoundTrip(t *testing.T) {
	id, rcpt, err := GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(id, "AGE-SECRET-KEY-") || !strings.HasPrefix(rcpt, "age1") {
		t.Fatalf("bad key formats: id=%q rcpt=%q", id, rcpt)
	}
	msg := []byte("the compartment identity")
	ct, err := Seal(rcpt, msg)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ct, msg) {
		t.Fatal("ciphertext contains plaintext")
	}
	got, err := Unseal(id, ct)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, msg) {
		t.Fatalf("round trip mismatch: %q", got)
	}
}

func TestUnsealWithWrongIdentityFails(t *testing.T) {
	_, rcpt, _ := GenerateIdentity()
	otherID, _, _ := GenerateIdentity()
	ct, _ := Seal(rcpt, []byte("secret"))
	if _, err := Unseal(otherID, ct); err == nil {
		t.Fatal("expected decryption failure with the wrong identity")
	}
}

func TestRecipientFor(t *testing.T) {
	id, rcpt, _ := GenerateIdentity()
	got, err := RecipientFor(id)
	if err != nil {
		t.Fatal(err)
	}
	if got != rcpt {
		t.Fatalf("RecipientFor = %q, want %q", got, rcpt)
	}
}
