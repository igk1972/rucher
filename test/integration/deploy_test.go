// SPDX-License-Identifier: AGPL-3.0-or-later
//go:build integration

package integration

import (
	"strings"
	"testing"
)

// TestDeployBootstrap drives `ops nodes deploy --binary … --store-url …` from the
// host against a real node: it should install the binary at /usr/local/bin/rucher,
// print the node's recipient (node key init), write /etc/rucher/agent.yml, and
// enable the agent timer (node agent install).
func TestDeployBootstrap(t *testing.T) {
	requireNodes(t, node1)
	// Leave the node clean for the rest of the suite: stop the timer we enabled.
	t.Cleanup(func() { nodeSudo(t, node1, "systemctl", "disable", "--now", "rucher-agent.timer") })

	bin := linuxBinary(t, nodeGoarch(t, node1))
	store := newStore(t) // its URL only needs to be well-formed for agent.yml; install does not fetch

	r := host(t, nodesDir(t), "ops", "nodes", "--dir", nodesDir(t),
		"deploy", "--binary", bin, "--store-url", gitURL(store), node1)
	if r.code != 0 {
		t.Fatalf("deploy exited %d:\n%s\n%s", r.code, r.stdout, r.stderr)
	}
	if !strings.Contains(r.stdout, "age1") {
		t.Fatalf("deploy did not print a node recipient:\n%s", r.stdout)
	}

	if res := nodeSudo(t, node1, "test", "-x", "/usr/local/bin/rucher"); res.code != 0 {
		t.Fatal("rucher not executable at /usr/local/bin/rucher")
	}
	if res := nodeSudo(t, node1, "test", "-f", "/etc/rucher/agent.yml"); res.code != 0 {
		t.Fatal("/etc/rucher/agent.yml was not written")
	}
	if res := nodeSudo(t, node1, "systemctl", "is-enabled", "rucher-agent.timer"); !strings.Contains(res.stdout, "enabled") {
		t.Fatalf("rucher-agent.timer not enabled: %q", res.out())
	}
}
