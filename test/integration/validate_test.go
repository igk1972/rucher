// SPDX-License-Identifier: AGPL-3.0-or-later
//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOpsValidateQuadletLint exercises the real host binary end-to-end (no Lima node):
// `ops validate` must run each Quadlet unit through Podman's parser, so a bad .container
// fails and a valid one passes. This proves the podman parser is linked and works in the
// built binary, complementing the unit tests in internal/quadletlint. Host-only.
func TestOpsValidateQuadletLint(t *testing.T) {
	build(t) // builds hostBin

	root := t.TempDir()
	writeCadreDir := func(name, unit string) {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "rucher.yml"), []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "web.container"), []byte(unit), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeCadreDir("good", "[Container]\nImage=docker.io/library/nginx:alpine\nPublishPort=127.0.0.1:8080:80\n")
	writeCadreDir("badimage", "[Container]\nExec=sleep infinity\n") // no Image=
	writeCadreDir("badkey", "[Container]\nImage=nginx\nMemoryyy=1g\n")

	// The valid cadre passes.
	if r := host(t, root, "ops", "validate", "--dir", root, "good"); r.code != 0 || !strings.Contains(r.stdout, "good: OK") {
		t.Fatalf("valid cadre should pass: code=%d out=%q", r.code, r.stdout)
	}
	// A missing Image is caught by the quadlet parser as an ERROR (exit 1).
	if r := host(t, root, "ops", "validate", "--dir", root, "badimage"); r.code == 0 || !strings.Contains(r.stdout, "ERROR") {
		t.Fatalf("cadre with no Image should fail: code=%d out=%q", r.code, r.stdout)
	}
	// An unknown key is likewise fatal — the real value of linking podman's parser.
	r := host(t, root, "ops", "validate", "--dir", root, "badkey")
	if r.code == 0 || !strings.Contains(r.stdout, "ERROR") || !strings.Contains(r.stdout, "Memoryyy") {
		t.Fatalf("cadre with an unknown key should fail naming it: code=%d out=%q", r.code, r.stdout)
	}
}
