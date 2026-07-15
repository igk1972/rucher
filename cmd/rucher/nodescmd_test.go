// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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
	// M13: with no home dir the fallback must be per-user, never a fixed shared
	// /tmp name a co-tenant could pre-create to defeat TOFU pinning.
	t.Run("fallback is per-user when home unavailable", func(t *testing.T) {
		t.Setenv("RUCHER_KNOWN_HOSTS", "")
		t.Setenv("HOME", "")
		got := knownHostsPath()
		if got == filepath.Join(os.TempDir(), "rucher-known_hosts") {
			t.Fatalf("fallback must not be the fixed shared /tmp name: %q", got)
		}
		if !strings.Contains(got, strconv.Itoa(os.Getuid())) {
			t.Fatalf("fallback should be per-user (contain uid): %q", got)
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
