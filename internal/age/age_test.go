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

func TestSealToMultipleRecipients(t *testing.T) {
	idA, rcptA, err := GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	idB, rcptB, err := GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	idC, _, err := GenerateIdentity() // unrelated identity, must not decrypt
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("the shared compartment identity")
	ct, err := SealTo([]string{rcptA, rcptB}, msg)
	if err != nil {
		t.Fatal(err)
	}
	for name, id := range map[string]string{"A": idA, "B": idB} {
		got, err := Unseal(id, ct)
		if err != nil {
			t.Fatalf("recipient %s failed to unseal: %v", name, err)
		}
		if !bytes.Equal(got, msg) {
			t.Fatalf("recipient %s round trip mismatch: %q", name, got)
		}
	}
	if _, err := Unseal(idC, ct); err == nil {
		t.Fatal("expected an unrelated identity to fail decryption")
	}
}

func TestSealToNoRecipientsFails(t *testing.T) {
	if _, err := SealTo(nil, []byte("secret")); err == nil {
		t.Fatal("expected an error when sealing to no recipients")
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
