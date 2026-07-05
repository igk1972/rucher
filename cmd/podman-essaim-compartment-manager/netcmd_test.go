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
