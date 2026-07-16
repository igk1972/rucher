// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"rucher/internal/nodestatus"
)

func TestKnownHostsPath(t *testing.T) {
	t.Run("env override returns verbatim", func(t *testing.T) {
		t.Setenv("RUCHER_KNOWN_HOSTS", "/custom/known_hosts")
		if got := knownHostsPath(); got != "/custom/known_hosts" {
			t.Fatalf("knownHostsPath() = %q, want /custom/known_hosts", got)
		}
	})
	t.Run("default when unset", func(t *testing.T) {
		t.Setenv("RUCHER_KNOWN_HOSTS", "")
		if got := knownHostsPath(); !strings.HasSuffix(got, filepath.Join(".config", "rucher", "known_hosts")) {
			t.Fatalf("knownHostsPath() = %q, want a path ending in .config/rucher/known_hosts", got)
		}
	})
	// M13/L2: with no home dir the fallback must be an unpredictable private temp dir, never a
	// fixed/predictable name a co-tenant could pre-create to defeat TOFU pinning.
	t.Run("fallback is an unpredictable temp path when home unavailable", func(t *testing.T) {
		t.Setenv("RUCHER_KNOWN_HOSTS", "")
		t.Setenv("HOME", "")
		got := knownHostsPath()
		if got == filepath.Join(os.TempDir(), "rucher-known_hosts") ||
			got == filepath.Join(os.TempDir(), fmt.Sprintf("rucher-%d", os.Getuid()), "known_hosts") {
			t.Fatalf("fallback must not be a predictable /tmp name: %q", got)
		}
		if got2 := knownHostsPath(); got == got2 {
			t.Fatalf("fallback must be unpredictable (distinct per call), got %q twice", got)
		}
	})
}

func TestRenderHostsJSON(t *testing.T) {
	rows := []nodestatus.Row{
		{Node: "a", Reachable: true, Revision: "r1", Applied: 2, Removed: 1, Errors: []string{"db: boom"}},
		{Node: "b", Reachable: false, Errors: []string{"conn refused"}},
	}
	var buf bytes.Buffer
	rc := renderNodesJSON(&buf, rows)
	if rc != 1 {
		t.Fatalf("rc = %d, want 1 (b unreachable)", rc)
	}
	out := strings.TrimSpace(buf.String())
	if !strings.HasPrefix(out, "[") {
		t.Fatalf("output should be a JSON array, got: %q", out)
	}
	var got []nodestatus.Row
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	a, b := got[0], got[1]
	if !a.Reachable || a.Revision != "r1" || a.Applied != 2 || a.Removed != 1 {
		t.Fatalf("a = %+v", a)
	}
	if !slices.Equal(a.Errors, []string{"db: boom"}) {
		t.Fatalf("a.Errors = %v, want [db: boom]", a.Errors)
	}
	if b.Reachable {
		t.Fatalf("b should be unreachable: %+v", b)
	}
	if !slices.Equal(b.Errors, []string{"conn refused"}) {
		t.Fatalf("b.Errors = %v, want [conn refused]", b.Errors)
	}
}

func TestRenderHostsJSONEmpty(t *testing.T) {
	var buf bytes.Buffer
	rc := renderNodesJSON(&buf, nil)
	if rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}
	if got := strings.TrimSpace(buf.String()); got != "[]" {
		t.Fatalf("output = %q, want []", got)
	}
}

func TestRenderHostsTableRC(t *testing.T) {
	unreachable := []nodestatus.Row{
		{Node: "a", Reachable: true},
		{Node: "b", Reachable: false},
	}
	var buf bytes.Buffer
	if rc := renderNodesTable(&buf, unreachable, false); rc != 1 {
		t.Fatalf("rc = %d, want 1 when a row is unreachable", rc)
	}

	allReachable := []nodestatus.Row{
		{Node: "a", Reachable: true},
		{Node: "b", Reachable: true},
	}
	buf.Reset()
	if rc := renderNodesTable(&buf, allReachable, false); rc != 0 {
		t.Fatalf("rc = %d, want 0 when all rows reachable", rc)
	}

	// A reachable node whose pass failed (Errors set) must also yield rc=1.
	reachableWithErrors := []nodestatus.Row{
		{Node: "a", Reachable: true, Errors: []string{"store sync: unreachable"}},
	}
	buf.Reset()
	if rc := renderNodesTable(&buf, reachableWithErrors, false); rc != 1 {
		t.Fatalf("rc = %d, want 1 when a reachable node reports errors", rc)
	}
	buf.Reset()
	if rc := renderNodesJSON(&buf, reachableWithErrors); rc != 1 {
		t.Fatalf("json rc = %d, want 1 when a reachable node reports errors", rc)
	}
}

// TestRenderPendingNode: a reachable-but-pending node (agent hasn't reported yet)
// renders REACHABLE=yes with "pending" in the REVISION column and must not bump the
// exit code — it is healthy-but-waiting, not a failure.
func TestRenderPendingNode(t *testing.T) {
	rows := []nodestatus.Row{{Node: "fresh", Address: "6.6.6.6", Reachable: true, Pending: true}}

	var buf bytes.Buffer
	if rc := renderNodesTable(&buf, rows, false); rc != 0 {
		t.Fatalf("rc = %d, want 0 for a pending node (%q)", rc, buf.String())
	}
	out := buf.String()
	if !strings.Contains(out, "pending") {
		t.Fatalf("table should mark the node pending, got %q", out)
	}
	// The pending marker lives in REVISION; the node still reads as reachable.
	line := ""
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "fresh") {
			line = l
		}
	}
	if !strings.Contains(line, "yes") || !strings.Contains(line, "pending") {
		t.Fatalf("pending row should be reachable=yes with revision=pending, got %q", line)
	}

	// JSON keeps the revision empty and exposes the state via the pending field.
	buf.Reset()
	if rc := renderNodesJSON(&buf, rows); rc != 0 {
		t.Fatalf("json rc = %d, want 0 for a pending node", rc)
	}
	if !strings.Contains(buf.String(), `"pending": true`) {
		t.Fatalf("json should carry pending:true, got %q", buf.String())
	}
}

// TestRenderPendingDoesNotMaskUnreachable: a pending node must not zero out the exit
// code when the same run also has a genuinely unreachable node — the unreachable one
// still drives rc=1 in both renderers.
func TestRenderPendingDoesNotMaskUnreachable(t *testing.T) {
	rows := []nodestatus.Row{
		{Node: "p", Address: "1.1.1.1", Reachable: true, Pending: true},
		{Node: "u", Address: "2.2.2.2", Reachable: false, Errors: []string{"conn refused"}},
	}
	var buf bytes.Buffer
	if rc := renderNodesTable(&buf, rows, false); rc != 1 {
		t.Fatalf("table rc = %d, want 1 (an unreachable node is present) (%q)", rc, buf.String())
	}
	buf.Reset()
	if rc := renderNodesJSON(&buf, rows); rc != 1 {
		t.Fatalf("json rc = %d, want 1 (an unreachable node is present)", rc)
	}
}
