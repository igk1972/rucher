package cadre

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "web")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"rucher.yml":        "name: web\nsecrets:\n  from: secrets.sops.yaml\n",
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

func TestLoadExcludesSealedIdentity(t *testing.T) {
	dir := writeDir(t) // existing helper: builds a valid "web" cadre
	os.WriteFile(filepath.Join(dir, "identity.age"), []byte("-----BEGIN AGE-----\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "identity.lima-essaim-01.age"), []byte("-----BEGIN AGE-----\n"), 0o644)
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range c.Files {
		if strings.HasPrefix(f.Name, "identity.") && strings.HasSuffix(f.Name, ".age") {
			t.Fatalf("sealed identity %q must not be a materialized file", f.Name)
		}
	}
}

func TestLoadRejectsNameMismatch(t *testing.T) {
	dir := writeDir(t)
	os.WriteFile(filepath.Join(dir, "rucher.yml"), []byte("name: other\n"), 0o644)
	if _, err := Load(dir); err == nil {
		t.Fatal("expected name/dir mismatch error")
	}
}

// writeCadre lays down a minimal valid cadre plus the given extra
// files, letting a test override or add unit/support files.
func writeCadre(t *testing.T, extra map[string]string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "web")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"rucher.yml":        "name: web\nsecrets:\n  from: secrets.sops.yaml\n",
		"secrets.sops.yaml": "db_password: ENC[...]\n",
	}
	for name, body := range extra {
		files[name] = body
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestLoadRejectsMissingEnvironmentFile(t *testing.T) {
	dir := writeCadre(t, map[string]string{
		"web.container": "[Container]\nImage=nginx\nEnvironmentFile=%h/.config/containers/systemd/app.env\n",
	})
	if _, err := Load(dir); err == nil {
		t.Fatal("expected missing EnvironmentFile error")
	}
}

func TestLoadAcceptsPresentEnvironmentFile(t *testing.T) {
	dir := writeCadre(t, map[string]string{
		"web.container": "[Container]\nImage=nginx\nEnvironmentFile=%h/.config/containers/systemd/app.env\n",
		"app.env":       "A=1\n",
	})
	if _, err := Load(dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadRejectsEmptyUnit(t *testing.T) {
	dir := writeCadre(t, map[string]string{"web.container": ""})
	if _, err := Load(dir); err == nil {
		t.Fatal("expected error for empty unit file")
	}
}

func TestLoadRejectsUnitWithoutSection(t *testing.T) {
	dir := writeCadre(t, map[string]string{"web.container": "Image=nginx\n"})
	if _, err := Load(dir); err == nil {
		t.Fatal("expected error for unit without a [Section] header")
	}
}

func TestLoadIgnoresVolumeReference(t *testing.T) {
	// A named volume is not a cadre-local file and must not be validated.
	dir := writeCadre(t, map[string]string{
		"web.container": "[Container]\nImage=nginx\nVolume=data:/v\n",
	})
	if _, err := Load(dir); err != nil {
		t.Fatalf("Volume references must not be validated: %v", err)
	}
}
