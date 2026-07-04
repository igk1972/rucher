package secrets

import (
	"testing"

	"podman-essaim-compartment-manager/internal/host"
)

func TestDecryptParsesJSON(t *testing.T) {
	f := &host.Fake{Responses: map[string]host.Result{
		"user:1234:env SOPS_AGE_KEY_FILE=/id.txt sops -d --input-type yaml --output-type json /dev/stdin": {
			Stdout: `{"db_password":"s3cr3t","ghcr_token":"tok"}`,
		},
	}}
	got, err := Decrypt(f, "pecm-web", 1234, "/id.txt", []byte("ciphertext-bytes"))
	if err != nil {
		t.Fatal(err)
	}
	if got["db_password"] != "s3cr3t" || got["ghcr_token"] != "tok" {
		t.Fatalf("decoded = %v", got)
	}
	// ciphertext must be piped on stdin and the identity passed via SOPS_AGE_KEY_FILE env
	stdinOK, envOK := false, false
	for _, c := range f.Calls {
		if string(c.Stdin) == "ciphertext-bytes" {
			stdinOK = true
		}
		for _, tok := range c.Argv {
			if tok == "SOPS_AGE_KEY_FILE=/id.txt" {
				envOK = true
			}
		}
	}
	if !stdinOK {
		t.Fatal("ciphertext not passed via stdin")
	}
	if !envOK {
		t.Fatal("SOPS_AGE_KEY_FILE not set")
	}
}

func TestHashes(t *testing.T) {
	h := Hashes(map[string]string{"a": "x"})
	if h["a"] == "" {
		t.Fatal("expected hash for key a")
	}
}
