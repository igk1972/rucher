package sopsage

import (
	"bytes"
	"os"
	"testing"
)

// TestDecryptScalarKnownVector is the gold vector from the sops v3 aes cipher
// test: key of 32 'f' bytes, AAD "bar:", decrypts to "foo".
func TestDecryptScalarKnownVector(t *testing.T) {
	key := bytes.Repeat([]byte("f"), 32)
	enc := "ENC[AES256_GCM,data:oYyi,iv:MyIDYbT718JRr11QtBkcj3Dwm4k1aCGZBVeZf0EyV8o=,tag:t5z2Z023Up0kxwCgw1gNxg==,type:str]"
	plain, typ, err := decryptScalar(enc, key, "bar:")
	if err != nil {
		t.Fatalf("decryptScalar: %v", err)
	}
	if string(plain) != "foo" || typ != "str" {
		t.Fatalf("got (%q,%q), want (foo,str)", plain, typ)
	}
	// Wrong AAD must fail authentication (proves the AAD is bound).
	if _, _, err := decryptScalar(enc, key, "wrong:"); err == nil {
		t.Fatal("expected auth failure with wrong AAD")
	}
}

// TestDecryptFixture decrypts a real secrets.sops.yaml produced by the sops CLI
// (age backend). This exercises the whole path — parse, age unwrap, value AAD,
// and MAC verification with lastmodified as AAD — against ground truth.
func TestDecryptFixture(t *testing.T) {
	id, err := os.ReadFile("testdata/identity.txt")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("testdata/secrets.sops.yaml")
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decrypt(id, data)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	want := map[string]string{"db_password": "s3cr3t", "ghcr_token": "tok"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

// TestRoundTrip encrypts then decrypts, and confirms the MAC verifies.
func TestRoundTrip(t *testing.T) {
	id, err := os.ReadFile("testdata/identity.txt")
	if err != nil {
		t.Fatal(err)
	}
	// Recipient matching testdata/identity.txt.
	const recipient = "age1haymk3vfcphhzwyl4rh7f2ed907x77vgcrdfkmnf9lvy0sns3smqk905gu"
	in := []KV{{"alpha", "one"}, {"beta", "two two"}, {"gamma", ""}}
	enc, err := Encrypt([]string{recipient}, in, "2026-07-07T00:00:00Z")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	got, err := Decrypt(id, enc)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	for _, kv := range in {
		if got[kv.Key] != kv.Value {
			t.Errorf("%s = %q, want %q", kv.Key, got[kv.Key], kv.Value)
		}
	}
}
