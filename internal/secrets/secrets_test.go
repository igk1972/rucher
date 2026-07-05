package secrets

import (
	"testing"

	"podman-essaim-compartment-manager/internal/host"
)

func TestDecryptParsesJSON(t *testing.T) {
	f := &host.Fake{Responses: map[string]host.Result{
		"root:env SOPS_AGE_KEY_FILE=/id.txt sops -d --output-type json /c/secrets.sops.yaml": {
			Stdout: `{"db_password":"s3cr3t","ghcr_token":"tok"}`,
		},
	}}
	got, err := Decrypt(f, "/id.txt", "/c/secrets.sops.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if got["db_password"] != "s3cr3t" || got["ghcr_token"] != "tok" {
		t.Fatalf("decoded = %v", got)
	}
	// decryption runs as root, with the compartment identity via SOPS_AGE_KEY_FILE
	rootOK, envOK := false, false
	for _, c := range f.Calls {
		if c.Root {
			rootOK = true
		}
		for _, tok := range c.Argv {
			if tok == "SOPS_AGE_KEY_FILE=/id.txt" {
				envOK = true
			}
		}
	}
	if !rootOK {
		t.Fatal("decrypt must run as root")
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
