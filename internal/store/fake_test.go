// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
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

func TestFakeReturnsError(t *testing.T) {
	f := &Fake{Err: errors.New("boom")}
	_, _, err := f.Sync(context.Background())
	if !errors.Is(err, f.Err) {
		t.Fatalf("Sync err = %v, want the configured Err", err)
	}
}
