// SPDX-License-Identifier: AGPL-3.0-or-later
//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// userUnitPath is where a cadre's native systemd units (and the synthesized
// prune units) land on the node.
func userUnitPath(name string) string {
	return "/var/lib/rucher/cadres/" + name + "/.config/systemd/user"
}

// T-prune — the synthesized image-GC units are provisioned by default and torn
// down when the manifest disables pruning.
func TestPruneTimerDefaultOnAndDisable(t *testing.T) {
	requireNodes(t, node1)
	const name = "itprune"
	cleanupCadre(t, name, node1)
	t.Cleanup(func() { cleanupCadre(t, name, node1) })

	parent := newCadre(t, name, map[string]string{
		"rucher.yml":  "{}\n",
		"data.volume": volumeUnit,
	})
	if r := nodeApply(t, node1, parent, name); r.code != 0 {
		t.Fatalf("apply %s: code=%d stderr=%s", name, r.code, r.stderr)
	}

	ls := nodeSudo(t, node1, "ls", "-1", userUnitPath(name))
	for _, want := range []string{"rucher-prune.timer", "rucher-prune.service"} {
		if !strings.Contains(ls.stdout, want) {
			t.Fatalf("expected %q in the user unit dir, got:\n%s%s", want, ls.stdout, ls.stderr)
		}
	}
	if r := cadreUser(t, node1, name, "systemctl", "--user", "is-enabled", "rucher-prune.timer"); r.code != 0 || !strings.Contains(r.stdout, "enabled") {
		t.Fatalf("is-enabled rucher-prune.timer: code=%d stdout=%q stderr=%q", r.code, r.stdout, r.stderr)
	}
	// The [Install]-less oneshot must run on demand (proves the ExecStart is valid).
	if r := cadreUser(t, node1, name, "systemctl", "--user", "start", "rucher-prune.service"); r.code != 0 {
		t.Fatalf("start rucher-prune.service: code=%d stderr=%q", r.code, r.stderr)
	}

	if err := os.WriteFile(filepath.Join(parent, name, "rucher.yml"), []byte("prune:\n  enabled: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := nodeApply(t, node1, parent, name); r.code != 0 {
		t.Fatalf("re-apply %s: code=%d stderr=%s", name, r.code, r.stderr)
	}

	if r := cadreUser(t, node1, name, "systemctl", "--user", "is-enabled", "rucher-prune.timer"); r.code == 0 {
		t.Fatalf("rucher-prune.timer still enabled after prune was disabled: %q", r.stdout)
	}
	ls = nodeSudo(t, node1, "ls", "-1", userUnitPath(name))
	if strings.Contains(ls.stdout, "rucher-prune") {
		t.Fatalf("prune unit files must be removed from the user unit dir, got:\n%s", ls.stdout)
	}
}
