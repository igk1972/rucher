// SPDX-License-Identifier: AGPL-3.0-or-later
//go:build integration

package integration

import (
	"strings"
	"testing"
)

// A minimal `.volume` unit reconciles without pulling any image: quadlet generates
// a oneshot `podman volume create` service that goes active, so the cadre's state
// persists and its unit shows a real ActiveState. That keeps the core tests
// independent of the node's outbound network.
const volumeUnit = "[Volume]\n"

// T1.1 — regression for commit 2e99d1d: `ops nodes status --live` must run
// `rucher node cadre status` on the node, not the long-gone `rucher status`.
// Before the fix the node returned its usage banner (unknown command) and the
// live block showed that banner instead of the unit status table.
func TestLiveShowsUnitStatus(t *testing.T) {
	requireNodes(t, node1)
	const name = "itlive"
	cleanupCadre(t, name, node1)
	t.Cleanup(func() {
		cleanupCadre(t, name, node1)
		nodeSudo(t, node1, "rm", "-f", "/var/lib/rucher/agent-status.json")
	})

	parent := newCadre(t, name, map[string]string{
		"rucher.yml":  "{}\n",
		"data.volume": volumeUnit,
	})
	if r := nodeApply(t, node1, parent, name); r.code != 0 {
		t.Fatalf("apply %s: code=%d stderr=%s", name, r.code, r.stderr)
	}

	// This test drives the --live path and asserts on a concrete revision, so seed a
	// minimal agent-status.json — without it the node would be reachable-but-pending
	// (empty revision).
	seed := `{"revision":"ittest","applied":[],"removed":[]}`
	if r := nodeSudoStdin(t, node1, []byte(seed), "tee", "/var/lib/rucher/agent-status.json"); r.code != 0 {
		t.Fatalf("seed agent-status.json: %s", r.stderr)
	}

	r := host(t, nodesDir(t), "ops", "nodes", "--dir", nodesDir(t), "status", "--live", node1)
	live := r.stdout

	// The status table header is produced only by `node cadre status`; the old bug
	// produced the usage banner ("unknown command"). Assert on both directions.
	if strings.Contains(live, "unknown command") || strings.Contains(live, "node — on the Linux node") {
		t.Fatalf("--live shows the usage banner, not unit status:\n%s", live)
	}
	// The live block is the `node cadre status` table: header + the cadre's unit
	// (shown by its Quadlet filename) with a real ActiveState.
	for _, want := range []string{"ACTIVE", "SUB", name, "data.volume", "active"} {
		if !strings.Contains(live, want) {
			t.Fatalf("--live output missing %q:\n%s", want, live)
		}
	}
}

// T1.2 — regression for commit 6b04e8a: any *.sops.yaml is a service file and must
// never be materialized onto the node, not just a file literally named .sops.yaml.
// A second encrypted doc (extra.sops.yaml) must not leak into the systemd dir,
// while ordinary support files still do.
func TestExtraSopsNotMaterialized(t *testing.T) {
	requireNodes(t, node1)
	const name = "itsops"
	cleanupCadre(t, name, node1)
	t.Cleanup(func() { cleanupCadre(t, name, node1) })

	parent := newCadre(t, name, map[string]string{
		// No secrets.from file is shipped, so apply performs no decryption; the test
		// is purely about file classification.
		"rucher.yml":      "{}\n",
		"data.volume":     volumeUnit,
		"app.conf":        "answer = 42\n",            // ordinary support file: MUST be materialized
		"extra.sops.yaml": "api_token: ENC[hidden]\n", // second SOPS doc: MUST NOT be materialized
	})
	if r := nodeApply(t, node1, parent, name); r.code != 0 {
		t.Fatalf("apply %s: code=%d stderr=%s", name, r.code, r.stderr)
	}

	ls := nodeSudo(t, node1, "ls", "-1", systemdPath(name))
	if ls.code != 0 {
		t.Fatalf("ls systemd dir: %s", ls.stderr)
	}
	got := ls.stdout
	if strings.Contains(got, "extra.sops.yaml") {
		t.Fatalf("extra.sops.yaml leaked into the systemd dir:\n%s", got)
	}
	for _, want := range []string{"data.volume", "app.conf"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q materialized, got:\n%s", want, got)
		}
	}
}
