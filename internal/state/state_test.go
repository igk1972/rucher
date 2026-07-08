// SPDX-License-Identifier: AGPL-3.0-or-later

package state

import (
	"path/filepath"
	"testing"
)

func TestLoadMissingReturnsEmpty(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Files == nil || len(s.Files) != 0 {
		t.Fatalf("expected empty state, got %+v", s)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "web.json")
	want := State{
		Name:         "web",
		UID:          1234,
		Files:        map[string]string{"web.container": "h1"},
		SecretHashes: map[string]string{"db_password": "h2"},
		Units:        []string{"web.container"},
	}
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.UID != 1234 || got.Files["web.container"] != "h1" || got.SecretHashes["db_password"] != "h2" {
		t.Fatalf("round trip mismatch: %+v", got)
	}
}
