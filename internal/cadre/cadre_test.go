// SPDX-License-Identifier: AGPL-3.0-or-later

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
		"rucher.yml":        "secrets:\n  from: secrets.sops.yaml\n",
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

func TestLoadExcludesExtraSopsFile(t *testing.T) {
	// Any *.sops.yaml is a service file, not just the one named by secrets.from:
	// a second encrypted doc must not leak into the cadre's systemd dir.
	dir := writeCadre(t, map[string]string{
		"web.container":   "[Container]\nImage=nginx\n",
		"extra.sops.yaml": "api_token: ENC[...]\n",
	})
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range c.Files {
		if strings.HasSuffix(f.Name, ".sops.yaml") {
			t.Fatalf("SOPS file %q must not be a materialized file", f.Name)
		}
	}
}

func TestLoadRejectsStrayNameKey(t *testing.T) {
	// A cadre's name comes from its directory; a leftover name: in the
	// manifest is an unknown key rejected by strict decode.
	dir := writeDir(t)
	os.WriteFile(filepath.Join(dir, "rucher.yml"), []byte("name: web\n"), 0o644)
	if _, err := Load(dir); err == nil {
		t.Fatal("expected stray name key error")
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
		"rucher.yml":        "secrets:\n  from: secrets.sops.yaml\n",
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

func TestLoadRejectsQuadletWithoutTypeSection(t *testing.T) {
	// [Unit] alone satisfies the generic section check, but the Quadlet
	// generator requires the type section and fails without it.
	dir := writeCadre(t, map[string]string{
		"web.container": "[Unit]\nDescription=x\n",
	})
	if _, err := Load(dir); err == nil {
		t.Fatal("expected error for a .container without a [Container] section")
	}
}

func TestWarningsPublishPortAllInterfaces(t *testing.T) {
	unit := func(v string) Cadre {
		body := "[Container]\nImage=nginx\nPublishPort=" + v + "\n"
		return Cadre{Files: []File{{Name: "web.container", Content: []byte(body), IsUnit: true}}}
	}
	for _, v := range []string{"80", "8080:80", "8080:80/tcp", "0.0.0.0:8080:80", "[::]:8080:80"} {
		if got := unit(v).Warnings(); len(got) != 1 || !strings.Contains(got[0], v) {
			t.Fatalf("PublishPort=%s: warnings = %v, want one mentioning the value", v, got)
		}
	}
	for _, v := range []string{"127.0.0.1:8080:80", "[::1]:8080:80", "10.1.2.3:8080:80/udp"} {
		if got := unit(v).Warnings(); len(got) != 0 {
			t.Fatalf("PublishPort=%s: warnings = %v, want none", v, got)
		}
	}
}

func TestWarningsSkipSupportFiles(t *testing.T) {
	c := Cadre{Files: []File{{Name: "notes.conf", Content: []byte("PublishPort=80\n")}}}
	if got := c.Warnings(); len(got) != 0 {
		t.Fatalf("warnings = %v, want none for a support file", got)
	}
}

func TestLoadRejectsDeclaredSecretsWithoutFile(t *testing.T) {
	// secrets.create names keys but no secrets.sops.yaml is shipped -> load error
	// (rather than silently treating the cadre as secret-less and deleting them).
	dir := writeCadre(t, map[string]string{
		"rucher.yml":    "secrets:\n  create:\n    - db_password\n",
		"web.container": "[Container]\nImage=nginx\n",
	})
	os.Remove(filepath.Join(dir, "secrets.sops.yaml")) // writeCadre ships one by default
	if _, err := Load(dir); err == nil {
		t.Fatal("expected an error when secrets.create is set but no secrets file exists")
	}
}

func TestLoadRejectsLoginWithoutSecretsFile(t *testing.T) {
	dir := writeCadre(t, map[string]string{
		"rucher.yml":    "registries:\n  login:\n    - registry: ghcr.io\n      username: u\n      passwordKey: tok\n",
		"web.container": "[Container]\nImage=nginx\n",
	})
	os.Remove(filepath.Join(dir, "secrets.sops.yaml"))
	if _, err := Load(dir); err == nil {
		t.Fatal("expected an error when registries.login is set but no secrets file exists")
	}
}

func TestLoadRejectsLeadingDashName(t *testing.T) {
	dir := writeCadre(t, map[string]string{
		"-x.container": "[Container]\nImage=nginx\n",
	})
	if _, err := Load(dir); err == nil {
		t.Fatal("a file name starting with '-' must be rejected")
	}
}

func TestLoadRejectsReservedPruneName(t *testing.T) {
	dir := writeCadre(t, map[string]string{
		"rucher-prune.timer": "[Timer]\nOnCalendar=daily\n",
	})
	if _, err := Load(dir); err == nil {
		t.Fatal("expected error for a file colliding with the synthesized prune units")
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
