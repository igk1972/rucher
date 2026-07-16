// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestAgentTimerUnit(t *testing.T) {
	if got := agentTimerUnit("45s"); !strings.Contains(got, "OnUnitActiveSec=45s") {
		t.Fatalf("configured interval not honored: %q", got)
	}
	if got := agentTimerUnit(""); !strings.Contains(got, "OnUnitActiveSec=30s") {
		t.Fatalf("empty interval should default to 30s: %q", got)
	}
}

func TestParseKeygen(t *testing.T) {
	name, recipients, err := parseKeygen([]string{"web", "--to", "age1abc"})
	if err != nil {
		t.Fatal(err)
	}
	if name != "web" || len(recipients) != 1 || recipients[0] != "age1abc" {
		t.Fatalf("got %q %v", name, recipients)
	}
	name, recipients, err = parseKeygen([]string{"web", "--to", "age1abc", "--to", "age1def"})
	if err != nil {
		t.Fatal(err)
	}
	if name != "web" || len(recipients) != 2 || recipients[0] != "age1abc" || recipients[1] != "age1def" {
		t.Fatalf("got %q %v", name, recipients)
	}
	_, recipients, err = parseKeygen([]string{"web", "--to", "age1abc", "--to", "age1abc"})
	if err != nil {
		t.Fatal(err)
	}
	if len(recipients) != 1 || recipients[0] != "age1abc" {
		t.Fatalf("duplicate --to not deduplicated: %v", recipients)
	}
	if _, _, err := parseKeygen([]string{"web"}); err == nil {
		t.Fatal("expected error when --to is missing")
	}
	if _, _, err := parseKeygen([]string{"web", "extra", "--to", "age1"}); err == nil {
		t.Fatal("expected error on an extra positional argument")
	}
	if _, _, err := parseKeygen([]string{"--too", "web", "--to", "age1"}); err == nil {
		t.Fatal("expected error on a flag-looking positional (typo), not a cadre name")
	}
}

func TestParseAgentConfig(t *testing.T) {
	if p, err := parseAgentConfig(nil); err != nil || p != "/etc/rucher/agent.yml" {
		t.Fatalf("default: got %q, %v", p, err)
	}
	if p, err := parseAgentConfig([]string{"--config", "/tmp/a.yml"}); err != nil || p != "/tmp/a.yml" {
		t.Fatalf("explicit: got %q, %v", p, err)
	}
	// A bare --config, a typo'd flag, a stray token, or a trailing argument are all
	// usage errors — never a silent fallback to the default config path.
	for _, bad := range [][]string{
		{"--config"},                // flag with no value
		{"--config", ""},            // empty value
		{"--cfg", "x"},              // unknown flag
		{"garbage"},                 // stray positional
		{"--config", "/p", "extra"}, // trailing token
	} {
		if _, err := parseAgentConfig(bad); err == nil {
			t.Fatalf("expected error for %v", bad)
		}
	}
}

// TestCmdKeygenRejectsTraversalName covers L5: an unvalidated name must not reach
// the cadres/<name> path join (e.g. "../../evil" escaping the tree).
func TestCmdKeygenRejectsTraversalName(t *testing.T) {
	var out bytes.Buffer
	if code := cmdKeygen([]string{"../../evil", "--to", "age1abc"}, &out); code == 0 {
		t.Fatalf("cmdKeygen accepted a traversal name; output: %q", out.String())
	}
}
