// SPDX-License-Identifier: AGPL-3.0-or-later
//go:build integration

package integration

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// subidRange returns the [start, start+count) subuid block a cadre user owns in
// /etc/subuid on the node, or ok=false if it has none.
func subidRange(t *testing.T, node, user string) (start, count int, ok bool) {
	t.Helper()
	out := nodeSudo(t, node, "cat", "/etc/subuid").stdout
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(strings.TrimSpace(line), ":")
		if len(f) != 3 || f[0] != user {
			continue
		}
		s, e1 := strconv.Atoi(f[1])
		c, e2 := strconv.Atoi(f[2])
		if e1 == nil && e2 == nil {
			return s, c, true
		}
	}
	return 0, 0, false
}

// applyPlain applies a minimal secret-less cadre (a lone .volume unit) to node1.
func applyPlain(t *testing.T, name string) {
	t.Helper()
	parent := newCadre(t, name, map[string]string{
		"rucher.yml":  "{}\n",
		"data.volume": volumeUnit,
	})
	if r := nodeApply(t, node1, parent, name); r.code != 0 {
		t.Fatalf("apply %s: code=%d err=%q", name, r.code, r.stderr)
	}
}

// T4.1 — each cadre user gets a unique, non-overlapping 65536-id subuid block.
func TestSubuidBlocksDisjoint(t *testing.T) {
	requireNodes(t, node1)
	const a, b = "itsuba", "itsubb"
	t.Cleanup(func() { cleanupCadre(t, a, node1); cleanupCadre(t, b, node1) })
	cleanupCadre(t, a, node1)
	cleanupCadre(t, b, node1)

	applyPlain(t, a)
	applyPlain(t, b)

	sa, ca, oka := subidRange(t, node1, "rucher-"+a)
	sb, cb, okb := subidRange(t, node1, "rucher-"+b)
	if !oka || !okb {
		t.Fatalf("missing subuid block: %s ok=%v, %s ok=%v", a, oka, b, okb)
	}
	if ca != 65536 || cb != 65536 {
		t.Fatalf("subuid counts = %d, %d, want 65536 each", ca, cb)
	}
	// Disjoint iff NOT (a starts before b ends AND b starts before a ends).
	if sa < sb+cb && sb < sa+ca {
		t.Fatalf("subuid blocks overlap: [%d,%d) and [%d,%d)", sa, sa+ca, sb, sb+cb)
	}
}

// cadreSecretNames lists the podman secret names in a cadre user's own store.
func cadreSecretNames(t *testing.T, node, name string) string {
	t.Helper()
	uid := nodeSudo(t, node, "id", "-u", "rucher-"+name).out()
	return nodeSudo(t, node,
		"runuser", "-u", "rucher-"+name, "--",
		"env", "XDG_RUNTIME_DIR=/run/user/"+uid,
		"DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/"+uid+"/bus",
		"podman", "secret", "ls", "--format", "{{.Name}}",
	).stdout
}

// T4.2 — a cadre's secrets live in its own user's podman store; another cadre
// (another Linux user) cannot see them.
func TestCrossCadreSecretIsolation(t *testing.T) {
	requireNodes(t, node1)
	const a, b = "itseca", "itsecb"
	t.Cleanup(func() { cleanupCadre(t, a, node1); cleanupCadre(t, b, node1) })
	cleanupCadre(t, a, node1)
	cleanupCadre(t, b, node1)

	// Cadre A ships a secret.
	rec := rucherNode(t, node1, "node", "cadre", "new", a)
	if rec.code != 0 {
		t.Fatalf("new %s: %s", a, rec.stderr)
	}
	pa := newCadre(t, a, map[string]string{
		"rucher.yml":  "secrets:\n  from: secrets.sops.yaml\n",
		"data.volume": volumeUnit,
	})
	sopsEncrypt(t, rec.out(), "db_password: hunter2\n", filepath.Join(pa, a, "secrets.sops.yaml"))
	if r := nodeApply(t, node1, pa, a); r.code != 0 {
		t.Fatalf("apply %s: %s", a, r.stderr)
	}
	// Cadre B is a plain cadre (no secret).
	applyPlain(t, b)

	if !strings.Contains(cadreSecretNames(t, node1, a), "db_password") {
		t.Fatalf("cadre %s should see its own secret", a)
	}
	if strings.Contains(cadreSecretNames(t, node1, b), "db_password") {
		t.Fatalf("cadre %s must NOT see cadre %s's secret", b, a)
	}
}

// T4.3 — resources in the manifest become a systemd slice drop-in on the cadre user.
func TestResourceLimitsApplied(t *testing.T) {
	requireNodes(t, node1)
	const name = "itlim"
	t.Cleanup(func() { cleanupCadre(t, name, node1) })
	cleanupCadre(t, name, node1)

	parent := newCadre(t, name, map[string]string{
		"rucher.yml":  "resources:\n  memoryMax: 128M\n  cpuQuota: \"50%\"\n",
		"data.volume": volumeUnit,
	})
	if r := nodeApply(t, node1, parent, name); r.code != 0 {
		t.Fatalf("apply: %s", r.stderr)
	}
	uid := nodeSudo(t, node1, "id", "-u", "rucher-"+name).out()
	conf := "/etc/systemd/system/user-" + uid + ".slice.d/50-rucher.conf"
	c := nodeSudo(t, node1, "cat", conf)
	if c.code != 0 {
		t.Fatalf("slice drop-in missing at %s: %s", conf, c.stderr)
	}
	for _, want := range []string{"MemoryMax=128M", "CPUQuota=50%"} {
		if !strings.Contains(c.stdout, want) {
			t.Fatalf("drop-in missing %q:\n%s", want, c.stdout)
		}
	}
}
