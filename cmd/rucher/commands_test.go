// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCmdPlanPrintsUnits(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "web")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "rucher.yml"), []byte("{}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "web.container"), []byte("[Container]\nImage=nginx\n"), 0o644)

	var out bytes.Buffer
	code := cmdPlan(root, nil, &out)
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(out.String(), "web.container") {
		t.Fatalf("plan output = %q", out.String())
	}
	if !strings.Contains(out.String(), "rucher-prune.timer") {
		t.Fatalf("plan output = %q, want the synthesized prune timer listed", out.String())
	}
}

func TestCmdPlanNamedNotFound(t *testing.T) {
	root := t.TempDir()
	// No subdirectory named "web": pointing --dir at a folder that lacks it.
	os.WriteFile(filepath.Join(root, "rucher.yml"), []byte("{}\n"), 0o644)

	var out bytes.Buffer
	code := cmdPlan(root, []string{"web"}, &out)
	if code == 0 {
		t.Fatalf("code = 0, want non-zero; output = %q", out.String())
	}
	if !strings.Contains(out.String(), "not found") || !strings.Contains(out.String(), "web") {
		t.Fatalf("plan output = %q, want mention of \"web\" not found", out.String())
	}
}

func TestCmdApplyNamedNotFound(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "rucher.yml"), []byte("{}\n"), 0o644)

	var out bytes.Buffer
	code := cmdApply(root, []string{"web"}, &out)
	if code == 0 {
		t.Fatalf("code = 0, want non-zero; output = %q", out.String())
	}
	if !strings.Contains(out.String(), "not found") || !strings.Contains(out.String(), "web") {
		t.Fatalf("apply output = %q, want mention of \"web\" not found", out.String())
	}
}

func TestCmdPlanEmptyDirNoNames(t *testing.T) {
	root := t.TempDir()

	var out bytes.Buffer
	code := cmdPlan(root, nil, &out)
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "no cadres found") {
		t.Fatalf("plan output = %q, want \"no cadres found\" notice", out.String())
	}
}

func TestCmdApplyEmptyDirNoNames(t *testing.T) {
	root := t.TempDir()

	var out bytes.Buffer
	code := cmdApply(root, nil, &out)
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "no cadres found") {
		t.Fatalf("apply output = %q, want \"no cadres found\" notice", out.String())
	}
}

func TestCmdValidateOK(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "web")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "rucher.yml"), []byte("{}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "web.container"), []byte("[Container]\nImage=nginx\n"), 0o644)

	var out bytes.Buffer
	code := cmdValidate(root, nil, &out)
	if code != 0 {
		t.Fatalf("code = %d, want 0; output = %q", code, out.String())
	}
	if !strings.Contains(out.String(), "web: OK") {
		t.Fatalf("validate output = %q, want \"web: OK\"", out.String())
	}
}

func TestCmdValidateReportsBadPath(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "web")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "rucher.yml"), []byte("{}\n"), 0o644)
	// The unit references a support file the cadre does not ship.
	os.WriteFile(filepath.Join(dir, "web.container"),
		[]byte("[Container]\nImage=nginx\nEnvironmentFile=app.env\n"), 0o644)

	var out bytes.Buffer
	code := cmdValidate(root, nil, &out)
	if code == 0 {
		t.Fatalf("code = 0, want non-zero; output = %q", out.String())
	}
	if !strings.Contains(out.String(), "web: ERROR") || !strings.Contains(out.String(), "app.env") {
		t.Fatalf("validate output = %q, want \"web: ERROR ... app.env\"", out.String())
	}
}

func TestCmdValidateEmptyDirNoNames(t *testing.T) {
	root := t.TempDir()

	var out bytes.Buffer
	code := cmdValidate(root, nil, &out)
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "no cadres found") {
		t.Fatalf("validate output = %q, want \"no cadres found\" notice", out.String())
	}
}

func TestDiscover(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "web"), 0o755)
	os.MkdirAll(filepath.Join(root, "db"), 0o755)
	os.WriteFile(filepath.Join(root, "notes.txt"), []byte("x"), 0o644)

	// Named present: returns the one matching path.
	got, err := discover(root, []string{"web"})
	if err != nil {
		t.Fatalf("discover present: %v", err)
	}
	if len(got) != 1 || filepath.Base(got[0]) != "web" {
		t.Fatalf("discover present = %v, want [.../web]", got)
	}

	// Named missing: returns an error.
	if _, err := discover(root, []string{"missing"}); err == nil {
		t.Fatalf("discover missing: err = nil, want error")
	}

	// A repeated name resolves to the cadre once, not twice.
	dup, err := discover(root, []string{"web", "web"})
	if err != nil {
		t.Fatalf("discover dup: %v", err)
	}
	if len(dup) != 1 {
		t.Fatalf("discover dup = %v, want a single path", dup)
	}

	// No names: returns all subdirectories (files excluded).
	all, err := discover(root, nil)
	if err != nil {
		t.Fatalf("discover all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("discover all = %v, want 2 subdirs", all)
	}
}
