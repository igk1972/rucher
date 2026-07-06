package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestParseNetJoin(t *testing.T) {
	h, a, jsonOut, err := parseNetJoin([]string{"web", "--address", "1.2.3.4"})
	if err != nil {
		t.Fatal(err)
	}
	if h != "web" || a != "1.2.3.4" {
		t.Fatalf("got %q %q", h, a)
	}
	if jsonOut {
		t.Fatal("jsonOut should default to false")
	}
}

func TestParseNetJoinTrimsAddress(t *testing.T) {
	_, a, _, err := parseNetJoin([]string{"web", "--address", " 1.2.3.4 "})
	if err != nil {
		t.Fatal(err)
	}
	if a != "1.2.3.4" {
		t.Fatalf("address = %q, want %q", a, "1.2.3.4")
	}
}

func TestParseNetJoinJSONFlag(t *testing.T) {
	h, a, jsonOut, err := parseNetJoin([]string{"web", "--address", "1.2.3.4", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	if h != "web" || a != "1.2.3.4" {
		t.Fatalf("host/address still parsed with --json: got %q %q", h, a)
	}
	if !jsonOut {
		t.Fatal("--json should set jsonOut")
	}
}

func TestParseNetJoinErrors(t *testing.T) {
	cases := map[string][]string{
		"missing host":          {"--address", "1.2.3.4"},
		"missing address":       {"web"},
		"missing address value": {"web", "--address"},
		"extra positional":      {"web", "extra", "--address", "1.2.3.4"},
		"unknown flag":          {"web", "--drivr", "--address", "1.2.3.4"},
		"blank address":         {"web", "--address", ""},
		"whitespace address":    {"web", "--address", "  "},
	}
	for name, args := range cases {
		if _, _, _, err := parseNetJoin(args); err == nil {
			t.Fatalf("%s: parseNetJoin(%v) expected error, got nil", name, args)
		}
	}
}

func TestCmdNetJoinJSONOutput(t *testing.T) {
	dir := t.TempDir()
	// ops nodes join writes into an existing node directory; WriteNetwork does not
	// create parents, so set the node dir up first.
	if err := os.MkdirAll(filepath.Join(dir, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	code := cmdNetJoin(dir, []string{"web", "--address", "1.2.3.4", "--json"}, &out)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	want := `{"node":"web","address":"1.2.3.4"}` + "\n"
	if out.String() != want {
		t.Fatalf("output = %q, want %q", out.String(), want)
	}
	// The config file must still be written next to the node directory.
	if _, err := os.ReadFile(filepath.Join(dir, "web", "configuration.yml")); err != nil {
		t.Fatalf("config not written: %v", err)
	}
}
