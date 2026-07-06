package main

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"rucher/internal/hoststatus"
)

func TestRenderHostsJSON(t *testing.T) {
	rows := []hoststatus.Row{
		{Host: "a", Reachable: true, Revision: "r1", Applied: 2, Removed: 1, Errors: []string{"db: boom"}},
		{Host: "b", Reachable: false, Errors: []string{"conn refused"}},
	}
	var buf bytes.Buffer
	rc := renderHostsJSON(&buf, rows)
	if rc != 1 {
		t.Fatalf("rc = %d, want 1 (b unreachable)", rc)
	}
	out := strings.TrimSpace(buf.String())
	if !strings.HasPrefix(out, "[") {
		t.Fatalf("output should be a JSON array, got: %q", out)
	}
	var got []hoststatus.Row
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
	rc := renderHostsJSON(&buf, nil)
	if rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}
	if got := strings.TrimSpace(buf.String()); got != "[]" {
		t.Fatalf("output = %q, want []", got)
	}
}

func TestRenderHostsTableRC(t *testing.T) {
	unreachable := []hoststatus.Row{
		{Host: "a", Reachable: true},
		{Host: "b", Reachable: false},
	}
	var buf bytes.Buffer
	if rc := renderHostsTable(&buf, unreachable, false); rc != 1 {
		t.Fatalf("rc = %d, want 1 when a row is unreachable", rc)
	}

	allReachable := []hoststatus.Row{
		{Host: "a", Reachable: true},
		{Host: "b", Reachable: true},
	}
	buf.Reset()
	if rc := renderHostsTable(&buf, allReachable, false); rc != 0 {
		t.Fatalf("rc = %d, want 0 when all rows reachable", rc)
	}
}
