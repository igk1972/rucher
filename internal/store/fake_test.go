package store

import (
	"context"
	"testing"
)

func TestFakeReturnsConfigured(t *testing.T) {
	var s Store = &Fake{Checkout: "/tmp/co", Revision: "abc123"}
	co, rev, err := s.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if co != "/tmp/co" || rev != "abc123" {
		t.Fatalf("got %q %q", co, rev)
	}
}
