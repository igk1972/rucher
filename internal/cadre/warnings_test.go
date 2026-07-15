// SPDX-License-Identifier: AGPL-3.0-or-later

package cadre

import (
	"strings"
	"testing"
)

func TestWarningsSurfacesScanError(t *testing.T) {
	// A line past the scan buffer must yield an advisory, not a silent truncation.
	long := "[Container]\nImage=" + strings.Repeat("x", 2<<20) + "\nPublishPort=8080\n"
	c := Cadre{Files: []File{{Name: "web.container", Content: []byte(long), IsUnit: true}}}
	w := c.Warnings()
	found := false
	for _, s := range w {
		if strings.Contains(s, "advisory scan incomplete") {
			found = true
		}
	}
	if !found {
		t.Fatalf("want an advisory scan-incomplete warning, got %v", w)
	}
}
