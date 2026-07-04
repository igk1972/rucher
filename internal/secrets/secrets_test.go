package secrets

import (
	"strings"
	"testing"

	"podman-essaim-compartment-manager/internal/host"
)

func TestDecryptParsesJSON(t *testing.T) {
	f := &host.Fake{Responses: map[string]host.Result{
		"user:1234:env SOPS_AGE_KEY_FILE=/id.txt sops -d --output-type json /c/secrets.sops.yaml": {
			Stdout: `{"db_password":"s3cr3t","ghcr_token":"tok"}`,
		},
	}}
	got, err := Decrypt(f, "pecm-web", 1234, "/id.txt", "/c/secrets.sops.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if got["db_password"] != "s3cr3t" || got["ghcr_token"] != "tok" {
		t.Fatalf("decoded = %v", got)
	}
	// identity must be passed via SOPS_AGE_KEY_FILE env, not argv beyond the file
	found := false
	for _, c := range f.Calls {
		if strings.Contains(strings.Join(c.Argv, " "), "SOPS_AGE_KEY_FILE=/id.txt") {
			found = true
		}
	}
	if !found {
		t.Fatal("SOPS_AGE_KEY_FILE not set")
	}
}

func TestHashes(t *testing.T) {
	h := Hashes(map[string]string{"a": "x"})
	if h["a"] == "" {
		t.Fatal("expected hash for key a")
	}
}
