// SPDX-License-Identifier: AGPL-3.0-or-later
//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// T-operator-service — a cadre may ship its own native .service files: an
// [Install]-less oneshot (installed and fired by a companion .timer, never enabled)
// and a standalone service carrying [Install] (enabled directly). Dropping the
// latter from the cadre disables and removes it on the next apply.
func TestOperatorServiceInstallEnableAndRemove(t *testing.T) {
	requireNodes(t, node1)
	const name = "itservice"
	cleanupCadre(t, name, node1)
	t.Cleanup(func() { cleanupCadre(t, name, node1) })

	const (
		jobService    = "[Unit]\nDescription=it job\n[Service]\nType=oneshot\nExecStart=/bin/true\n"
		jobTimer      = "[Unit]\nDescription=it job timer\n[Timer]\nOnCalendar=daily\n[Install]\nWantedBy=timers.target\n"
		workerService = "[Unit]\nDescription=it worker\n[Service]\nType=oneshot\nExecStart=/bin/true\n[Install]\nWantedBy=default.target\n"
	)
	parent := newCadre(t, name, map[string]string{
		"rucher.yml":     "prune:\n  enabled: false\n",
		"data.volume":    volumeUnit,
		"job.service":    jobService,
		"job.timer":      jobTimer,
		"worker.service": workerService,
	})
	if r := nodeApply(t, node1, parent, name); r.code != 0 {
		t.Fatalf("apply %s: code=%d stderr=%s", name, r.code, r.stderr)
	}

	// All three native units land in the user unit dir.
	ls := nodeSudo(t, node1, "ls", "-1", userUnitPath(name))
	for _, want := range []string{"job.service", "job.timer", "worker.service"} {
		if !strings.Contains(ls.stdout, want) {
			t.Fatalf("expected %q in the user unit dir, got:\n%s%s", want, ls.stdout, ls.stderr)
		}
	}

	// The companion timer and the [Install]-bearing service are enabled.
	for _, unit := range []string{"job.timer", "worker.service"} {
		if r := cadreUser(t, node1, name, "systemctl", "--user", "is-enabled", unit); r.code != 0 || !strings.Contains(r.stdout, "enabled") {
			t.Fatalf("is-enabled %s: code=%d stdout=%q stderr=%q", unit, r.code, r.stdout, r.stderr)
		}
	}

	// The [Install]-less oneshot is installed but NOT enabled (systemd reports it "static"),
	// yet runs on demand — proving its ExecStart is valid and it is reachable by its timer.
	if r := cadreUser(t, node1, name, "systemctl", "--user", "is-enabled", "job.service"); strings.Contains(r.stdout, "enabled") {
		t.Fatalf("job.service must not be enabled, is-enabled stdout=%q", r.stdout)
	}
	if r := cadreUser(t, node1, name, "systemctl", "--user", "start", "job.service"); r.code != 0 {
		t.Fatalf("start job.service: code=%d stderr=%q", r.code, r.stderr)
	}

	// Drop the enabled service from the cadre; the next apply disables and removes it.
	if err := os.Remove(filepath.Join(parent, name, "worker.service")); err != nil {
		t.Fatal(err)
	}
	if r := nodeApply(t, node1, parent, name); r.code != 0 {
		t.Fatalf("re-apply %s: code=%d stderr=%s", name, r.code, r.stderr)
	}
	if r := cadreUser(t, node1, name, "systemctl", "--user", "is-enabled", "worker.service"); strings.Contains(r.stdout, "enabled") {
		t.Fatalf("worker.service still enabled after removal: stdout=%q", r.stdout)
	}
	// The disable must have removed the wants-symlink, not just the unit file — a broken
	// no-op disable would leave a dangling link here (which `is-enabled` alone can't detect
	// once the unit file is gone).
	wants := userUnitPath(name) + "/default.target.wants/worker.service"
	if r := nodeSudo(t, node1, "test", "-L", wants); r.code == 0 {
		t.Fatalf("worker.service wants-symlink must be removed by disable: %s still present", wants)
	}
	ls = nodeSudo(t, node1, "ls", "-1", userUnitPath(name))
	if strings.Contains(ls.stdout, "worker.service") {
		t.Fatalf("worker.service must be removed from the user unit dir, got:\n%s", ls.stdout)
	}
	// The oneshot service and its timer remain.
	for _, want := range []string{"job.service", "job.timer"} {
		if !strings.Contains(ls.stdout, want) {
			t.Fatalf("expected %q to remain in the user unit dir, got:\n%s", want, ls.stdout)
		}
	}
}
