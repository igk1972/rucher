package manifest

import "testing"

func TestLoadDefaultsAndParse(t *testing.T) {
	data := []byte(`
registries:
  login:
    - registry: ghcr.io
      username: deploy
      passwordKey: ghcr_token
resources:
  memoryMax: 512M
`)
	m, err := Load(data)
	if err != nil {
		t.Fatal(err)
	}
	if m.Secrets.From != "secrets.sops.yaml" {
		t.Fatalf("default Secrets.From = %q", m.Secrets.From)
	}
	if len(m.Registries.Login) != 1 || m.Registries.Login[0].PasswordKey != "ghcr_token" {
		t.Fatalf("login not parsed: %+v", m.Registries.Login)
	}
	if m.Resources.MemoryMax != "512M" {
		t.Fatalf("MemoryMax = %q", m.Resources.MemoryMax)
	}
}

func TestLoadRejectsUnknownKey(t *testing.T) {
	data := []byte(`
resources:
  memmoryMax: 512M
`)
	if _, err := Load(data); err == nil {
		t.Fatal("expected error for unknown key memmoryMax")
	}
}

func TestLoadRejectsStrayNameKey(t *testing.T) {
	// The manifest no longer has a name field; a leftover name: is now an
	// unknown key rejected by strict decode.
	if _, err := Load([]byte("name: web\n")); err == nil {
		t.Fatal("expected error for stray name key")
	}
}

func TestLoadEmptyManifestIsValid(t *testing.T) {
	// With no name field, an empty (or comment-only) rucher.yml is a valid manifest
	// with every field at its default — `touch rucher.yml` must not fail.
	for _, data := range []string{"", "\n", "# a comment only\n"} {
		m, err := Load([]byte(data))
		if err != nil {
			t.Fatalf("empty manifest %q should load, got %v", data, err)
		}
		if m.Secrets.From != "secrets.sops.yaml" {
			t.Fatalf("default Secrets.From = %q", m.Secrets.From)
		}
	}
}

func TestValidateRejectsIncompleteLogin(t *testing.T) {
	m := Manifest{Registries: Registries{Login: []Login{{Registry: "ghcr.io"}}}}
	if err := m.Validate(); err == nil {
		t.Fatal("expected error for incomplete login")
	}
}
