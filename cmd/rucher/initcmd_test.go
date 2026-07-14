// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"rucher/internal/cadre"
)

func TestCmdInitScaffoldsValidCadre(t *testing.T) {
	root := t.TempDir()
	var out bytes.Buffer
	if code := cmdInit(root, "demo", &out); code != 0 {
		t.Fatalf("code = %d: %s", code, out.String())
	}
	for _, fn := range []string{"rucher.yml", "web.container"} {
		if _, err := os.Stat(filepath.Join(root, "demo", fn)); err != nil {
			t.Fatalf("missing %s: %v", fn, err)
		}
	}
	c, err := cadre.Load(filepath.Join(root, "demo"))
	if err != nil {
		t.Fatalf("scaffold does not load: %v", err)
	}
	if w := c.Warnings(); len(w) != 0 {
		t.Fatalf("scaffold has warnings: %v", w)
	}
	out.Reset()
	if code := cmdValidate(root, nil, &out); code != 0 {
		t.Fatalf("validate code = %d: %s", code, out.String())
	}
	if strings.Contains(out.String(), "WARN") || !strings.Contains(out.String(), "demo: OK") {
		t.Fatalf("validate output = %q, want a clean demo: OK", out.String())
	}
}

func TestCmdInitRefusesExistingDir(t *testing.T) {
	root := t.TempDir()
	var out bytes.Buffer
	if code := cmdInit(root, "demo", &out); code != 0 {
		t.Fatalf("first init failed: %s", out.String())
	}
	out.Reset()
	if code := cmdInit(root, "demo", &out); code == 0 || !strings.Contains(out.String(), "exists") {
		t.Fatalf("second init: code=%d output=%q, want an exists error", code, out.String())
	}
}

func TestCmdInitRejectsBadName(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"", "Web_1", "-lead", "a-very-long-name-exceeding-the-limit"} {
		var out bytes.Buffer
		if code := cmdInit(root, name, &out); code == 0 {
			t.Fatalf("name %q accepted, want rejection", name)
		}
	}
}
