package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunNoArgsPrintsUsageAndFails(t *testing.T) {
	var out bytes.Buffer
	code := run(nil, &out)
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(out.String(), "podman-essaim-compartment-manager") {
		t.Fatalf("usage not printed: %q", out.String())
	}
}
