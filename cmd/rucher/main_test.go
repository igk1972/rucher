// SPDX-License-Identifier: AGPL-3.0-or-later

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
	if _, _, err := parseDir([]string{"--drii", "/c"}); err == nil {
		t.Fatal("parseDir must reject an unknown flag, not treat it as a name")
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
	if _, _, err := parseRm([]string{"--force", "web"}); err == nil {
		t.Fatal("parseRm must reject an unknown flag, not treat it as a cadre name")
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

// TestNodeAgentRejectsMalformedConfig covers L11: a bare --config or a stray token
// must fail with a usage error, not fall back to the default config path.
// These all return code 2 during parsing, before any node side effect.
func TestNodeAgentRejectsMalformedConfig(t *testing.T) {
	for _, args := range [][]string{
		{"node", "agent", "run", "--config"},
		{"node", "agent", "install", "--config"},
		{"node", "agent", "run", "garbage"},
	} {
		var out bytes.Buffer
		if code := run(args, &out); code != 2 {
			t.Fatalf("%v: code = %d, want 2 (%q)", args, code, out.String())
		}
	}
}

func TestNodeAgentUnknownSubcommand(t *testing.T) {
	var out bytes.Buffer
	if code := run([]string{"node", "agent", "foo"}, &out); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(out.String(), "unknown node agent subcommand") {
		t.Fatalf("expected unknown-subcommand message, got %q", out.String())
	}
}

// TestNodesStatusRejectsUnknownFlag covers L12: a typo'd flag (--llive for --live)
// must be rejected, not treated as a phantom node name to SSH into.
func TestNodesStatusRejectsUnknownFlag(t *testing.T) {
	var out bytes.Buffer
	if code := run([]string{"ops", "nodes", "status", "--llive"}, &out); code != 2 {
		t.Fatalf("code = %d, want 2 (%q)", code, out.String())
	}
	if !strings.Contains(out.String(), "unknown flag") {
		t.Fatalf("expected unknown-flag message, got %q", out.String())
	}
}

// TestNodeCadreStatusRejectsFlag: a flag-looking token to `node cadre status` is a typo,
// not a cadre-name filter, so it must be rejected as a usage error.
func TestNodeCadreStatusRejectsFlag(t *testing.T) {
	var out bytes.Buffer
	if code := run([]string{"node", "cadre", "status", "--live"}, &out); code != 2 {
		t.Fatalf("code = %d, want 2 (%q)", code, out.String())
	}
	if !strings.Contains(out.String(), "unknown flag") {
		t.Fatalf("expected unknown-flag message, got %q", out.String())
	}
}

// TestFixedAritySubcommandsRejectExtraArgs: a fixed-arity subcommand must reject a
// trailing token (a typo or stray flag) with a usage error, not silently ignore it.
// All cases fail in the dispatcher before any node side effect.
func TestFixedAritySubcommandsRejectExtraArgs(t *testing.T) {
	for _, args := range [][]string{
		{"node", "cadre", "new", "demo", "extra"},
		{"node", "cadre", "logs", "demo", "app.service", "extra"},
		{"node", "cadre", "recipient", "demo", "extra"},
		{"node", "key", "init", "extra"},
		{"node", "key", "show", "extra"},
	} {
		var out bytes.Buffer
		if code := run(args, &out); code != 2 {
			t.Fatalf("%v: code = %d, want 2 (%q)", args, code, out.String())
		}
	}
}
