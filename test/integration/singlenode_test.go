//go:build integration

package integration

import (
	"strings"
	"testing"
)

// T2.1 — `node cadre new` provisions the OS user and the age identity (0600),
// prints the recipient, and is idempotent (a second call returns the same one).
func TestNewProvisionsUserAndIdentity(t *testing.T) {
	requireNodes(t, node1)
	const name = "itnew"
	cleanupCadre(t, name, node1)
	t.Cleanup(func() { cleanupCadre(t, name, node1) })

	r := rucherNode(t, node1, "node", "cadre", "new", name)
	if r.code != 0 || !strings.HasPrefix(r.out(), "age1") {
		t.Fatalf("new: code=%d out=%q err=%q", r.code, r.stdout, r.stderr)
	}
	recipient := r.out()

	if u := nodeSudo(t, node1, "id", "-u", "rucher-"+name); u.code != 0 {
		t.Fatalf("cadre user not created: %s", u.stderr)
	}
	idPath := "/var/lib/rucher/cadres/" + name + "/.config/rucher/age/identity.txt"
	if p := nodeSudo(t, node1, "stat", "-c", "%a", idPath); p.out() != "600" {
		t.Fatalf("identity.txt perms = %q, want 600", p.out())
	}

	// Idempotent: re-running new returns the existing recipient, not a new key.
	if r2 := rucherNode(t, node1, "node", "cadre", "new", name); r2.out() != recipient {
		t.Fatalf("new is not idempotent: %q != %q", r2.out(), recipient)
	}
}

// T2.4 — apply is idempotent: a second apply with no changes starts/restarts nothing.
func TestIdempotentApply(t *testing.T) {
	requireNodes(t, node1)
	const name = "itidem"
	cleanupCadre(t, name, node1)
	t.Cleanup(func() { cleanupCadre(t, name, node1) })

	parent := newCadre(t, name, map[string]string{
		"rucher.yml":  "name: " + name + "\n",
		"data.volume": volumeUnit,
	})
	if r := nodeApply(t, node1, parent, name); r.code != 0 {
		t.Fatalf("first apply: code=%d err=%q", r.code, r.stderr)
	}
	// Second apply: nothing changed, so the plan is empty.
	r := nodeApply(t, node1, parent, name)
	if r.code != 0 || !strings.Contains(r.stdout, "started=0 restarted=0") {
		t.Fatalf("second apply not idempotent: code=%d out=%q", r.code, r.stdout)
	}
}

// T2.5 — rm unmanages but keeps the OS user; rm --purge deletes the user too.
func TestRemoveKeepsUserPurgeDeletes(t *testing.T) {
	requireNodes(t, node1)
	const name = "itrm"
	cleanupCadre(t, name, node1)
	t.Cleanup(func() { cleanupCadre(t, name, node1) })

	parent := newCadre(t, name, map[string]string{
		"rucher.yml":  "name: " + name + "\n",
		"data.volume": volumeUnit,
	})
	if r := nodeApply(t, node1, parent, name); r.code != 0 {
		t.Fatalf("apply: code=%d err=%q", r.code, r.stderr)
	}

	// rm without purge: the OS user is retained.
	if r := rucherNode(t, node1, "node", "cadre", "rm", name); r.code != 0 {
		t.Fatalf("rm: code=%d err=%q", r.code, r.stderr)
	}
	if u := nodeSudo(t, node1, "id", "-u", "rucher-"+name); u.code != 0 {
		t.Fatalf("rm must keep the OS user, but it is gone")
	}

	// rm --purge: the OS user is deleted.
	if r := rucherNode(t, node1, "node", "cadre", "rm", name, "--purge"); r.code != 0 {
		t.Fatalf("rm --purge: code=%d err=%q", r.code, r.stderr)
	}
	if u := nodeSudo(t, node1, "id", "-u", "rucher-"+name); u.code == 0 {
		t.Fatalf("rm --purge must delete the OS user, but it still exists")
	}
}
