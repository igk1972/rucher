package main

import "testing"

func TestParseNetJoin(t *testing.T) {
	h, d, o, a, err := parseNetJoin([]string{"web", "--driver", "ssh", "--address", "1.2.3.4"})
	if err != nil {
		t.Fatal(err)
	}
	if h != "web" || d != "ssh" || a != "1.2.3.4" {
		t.Fatalf("got %q %q %q %q", h, d, o, a)
	}
	if o != "web" { // overlay-name defaults to host
		t.Fatalf("overlayName default = %q", o)
	}
	if _, _, _, _, err := parseNetJoin([]string{"--driver", "ssh"}); err == nil {
		t.Fatal("expected error without host")
	}
}

func TestParseNetJoinErrors(t *testing.T) {
	cases := map[string][]string{
		"unknown flag":         {"web", "--drivr", "x"},
		"unknown driver value": {"web", "--driver", "wat"},
		"missing driver value": {"web", "--driver"},
		"missing overlay-name": {"web", "--overlay-name"},
		"missing address":      {"web", "--address"},
		"extra positional":     {"web", "extra"},
	}
	for name, args := range cases {
		if _, _, _, _, err := parseNetJoin(args); err == nil {
			t.Fatalf("%s: parseNetJoin(%v) expected error, got nil", name, args)
		}
	}
}

func TestParseNetJoinValidDrivers(t *testing.T) {
	for _, driver := range []string{"ssh", "tailscale"} {
		h, d, o, a, err := parseNetJoin([]string{"web", "--driver", driver})
		if err != nil {
			t.Fatalf("driver %s: unexpected error: %v", driver, err)
		}
		if h != "web" || d != driver || o != "web" || a != "" {
			t.Fatalf("driver %s: got %q %q %q %q", driver, h, d, o, a)
		}
	}
}
