// SPDX-License-Identifier: AGPL-3.0-or-later

package prune

import (
	"strings"
	"testing"

	"rucher/internal/fileset"
	"rucher/internal/manifest"
)

func TestFilesNilWhenDisabled(t *testing.T) {
	off := false
	if got := Files(manifest.Prune{Enabled: &off}); got != nil {
		t.Fatalf("Files = %v, want nil when disabled", got)
	}
}

func TestFilesSynthesizeUnits(t *testing.T) {
	got := Files(manifest.Prune{Schedule: "weekly", Until: "240h"})
	if len(got) != 2 {
		t.Fatalf("Files = %d entries, want 2", len(got))
	}
	byName := map[string]string{}
	for _, f := range got {
		if !f.IsSystemdUnit {
			t.Fatalf("%s must carry IsSystemdUnit for user-unit-dir routing", f.Name)
		}
		if f.Hash != fileset.Hash(f.Content) {
			t.Fatalf("%s hash does not match content", f.Name)
		}
		byName[f.Name] = string(f.Content)
	}
	service := byName[fileset.PruneService]
	if !strings.Contains(service, "Type=oneshot") || !strings.Contains(service, "--filter until=240h") {
		t.Fatalf("service body:\n%s", service)
	}
	if strings.Contains(service, "[Install]") {
		t.Fatal("the service is fired by its timer and must not have an [Install] section")
	}
	timer := byName[fileset.PruneTimer]
	if !strings.Contains(timer, "OnCalendar=weekly") || !strings.Contains(timer, "WantedBy=timers.target") {
		t.Fatalf("timer body:\n%s", timer)
	}
}

func TestFilesDefaultsForZeroValue(t *testing.T) {
	// A Prune that did not pass through manifest.Load must still yield valid units.
	for _, f := range Files(manifest.Prune{}) {
		if strings.Contains(string(f.Content), "=\n") {
			t.Fatalf("%s has an empty directive:\n%s", f.Name, f.Content)
		}
	}
}
