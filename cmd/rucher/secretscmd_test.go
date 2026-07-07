package main

import (
	"bytes"
	"testing"

	"rucher/internal/age"
	"rucher/internal/sopsage"
)

func TestParseSecretsEncrypt(t *testing.T) {
	r, err := parseSecretsEncrypt([]string{"--to", "age1a", "--to", "age1b", "--to", "age1a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(r) != 2 { // age1a de-duplicated
		t.Fatalf("recipients = %v, want 2", r)
	}
	if _, err := parseSecretsEncrypt(nil); err == nil {
		t.Fatal("expected an error with no --to")
	}
}

// TestSecretsEncryptRoundTrip encrypts a plaintext YAML map through the CLI path
// and decrypts it back with the codec, confirming the operator flow works
// without any external sops binary.
func TestSecretsEncryptRoundTrip(t *testing.T) {
	id, rec, err := age.GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	in := bytes.NewBufferString("db_password: s3cr3t\napi_key: abc123\n")
	var out bytes.Buffer
	if code := cmdSecretsEncrypt([]string{"--to", rec}, in, &out); code != 0 {
		t.Fatalf("cmdSecretsEncrypt exit %d: %s", code, out.String())
	}
	got, err := sopsage.Decrypt([]byte(id), out.Bytes())
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if got["db_password"] != "s3cr3t" || got["api_key"] != "abc123" {
		t.Fatalf("got %v", got)
	}
}
