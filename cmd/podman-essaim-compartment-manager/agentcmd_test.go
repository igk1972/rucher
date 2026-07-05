package main

import (
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
	name, to, err := parseKeygen([]string{"web", "--to", "age1abc"})
	if err != nil {
		t.Fatal(err)
	}
	if name != "web" || to != "age1abc" {
		t.Fatalf("got %q %q", name, to)
	}
	if _, _, err := parseKeygen([]string{"web"}); err == nil {
		t.Fatal("expected error when --to is missing")
	}
}
