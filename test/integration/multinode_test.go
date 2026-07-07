//go:build integration

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// cadreManifest is a minimal store cadre: a name plus a `.volume` unit, which
// reconciles without pulling any image (see volumeUnit in core_test.go).
func seedStoreCadre(t *testing.T, store, name string) {
	t.Helper()
	writeStoreFile(t, store, "cadres/"+name+"/rucher.yml", "name: "+name+"\n")
	writeStoreFile(t, store, "cadres/"+name+"/data.volume", volumeUnit)
}

// T3.1 — one placement fans a cadre out to several nodes; a node it is not
// assigned to leaves it alone. Exercises the real multi-node GitOps path.
func TestPlacementAcrossNodes(t *testing.T) {
	requireNodes(t, node1, node2, node3)
	const name = "itplace"
	t.Cleanup(func() { cleanupCadre(t, name, node1, node2, node3) })

	store := newStore(t)
	seedStoreCadre(t, store, name)
	// Assigned to 01 and 02, but NOT 03.
	writeStoreFile(t, store, "placement.yml",
		"placements:\n  "+name+":\n    - "+node1+"\n    - "+node2+"\n")
	commitStore(t, store, "itplace on 01+02")

	prepareGitOps(t, store, node1, node2, node3)

	// 01 and 02 apply the cadre; 03 applies nothing.
	if r := agentRun(t, node1); r.code != 0 || !strings.Contains(r.stdout, "applied=1") {
		t.Fatalf("%s: want applied=1, code=%d out=%q err=%q", node1, r.code, r.stdout, r.stderr)
	}
	if r := agentRun(t, node2); r.code != 0 || !strings.Contains(r.stdout, "applied=1") {
		t.Fatalf("%s: want applied=1, code=%d out=%q err=%q", node2, r.code, r.stdout, r.stderr)
	}
	if r := agentRun(t, node3); r.code != 0 || !strings.Contains(r.stdout, "applied=0") {
		t.Fatalf("%s: want applied=0, code=%d out=%q err=%q", node3, r.code, r.stdout, r.stderr)
	}

	// The cadre user exists where assigned, and not where it isn't.
	if r := nodeSudo(t, node1, "id", "-u", "rucher-"+name); r.code != 0 {
		t.Fatalf("cadre user missing on %s", node1)
	}
	if r := nodeSudo(t, node3, "id", "-u", "rucher-"+name); r.code == 0 {
		t.Fatalf("cadre user unexpectedly present on %s", node3)
	}
}

// T3.2 — changing a placement migrates a cadre: the old node unmanages it
// (removed, user retained), the new node applies it.
func TestCadreMigration(t *testing.T) {
	requireNodes(t, node1, node2)
	const name = "itmig"
	t.Cleanup(func() { cleanupCadre(t, name, node1, node2) })

	store := newStore(t)
	seedStoreCadre(t, store, name)
	writeStoreFile(t, store, "placement.yml", "placements:\n  "+name+": "+node1+"\n")
	commitStore(t, store, "itmig on 01")

	prepareGitOps(t, store, node1, node2)

	// Initially on 01.
	if r := agentRun(t, node1); r.code != 0 || !strings.Contains(r.stdout, "applied=1") {
		t.Fatalf("initial %s: want applied=1, code=%d out=%q err=%q", node1, r.code, r.stdout, r.stderr)
	}

	// Migrate to 02.
	writeStoreFile(t, store, "placement.yml", "placements:\n  "+name+": "+node2+"\n")
	commitStore(t, store, "migrate itmig to 02")

	// 01 unmanages it (removed), keeping the OS user (rm without purge).
	if r := agentRun(t, node1); r.code != 0 || !strings.Contains(r.stdout, "removed=1") {
		t.Fatalf("migrate %s: want removed=1, code=%d out=%q err=%q", node1, r.code, r.stdout, r.stderr)
	}
	if r := nodeSudo(t, node1, "id", "-u", "rucher-"+name); r.code != 0 {
		t.Fatalf("cadre user must be retained on %s after unmanage (rm, not purge)", node1)
	}
	// 02 now applies it.
	if r := agentRun(t, node2); r.code != 0 || !strings.Contains(r.stdout, "applied=1") {
		t.Fatalf("migrate %s: want applied=1, code=%d out=%q err=%q", node2, r.code, r.stdout, r.stderr)
	}
}

// T3.3 — a cadre whose identity is sealed only to node-01 cannot be applied on
// node-02: the unseal fails, the agent reports the failure and exits non-zero,
// while node-01 (which can unseal) applies it cleanly.
func TestSealedIdentityNegative(t *testing.T) {
	requireNodes(t, node1, node2)
	if _, err := exec.LookPath("sops"); err != nil {
		t.Fatal("sops not found on host")
	}
	const name = "itseal"
	t.Cleanup(func() { cleanupCadre(t, name, node1, node2) })

	r1 := nodeKeyInit(t, node1)
	nodeKeyInit(t, node2)

	store := newStore(t)
	// Seal the cadre identity ONLY to node-01's recipient (writes cadres/itseal/identity.age,
	// prints the cadre recipient used to encrypt its secrets).
	seal := host(t, store, "ops", "key", "seal", name, "--to", r1)
	if seal.code != 0 {
		t.Fatalf("ops key seal: code=%d err=%s", seal.code, seal.stderr)
	}
	cadreRecipient := seal.out()

	// Encrypt a secret to the cadre recipient.
	sopsEncrypt(t, cadreRecipient, "db_password: s3cr3t\n",
		filepath.Join(store, "cadres", name, "secrets.sops.yaml"))

	writeStoreFile(t, store, "cadres/"+name+"/rucher.yml", "name: "+name+"\n")
	writeStoreFile(t, store, "cadres/"+name+"/data.volume", volumeUnit)
	// Assign to BOTH nodes; only node-01 can unseal.
	writeStoreFile(t, store, "placement.yml",
		"placements:\n  "+name+":\n    - "+node1+"\n    - "+node2+"\n")
	commitStore(t, store, "itseal sealed to 01 only")

	prepareGitOps(t, store, node1, node2)

	// node-01 can unseal → applies cleanly.
	if r := agentRun(t, node1); r.code != 0 || !strings.Contains(r.stdout, "applied=1") {
		t.Fatalf("%s should apply itseal: code=%d out=%q err=%q", node1, r.code, r.stdout, r.stderr)
	}

	// node-02 cannot unseal → non-zero exit, and its status records the failure.
	r := agentRun(t, node2)
	if r.code == 0 {
		t.Fatalf("%s should fail to apply the sealed cadre, got code=0 out=%q", node2, r.stdout)
	}
	status := nodeSudo(t, node2, "cat", "/var/lib/rucher/agent-status.json")
	if !strings.Contains(status.stdout, name) || !strings.Contains(status.stdout, `"ok": false`) {
		t.Fatalf("%s status should record itseal as failed:\n%s", node2, status.stdout)
	}
	if !strings.Contains(status.stdout, "unseal") {
		t.Fatalf("%s failure should mention unseal:\n%s", node2, status.stdout)
	}
}

// sopsEncrypt encrypts plaintext to an age recipient and writes the SOPS file.
// `--input-type yaml` is mandatory, else sops wraps the doc under a single `data` key.
func sopsEncrypt(t *testing.T, recipient, plaintext, outPath string) {
	t.Helper()
	cmd := exec.Command("sops", "--encrypt", "--input-type", "yaml", "--output-type", "yaml",
		"--age", recipient, "/dev/stdin")
	cmd.Stdin = strings.NewReader(plaintext)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("sops encrypt: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		t.Fatalf("mkdir for sops file: %v", err)
	}
	if err := os.WriteFile(outPath, out, 0o644); err != nil {
		t.Fatalf("write sops file: %v", err)
	}
}
