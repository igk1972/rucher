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
}

// TestCmdKeygenRejectsTraversalName covers L5: an unvalidated name must not reach
// the cadres/<name> path join (e.g. "../../evil" escaping the tree).
func TestCmdKeygenRejectsTraversalName(t *testing.T) {
	var out bytes.Buffer
	if code := cmdKeygen([]string{"../../evil", "--to", "age1abc"}, &out); code == 0 {
		t.Fatalf("cmdKeygen accepted a traversal name; output: %q", out.String())
	}
}
