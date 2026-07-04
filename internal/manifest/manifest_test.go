package manifest

import "testing"

func TestLoadDefaultsAndParse(t *testing.T) {
	data := []byte(`
name: web
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
	if m.Name != "web" {
		t.Fatalf("Name = %q", m.Name)
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

func TestValidateRejectsEmptyName(t *testing.T) {
	m := Manifest{}
	if err := m.Validate(); err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestValidateRejectsIncompleteLogin(t *testing.T) {
	m := Manifest{Name: "x", Registries: Registries{Login: []Login{{Registry: "ghcr.io"}}}}
	if err := m.Validate(); err == nil {
		t.Fatal("expected error for incomplete login")
	}
}
