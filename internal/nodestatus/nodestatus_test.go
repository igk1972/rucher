// SPDX-License-Identifier: AGPL-3.0-or-later

package nodestatus

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"rucher/internal/sshx"
)

func writeNode(t *testing.T, dir, name, body string) {
	t.Helper()
	os.MkdirAll(filepath.Join(dir, name), 0o755)
	os.WriteFile(filepath.Join(dir, name, "configuration.yml"), []byte(body), 0o644)
}

func TestCollectAggregatesAndIsolates(t *testing.T) {
	nodes := t.TempDir()
	writeNode(t, nodes, "a", "network: {address: 1.1.1.1}\n")
	writeNode(t, nodes, "b", "network: {address: 2.2.2.2}\n")

	// The Targets that Resolve yields for the two node configs.
	targetA := sshx.Target{Addr: "1.1.1.1:22", User: "root"}
	targetB := sshx.Target{Addr: "2.2.2.2:22", User: "root"}
	catCmd := []string{"cat", statusPath}

	statusJSON := `{"revision":"rev9","applied":[{"name":"web","ok":true},{"name":"db","ok":false,"error":"boom"}],"removed":["old"]}`
	f := &sshx.Fake{Responses: map[string]sshx.Result{
		// node a returns a status doc
		sshx.Key(targetA, catCmd): {Stdout: statusJSON},
		// node b: ssh fails (unreachable)
		sshx.Key(targetB, catCmd): {Code: 255, Stderr: "conn refused"},
	}}
	rows, err := Collect(f, nodes, "/nonexistent", nil, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d", len(rows))
	}
	var a, b Row
	for _, r := range rows {
		if r.Node == "a" {
			a = r
		} else {
			b = r
		}
	}
	if !a.Reachable || a.Revision != "rev9" || a.Applied != 2 || a.Removed != 1 {
		t.Fatalf("a = %+v", a)
	}
	// The failed "db" apply must surface as an exact "<name>: <error>" string.
	if !slices.Contains(a.Errors, "db: boom") {
		t.Fatalf("a.Errors = %v, want to contain %q", a.Errors, "db: boom")
	}
	if b.Reachable {
		t.Fatalf("b should be unreachable: %+v", b)
	}
	// An unreachable node must capture the ssh stderr so the operator can tell
	// "node down" from "config broken".
	if !slices.Contains(b.Errors, "conn refused") {
		t.Fatalf("b.Errors = %v, want to contain %q", b.Errors, "conn refused")
	}
}

func TestCollectInheritsGlobalConnection(t *testing.T) {
	nodes := t.TempDir()
	// Global default supplies the ssh user; the per-node file only has an address.
	os.WriteFile(filepath.Join(nodes, "configuration.yml"), []byte("connection:\n  user: globaluser\n"), 0o644)
	writeNode(t, nodes, "a", "network: {address: 1.1.1.1}\n")

	// Keying the fake by the globaluser target proves the global connection default
	// was merged in: without it Resolve would yield user "root" and the key would miss.
	target := sshx.Target{Addr: "1.1.1.1:22", User: "globaluser"}
	catCmd := []string{"cat", statusPath}
	statusJSON := `{"revision":"rev1","applied":[{"name":"web","ok":true}],"removed":[]}`
	f := &sshx.Fake{Responses: map[string]sshx.Result{
		sshx.Key(target, catCmd): {Stdout: statusJSON},
	}}
	rows, err := Collect(f, nodes, "/nonexistent", nil, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d", len(rows))
	}
	if !rows[0].Reachable || rows[0].Revision != "rev1" {
		t.Fatalf("global connection.user not inherited: %+v", rows[0])
	}
}

// TestCollectPreservesOrderUnderConcurrency runs with a bounded worker pool and
// asserts the rows still come back in the order of names. Also exercises the
// concurrent path for the race detector.
func TestCollectPreservesOrderUnderConcurrency(t *testing.T) {
	nodes := t.TempDir()
	names := []string{"n0", "n1", "n2", "n3", "n4"}
	catCmd := []string{"cat", statusPath}
	resp := map[string]sshx.Result{}
	for i, name := range names {
		addr := fmt.Sprintf("10.0.0.%d", i)
		writeNode(t, nodes, name, "network: {address: "+addr+"}\n")
		target := sshx.Target{Addr: addr + ":22", User: "root"}
		resp[sshx.Key(target, catCmd)] = sshx.Result{Stdout: `{"revision":"r","applied":[],"removed":[]}`}
	}
	f := &sshx.Fake{Responses: resp}

	rows, err := Collect(f, nodes, "/nonexistent", names, false, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(names) {
		t.Fatalf("rows = %d, want %d", len(rows), len(names))
	}
	for i, name := range names {
		if rows[i].Node != name {
			t.Fatalf("rows[%d].Node = %q, want %q (order not preserved)", i, rows[i].Node, name)
		}
		if !rows[i].Reachable {
			t.Fatalf("rows[%d] unreachable: %+v", i, rows[i])
		}
	}
}

func TestCollectCapturesTransportError(t *testing.T) {
	nodes := t.TempDir()
	writeNode(t, nodes, "c", "network: {address: 3.3.3.3}\n")

	// A transport failure makes Run return a non-nil error rather than a Result.
	f := &sshx.Fake{Err: errors.New("ssh spawn failed")}
	rows, err := Collect(f, nodes, "/nonexistent", nil, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d", len(rows))
	}
	c := rows[0]
	if c.Reachable {
		t.Fatalf("c should be unreachable: %+v", c)
	}
	if !slices.Contains(c.Errors, "ssh spawn failed") {
		t.Fatalf("c.Errors = %v, want to contain %q", c.Errors, "ssh spawn failed")
	}
}

// TestCollectFoldsPassLevelError covers M9: a node reachable over ssh whose agent
// status carries a pass-level Error must surface it, not read as a healthy node.
func TestCollectFoldsPassLevelError(t *testing.T) {
	nodes := t.TempDir()
	writeNode(t, nodes, "e", "network: {address: 5.5.5.5}\n")
	target := sshx.Target{Addr: "5.5.5.5:22", User: "root"}

	statusJSON := `{"revision":"","applied":[],"removed":[],"error":"store sync: remote unreachable"}`
	f := &sshx.Fake{Responses: map[string]sshx.Result{
		sshx.Key(target, []string{"cat", statusPath}): {Stdout: statusJSON},
	}}
	rows, err := Collect(f, nodes, "/nonexistent", nil, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	e := rows[0]
	if !e.Reachable {
		t.Fatalf("e should be reachable (ssh succeeded): %+v", e)
	}
	if !slices.Contains(e.Errors, "store sync: remote unreachable") {
		t.Fatalf("pass-level error not surfaced: %+v", e)
	}
}

func TestCollectFlagsCorruptStatus(t *testing.T) {
	nodes := t.TempDir()
	writeNode(t, nodes, "d", "network: {address: 4.4.4.4}\n")
	target := sshx.Target{Addr: "4.4.4.4:22", User: "root"}

	// The node is reachable but its status file is not valid JSON.
	f := &sshx.Fake{Responses: map[string]sshx.Result{
		sshx.Key(target, []string{"cat", statusPath}): {Stdout: "{ not json"},
	}}
	rows, err := Collect(f, nodes, "/nonexistent", nil, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	d := rows[0]
	if !d.Reachable {
		t.Fatalf("d should be reachable (ssh succeeded): %+v", d)
	}
	var flagged bool
	for _, e := range d.Errors {
		if strings.Contains(e, "unreadable agent status") {
			flagged = true
		}
	}
	if !flagged {
		t.Fatalf("a corrupt status must be flagged, got errors %v", d.Errors)
	}
}

func TestSanitizeNodeOutput(t *testing.T) {
	// ESC and other control chars are stripped; newline/tab and printable text survive.
	in := "ok\x1b[31mRED\x1b]0;title\x07 line\ttab\ndone\x00"
	got := sanitizeNodeOutput(in)
	want := "ok[31mRED]0;title line\ttab\ndone"
	if got != want {
		t.Fatalf("sanitizeNodeOutput = %q, want %q", got, want)
	}
	if strings.ContainsRune(got, 0x1b) {
		t.Fatal("ESC survived sanitization")
	}
}
