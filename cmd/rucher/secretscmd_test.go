package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"rucher/internal/age"
	"rucher/internal/sopsage"
)

func TestParseSecretsEncrypt(t *testing.T) {
	fl, err := parseSecretsEncrypt([]string{"--to", "age1a", "--to", "age1b", "--to", "age1a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(fl.to) != 2 { // age1a de-duplicated
		t.Fatalf("to = %v, want 2", fl.to)
	}
	if _, err := parseSecretsEncrypt(nil); err == nil {
		t.Fatal("expected an error with no recipients")
	}
	if _, err := parseSecretsEncrypt([]string{"--seal-to", "age1n"}); err == nil {
		t.Fatal("--seal-to without --cadre should error")
	}
	if _, err := parseSecretsEncrypt([]string{"--cadre", "web", "--seal-to", "age1n", "--to", "age1x"}); err == nil {
		t.Fatal("--seal-to together with --to should error")
	}
}

// TestSecretsEncryptRoundTrip covers the direct mode (--to, stdin -> stdout).
func TestSecretsEncryptRoundTrip(t *testing.T) {
	id, rec, err := age.GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	in := bytes.NewBufferString("db_password: s3cr3t\napi_key: abc123\n")
	var out bytes.Buffer
	if code := cmdSecretsEncrypt([]string{"--to", rec}, in, &out); code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	got, err := sopsage.Decrypt([]byte(id), out.Bytes())
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if got["db_password"] != "s3cr3t" || got["api_key"] != "abc123" {
		t.Fatalf("got %v", got)
	}
}

// TestSecretsEncryptSealMode covers the one-command seal+encrypt flow: it should
// write dir/<cadre>/{identity.age,secrets.sops.yaml}, sealed to the node key.
func TestSecretsEncryptSealMode(t *testing.T) {
	nodeID, nodeRcpt, err := age.GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	inFile := filepath.Join(dir, "web.plain.yaml")
	if err := os.WriteFile(inFile, []byte("db_password: s3cr3t\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	code := cmdSecretsEncrypt([]string{"--cadre", "web", "--seal-to", nodeRcpt, "--dir", dir, "--in", inFile}, nil, &out)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}

	idAge, err := os.ReadFile(filepath.Join(dir, "web", "identity.age"))
	if err != nil {
		t.Fatalf("identity.age not written: %v", err)
	}
	sops, err := os.ReadFile(filepath.Join(dir, "web", "secrets.sops.yaml"))
	if err != nil {
		t.Fatalf("secrets.sops.yaml not written: %v", err)
	}

	// The node unseals the cadre identity, which decrypts the secrets.
	cadreID, err := age.Unseal(nodeID, idAge)
	if err != nil {
		t.Fatalf("node cannot unseal the cadre identity: %v", err)
	}
	got, err := sopsage.Decrypt(cadreID, sops)
	if err != nil {
		t.Fatalf("decrypt secrets with cadre identity: %v", err)
	}
	if got["db_password"] != "s3cr3t" {
		t.Fatalf("got %v", got)
	}
}
