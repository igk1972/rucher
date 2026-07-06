package main

import "testing"

func TestParseNetJoin(t *testing.T) {
	h, a, err := parseNetJoin([]string{"web", "--address", "1.2.3.4"})
	if err != nil {
		t.Fatal(err)
	}
	if h != "web" || a != "1.2.3.4" {
		t.Fatalf("got %q %q", h, a)
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
		if _, _, err := parseNetJoin(args); err == nil {
			t.Fatalf("%s: parseNetJoin(%v) expected error, got nil", name, args)
		}
	}
}
