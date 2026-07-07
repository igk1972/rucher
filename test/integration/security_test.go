//go:build integration

package integration

import (
	"path/filepath"
	"strings"
	"testing"
)

// T7.1 — the sensitive files rucher writes are private: the cadre's age identity
// and the last-applied state are mode 0600.
func TestSensitiveFilePermissions(t *testing.T) {
	requireNodes(t, node1)
	const name = "itperm"
	cleanupCadre(t, name, node1)
	t.Cleanup(func() { cleanupCadre(t, name, node1) })

	// `new` generates the age identity (apply alone only creates it when a cadre
	// ships secrets); apply then writes the last-applied state file.
	if r := rucherNode(t, node1, "node", "cadre", "new", name); r.code != 0 {
		t.Fatalf("new: code=%d err=%q", r.code, r.stderr)
	}
	parent := newCadre(t, name, map[string]string{
		"rucher.yml":  "{}\n",
		"data.volume": volumeUnit,
	})
	if r := nodeApply(t, node1, parent, name); r.code != 0 {
		t.Fatalf("apply: code=%d err=%q", r.code, r.stderr)
	}

	idPath := "/var/lib/rucher/cadres/" + name + "/.config/rucher/age/identity.txt"
	statePath := "/var/lib/rucher/cadres/state/" + name + ".json"
	for _, f := range []string{idPath, statePath} {
		if p := nodeSudo(t, node1, "stat", "-c", "%a", f); p.out() != "600" {
			t.Fatalf("%s perms = %q, want 600", f, p.out())
		}
	}
}

// T7.2 — rucher never materializes secret plaintext into the state file or the
// cadre's systemd dir. (podman's own secret store is out of scope — that is
// podman's at-rest store, not something rucher writes.)
func TestSecretPlaintextNotInStateOrUnits(t *testing.T) {
	requireNodes(t, node1)
	const name = "itsecret"
	const secret = "sup3rs3cr3t_marker"
	cleanupCadre(t, name, node1)
	t.Cleanup(func() { cleanupCadre(t, name, node1) })

	rec := rucherNode(t, node1, "node", "cadre", "new", name)
	if rec.code != 0 {
		t.Fatalf("new: %s", rec.stderr)
	}
	parent := newCadre(t, name, map[string]string{
		"rucher.yml":  "secrets:\n  from: secrets.sops.yaml\n",
		"data.volume": volumeUnit,
	})
	sopsEncrypt(t, rec.out(), "db_password: "+secret+"\n",
		filepath.Join(parent, name, "secrets.sops.yaml"))

	if r := nodeApply(t, node1, parent, name); r.code != 0 {
		t.Fatalf("apply: code=%d err=%q", r.code, r.stderr)
	}

	// The state file records hashes only.
	st := nodeSudo(t, node1, "cat", "/var/lib/rucher/cadres/state/"+name+".json")
	if strings.Contains(st.stdout, secret) {
		t.Fatalf("secret plaintext leaked into the state file:\n%s", st.stdout)
	}
	// The materialized units/support files must not contain it either.
	if g := nodeSudo(t, node1, "grep", "-r", secret, systemdPath(name)); g.code == 0 {
		t.Fatalf("secret plaintext found under the systemd dir:\n%s", g.stdout)
	}
}
