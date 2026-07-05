package main

import "testing"

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
