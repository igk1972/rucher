package main

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

func TestRunNoArgsPrintsUsageAndFails(t *testing.T) {
	var out bytes.Buffer
	code := run(nil, &out)
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(out.String(), "rucher") {
		t.Fatalf("usage not printed: %q", out.String())
	}
}

func TestNodesStatusJSONWiring(t *testing.T) {
	// An empty hosts dir means no rows, so the --json wiring in run() should emit
	// an empty JSON array (not null) and exit 0 since nothing is unreachable.
	dir := t.TempDir()
	var out bytes.Buffer
	code := run([]string{"ops", "nodes", "--dir", dir, "status", "--json"}, &out)
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if got := strings.TrimSpace(out.String()); got != "[]" {
		t.Fatalf("output = %q, want []", got)
	}
}

func TestParseDir(t *testing.T) {
	cases := []struct {
		args     []string
		wantDir  string
		wantName []string
	}{
		{nil, "./cadres", nil},
		{[]string{"web"}, "./cadres", []string{"web"}},
		{[]string{"--dir", "/c", "web"}, "/c", []string{"web"}},
		{[]string{"web", "--dir", "/c"}, "/c", []string{"web"}},               // flag after name
		{[]string{"web", "--dir", "/c", "api"}, "/c", []string{"web", "api"}}, // flag between names
	}
	for _, tc := range cases {
		dir, names, err := parseDir(tc.args)
		if err != nil {
			t.Fatalf("parseDir(%v) error: %v", tc.args, err)
		}
		if dir != tc.wantDir || !slices.Equal(names, tc.wantName) {
			t.Fatalf("parseDir(%v) = (%q, %v), want (%q, %v)", tc.args, dir, names, tc.wantDir, tc.wantName)
		}
	}
	if _, _, err := parseDir([]string{"--dir"}); err == nil {
		t.Fatal("parseDir(--dir) with no value should error")
	}
}

func TestParseRm(t *testing.T) {
	cases := []struct {
		args      []string
		wantName  string
		wantPurge bool
	}{
		{[]string{"web"}, "web", false},
		{[]string{"web", "--purge"}, "web", true},
		{[]string{"--purge", "web"}, "web", true}, // flag before name
	}
	for _, tc := range cases {
		name, purge, err := parseRm(tc.args)
		if err != nil {
			t.Fatalf("parseRm(%v) error: %v", tc.args, err)
		}
		if name != tc.wantName || purge != tc.wantPurge {
			t.Fatalf("parseRm(%v) = (%q, %v), want (%q, %v)", tc.args, name, purge, tc.wantName, tc.wantPurge)
		}
	}
	if _, _, err := parseRm([]string{"--purge"}); err == nil {
		t.Fatal("parseRm with no name should error")
	}
	if _, _, err := parseRm(nil); err == nil {
		t.Fatal("parseRm with no args should error")
	}
}

func TestNodeApplyRejectsPositionalNames(t *testing.T) {
	// `node apply` reconciles the whole node; a named cadre must go through
	// `node cadre apply`, so a positional name here is a usage error with guidance.
	var out bytes.Buffer
	code := run([]string{"node", "apply", "web"}, &out)
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(out.String(), "node cadre apply") {
		t.Fatalf("expected guidance to `node cadre apply`, got %q", out.String())
	}
}

func TestNodeCadreApplyRequiresName(t *testing.T) {
	var out bytes.Buffer
	code := run([]string{"node", "cadre", "apply"}, &out)
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(out.String(), "node apply") {
		t.Fatalf("expected guidance to `node apply`, got %q", out.String())
	}
}

func TestUnknownGroupFails(t *testing.T) {
	var out bytes.Buffer
	if code := run([]string{"frobnicate"}, &out); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
}
