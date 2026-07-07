//go:build integration

package integration

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

// waitNodeReady starts a node (best effort) and waits until a shell command
// succeeds, so a node stopped by a test is fully restored for the rest of the suite.
func waitNodeReady(node string, timeout time.Duration) {
	exec.Command("limactl", "start", node).Run()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if exec.Command("limactl", "shell", node, "--", "true").Run() == nil {
			return
		}
		time.Sleep(2 * time.Second)
	}
}

// T6.1 — a node that is down shows REACHABLE=no and makes `ops nodes status` exit
// non-zero, without breaking the table. (Deep-merge of node configs and host-key
// TOFU are covered by unit tests in internal/nodecfg and internal/sshx.)
func TestNodeUnreachable(t *testing.T) {
	requireNodes(t, node3)
	// Always bring node3 back for the rest of the suite, however this test exits.
	t.Cleanup(func() { waitNodeReady(node3, 120*time.Second) })

	if out, err := exec.Command("limactl", "stop", node3).CombinedOutput(); err != nil {
		t.Fatalf("limactl stop %s: %v\n%s", node3, err, out)
	}

	r := host(t, nodesDir(t), "ops", "nodes", "--dir", nodesDir(t), "status", node3)
	if r.code == 0 {
		t.Fatalf("status must exit non-zero when a node is down:\n%s", r.stdout)
	}
	if !strings.Contains(r.stdout, node3) {
		t.Fatalf("status output missing node %s:\n%s", node3, r.stdout)
	}
	// The node's own row must be marked unreachable.
	var row string
	for _, l := range strings.Split(r.stdout, "\n") {
		if strings.Contains(l, node3) && strings.Contains(l, "no") {
			row = l
		}
	}
	if row == "" {
		t.Fatalf("node %s should show REACHABLE=no:\n%s", node3, r.stdout)
	}
}
