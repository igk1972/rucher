package compartment

import (
	"os"
	"path/filepath"
	"testing"
)

func writeDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "web")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"compartment.yml":   "name: web\nsecrets:\n  from: secrets.sops.yaml\n",
		"secrets.sops.yaml": "db_password: ENC[...]\n",
		".sops.yaml":        "creation_rules: []\n",
		"web.container":     "[Container]\nImage=nginx\n",
		"nginx.conf":        "server {}\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestLoadClassifiesFiles(t *testing.T) {
	c, err := Load(writeDir(t))
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "web" {
		t.Fatalf("Name = %q", c.Name)
	}
	if filepath.Base(c.SopsPath) != "secrets.sops.yaml" {
		t.Fatalf("SopsPath = %q", c.SopsPath)
	}
	got := map[string]bool{}
	for _, f := range c.Files {
		got[f.Name] = f.IsUnit
	}
	if len(got) != 2 { // web.container + nginx.conf; service files excluded
		t.Fatalf("Files = %v", got)
	}
	if !got["web.container"] || got["nginx.conf"] {
		t.Fatalf("classification wrong: %v", got)
	}
}

func TestLoadRejectsNameMismatch(t *testing.T) {
	dir := writeDir(t)
	os.WriteFile(filepath.Join(dir, "compartment.yml"), []byte("name: other\n"), 0o644)
	if _, err := Load(dir); err == nil {
		t.Fatal("expected name/dir mismatch error")
	}
}
