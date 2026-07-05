package hoststatus

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"podman-essaim-compartment-manager/internal/host"
)

func writeHost(t *testing.T, dir, name, body string) {
	t.Helper()
	os.MkdirAll(filepath.Join(dir, name), 0o755)
	os.WriteFile(filepath.Join(dir, name, "configuration.yml"), []byte(body), 0o644)
}

func TestCollectAggregatesAndIsolates(t *testing.T) {
	hosts := t.TempDir()
	writeHost(t, hosts, "a", "network: {driver: ssh, address: 1.1.1.1}\n")
	writeHost(t, hosts, "b", "network: {driver: ssh, address: 2.2.2.2}\n")

	statusJSON := `{"revision":"rev9","applied":[{"name":"web","ok":true},{"name":"db","ok":false,"error":"boom"}],"removed":["old"]}`
	f := &host.Fake{Responses: map[string]host.Result{
		// host a returns a status doc; the ssh argv ends with the address then the remote cmd
		"root:ssh -o BatchMode=yes -o ConnectTimeout=5 -o StrictHostKeyChecking=accept-new root@1.1.1.1 cat /var/lib/podman-essaim/agent-status.json": {Stdout: statusJSON},
		// host b: ssh fails (unreachable)
		"root:ssh -o BatchMode=yes -o ConnectTimeout=5 -o StrictHostKeyChecking=accept-new root@2.2.2.2 cat /var/lib/podman-essaim/agent-status.json": {Code: 255, Stderr: "conn refused"},
	}}
	rows, err := Collect(f, hosts, "/nonexistent", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d", len(rows))
	}
	var a, b Row
	for _, r := range rows {
		if r.Host == "a" {
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
	// An unreachable host must capture the ssh stderr so the operator can tell
	// "host down" from "config broken".
	if !slices.Contains(b.Errors, "conn refused") {
		t.Fatalf("b.Errors = %v, want to contain %q", b.Errors, "conn refused")
	}
}

func TestCollectCapturesTransportError(t *testing.T) {
	hosts := t.TempDir()
	writeHost(t, hosts, "c", "network: {driver: ssh, address: 3.3.3.3}\n")

	// A transport failure makes Root return a non-nil error rather than a Result.
	f := &host.Fake{Err: errors.New("ssh spawn failed")}
	rows, err := Collect(f, hosts, "/nonexistent", nil, false)
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
