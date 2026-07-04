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
	os.WriteFile(filepath.Join(dir, "compartment.yml"), []byte("name: web\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "web.container"), []byte("[Container]\nImage=nginx\n"), 0o644)

	var out bytes.Buffer
	code := cmdPlan(root, nil, &out)
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(out.String(), "web.container") {
		t.Fatalf("plan output = %q", out.String())
	}
}
