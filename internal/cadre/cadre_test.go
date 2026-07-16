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

func TestLoadRejectsSymlinkEntry(t *testing.T) {
	// A malicious store could ship a symlink pointing at a root-only node file; the root agent
	// reads cadre files, so os.ReadFile would follow it. Load must reject non-regular entries.
	dir := writeCadre(t, nil)
	secret := filepath.Join(t.TempDir(), "node-key")
	if err := os.WriteFile(secret, []byte("SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(dir, "leak.env")); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("Load must reject a symlinked cadre entry")
	}
}

func TestLoadRejectsSymlinkManifest(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "web")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "real.yml")
	if err := os.WriteFile(target, []byte("secrets:\n  from: secrets.sops.yaml\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "rucher.yml")); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("Load must reject a symlinked rucher.yml")
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

func TestValidateSeesRefAfterLongLine(t *testing.T) {
	// A line longer than bufio's default 64KB cap must not truncate the scan and
	// hide a later EnvironmentFile ref (which would let a broken unit validate).
	longLine := "# " + strings.Repeat("a", 70_000)
	dir := writeCadre(t, map[string]string{
		"web.container": "[Container]\nImage=nginx\n" + longLine + "\nEnvironmentFile=missing.env\n",
	})
	if _, err := Load(dir); err == nil {
		t.Fatal("expected missing EnvironmentFile error even after a >64KB line")
	}
}

func TestWarningsSeesPublishPortAfterLongLine(t *testing.T) {
	longLine := "# " + strings.Repeat("a", 70_000)
	body := "[Container]\nImage=nginx\n" + longLine + "\nPublishPort=80\n"
	c := Cadre{Files: []File{{Name: "web.container", Content: []byte(body), IsUnit: true}}}
	if got := c.Warnings(); len(got) != 1 {
		t.Fatalf("warnings = %v, want one PublishPort warning after a >64KB line", got)
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

func TestLoadClassifiesServiceAsSystemdUnit(t *testing.T) {
	dir := writeCadre(t, map[string]string{
		"job.service": "[Service]\nType=oneshot\nExecStart=/bin/true\n",
	})
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, f := range c.Files {
		if f.Name == "job.service" {
			found = true
			if !f.IsSystemdUnit || f.IsUnit {
				t.Fatalf("job.service classified wrong: IsSystemdUnit=%v IsUnit=%v", f.IsSystemdUnit, f.IsUnit)
			}
		}
	}
	if !found {
		t.Fatal("job.service missing from loaded files")
	}
}

func TestLoadRejectsServiceCollidingWithQuadletService(t *testing.T) {
	// web.container generates web.service; a hand-written web.service would shadow it.
	dir := writeCadre(t, map[string]string{
		"web.container": "[Container]\nImage=nginx\n",
		"web.service":   "[Service]\nExecStart=/bin/true\n[Install]\nWantedBy=default.target\n",
	})
	if _, err := Load(dir); err == nil {
		t.Fatal("expected a collision error for web.service shadowing the generated web.service")
	}
}

func TestLoadRejectsServiceCollidingWithSuffixedQuadletService(t *testing.T) {
	// A .pod generates <stem>-pod.service; a matching hand-written .service collides too.
	dir := writeCadre(t, map[string]string{
		"db.pod":         "[Pod]\n",
		"db-pod.service": "[Service]\nExecStart=/bin/true\n",
	})
	if _, err := Load(dir); err == nil {
		t.Fatal("expected a collision error for db-pod.service shadowing the generated db-pod.service")
	}
}

func TestLoadRejectsReservedPruneServiceName(t *testing.T) {
	dir := writeCadre(t, map[string]string{
		"rucher-prune.service": "[Service]\nType=oneshot\nExecStart=/bin/true\n",
	})
	if _, err := Load(dir); err == nil {
		t.Fatal("expected error for a file colliding with the synthesized prune service")
	}
}

func TestLoadAcceptsOneshotServiceWithTimer(t *testing.T) {
	dir := writeCadre(t, map[string]string{
		"job.service": "[Service]\nType=oneshot\nExecStart=/bin/true\n",
		"job.timer":   "[Timer]\nOnCalendar=daily\n[Install]\nWantedBy=timers.target\n",
	})
	if _, err := Load(dir); err != nil {
		t.Fatalf("a oneshot .service with a companion timer must load cleanly: %v", err)
	}
}

func TestLoadAcceptsServiceAlongsideNonCollidingQuadletUnit(t *testing.T) {
	// A .service coexisting with a Quadlet unit whose generated name differs must load
	// cleanly — guards the collision check against a false positive (the container populates
	// the generated-name map, unlike a lone .timer).
	dir := writeCadre(t, map[string]string{
		"web.container":  "[Container]\nImage=nginx\n",
		"logger.service": "[Service]\nType=oneshot\nExecStart=/bin/true\n",
	})
	if _, err := Load(dir); err != nil {
		t.Fatalf("a non-colliding .service must load cleanly: %v", err)
	}
}

func TestLoadRejectsQuadletUnitGeneratingReservedName(t *testing.T) {
	// rucher-prune.container generates rucher-prune.service, reserved for the synthesized
	// prune units — it would shadow them, so it must be rejected.
	dir := writeCadre(t, map[string]string{
		"rucher-prune.container": "[Container]\nImage=nginx\n",
	})
	if _, err := Load(dir); err == nil {
		t.Fatal("expected rejection: rucher-prune.container generates the reserved rucher-prune.service")
	}
}
