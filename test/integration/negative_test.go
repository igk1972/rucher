//go:build integration

package integration

import (
	"os"
	"strings"
	"testing"
)

// parseRevision pulls <rev> out of the agent's "revision <rev>: applied=.." line.
func parseRevision(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if s, ok := strings.CutPrefix(line, "revision "); ok {
			return strings.TrimSpace(strings.SplitN(s, ":", 2)[0])
		}
	}
	return ""
}

// T5.1 — an unknown key in rucher.yml is a hard error (strict decode), so a typo'd
// field fails the apply loudly instead of being silently dropped.
func TestUnknownManifestKeyRejected(t *testing.T) {
	requireNodes(t, node1)
	const name = "itbadkey"
	cleanupCadre(t, name, node1)
	t.Cleanup(func() { cleanupCadre(t, name, node1) })

	parent := newCadre(t, name, map[string]string{
		"rucher.yml":  "name: " + name + "\nbogusField: oops\n",
		"data.volume": volumeUnit,
	})
	r := nodeApply(t, node1, parent, name)
	if r.code == 0 {
		t.Fatalf("apply must fail on an unknown manifest key, got code 0: %q", r.stdout)
	}
	if !strings.Contains(r.stdout, "parse rucher.yml") {
		t.Fatalf("error should name the manifest parse failure:\n%s", r.stdout)
	}
}

// T5.2 — a `placement:` (singular) typo is rejected (strict decode) rather than
// parsing to zero placements and unmanaging every cadre on the node. A cadre applied
// under a correct placement must survive an agent pass over the broken file.
func TestPlacementTypoDoesNotUnmanage(t *testing.T) {
	requireNodes(t, node1)
	const name = "ittypo"
	t.Cleanup(func() { cleanupCadre(t, name, node1) })

	store := newStore(t)
	seedStoreCadre(t, store, name)
	writeStoreFile(t, store, "placement.yml", "placements:\n  "+name+": "+node1+"\n")
	commitStore(t, store, "correct placement")
	prepareGitOps(t, store, node1)

	if r := agentRun(t, node1); r.code != 0 || !strings.Contains(r.stdout, "applied=1") {
		t.Fatalf("initial apply: code=%d out=%q err=%q", r.code, r.stdout, r.stderr)
	}

	// Break the placement key, then run again.
	writeStoreFile(t, store, "placement.yml", "placement:\n  "+name+": "+node1+"\n")
	commitStore(t, store, "typo: placement (singular)")

	r := agentRun(t, node1)
	if r.code == 0 {
		t.Fatalf("agent must fail on the placement typo, got code 0: %q", r.stdout)
	}
	if !strings.Contains(r.stdout, "placement") {
		t.Fatalf("error should name placement.yml:\n%s\n%s", r.stdout, r.stderr)
	}
	// The cadre must NOT have been unmanaged: its user and state are still present.
	if u := nodeSudo(t, node1, "id", "-u", "rucher-"+name); u.code != 0 {
		t.Fatalf("cadre was unmanaged despite the parse error (user gone)")
	}
	if s := nodeSudo(t, node1, "test", "-f", "/var/lib/rucher/cadres/state/"+name+".json"); s.code != 0 {
		t.Fatalf("cadre state was dropped despite the parse error")
	}
}

// T5.3 — a transient store-fetch failure with a valid checkout already present
// keeps the agent running on the last-good revision instead of aborting.
func TestStoreLastGoodOnFetchFailure(t *testing.T) {
	requireNodes(t, node1)
	const name = "itlastgood"
	t.Cleanup(func() { cleanupCadre(t, name, node1) })

	store := newStore(t)
	seedStoreCadre(t, store, name)
	writeStoreFile(t, store, "placement.yml", "placements:\n  "+name+": "+node1+"\n")
	commitStore(t, store, "initial")
	prepareGitOps(t, store, node1)

	r1 := agentRun(t, node1)
	if r1.code != 0 || !strings.Contains(r1.stdout, "applied=1") {
		t.Fatalf("initial apply: code=%d out=%q err=%q", r1.code, r1.stdout, r1.stderr)
	}
	rev1 := parseRevision(r1.stdout)

	// Make the store unfetchable (delete it on the host). The node still has a valid
	// checkout, so the next pass must keep running on the last-good revision.
	if err := os.RemoveAll(store); err != nil {
		t.Fatalf("remove store: %v", err)
	}

	r2 := agentRun(t, node1)
	if r2.code != 0 {
		t.Fatalf("agent must survive a fetch failure on a valid checkout: code=%d out=%q err=%q", r2.code, r2.stdout, r2.stderr)
	}
	if !strings.Contains(r2.stdout, "applied=1") {
		t.Fatalf("cadre should stay applied on last-good: %q", r2.stdout)
	}
	if rev2 := parseRevision(r2.stdout); rev2 != rev1 {
		t.Fatalf("revision should stay last-good %q, got %q", rev1, rev2)
	}
}

// Changing store.url must switch stores without a manual cache wipe: the agent
// re-clones when the configured URL differs from the cached origin. On the old
// code the second pass kept pulling store A's origin and never saw B.
func TestStoreURLChangeSwitchesStores(t *testing.T) {
	requireNodes(t, node1)
	if storeErr != nil {
		t.Fatalf("store server unavailable: %v", storeErr)
	}
	const a, b = "iturla", "iturlb"
	t.Cleanup(func() { cleanupCadre(t, a, node1); cleanupCadre(t, b, node1) })
	verifyStoreReachable(t, node1)

	storeA := newStore(t)
	seedStoreCadre(t, storeA, a)
	writeStoreFile(t, storeA, "placement.yml", "placements:\n  "+a+": "+node1+"\n")
	commitStore(t, storeA, "store A")

	storeB := newStore(t)
	seedStoreCadre(t, storeB, b)
	writeStoreFile(t, storeB, "placement.yml", "placements:\n  "+b+": "+node1+"\n")
	commitStore(t, storeB, "store B")

	// Clean start, then point the agent at store A.
	resetAgentCache(t, node1)
	nodeKeyInit(t, node1)
	writeAgentConfig(t, node1, node1, gitURL(storeA))
	if r := agentRun(t, node1); r.code != 0 || !strings.Contains(r.stdout, "applied=1") {
		t.Fatalf("store A run: code=%d out=%q err=%q", r.code, r.stdout, r.stderr)
	}
	if u := nodeSudo(t, node1, "id", "-u", "rucher-"+a); u.code != 0 {
		t.Fatalf("cadre %s not applied from store A", a)
	}

	// Repoint at store B WITHOUT clearing /var/lib/rucher/store: the fix re-clones.
	writeAgentConfig(t, node1, node1, gitURL(storeB))
	r := agentRun(t, node1)
	if r.code != 0 {
		t.Fatalf("store B run: code=%d out=%q err=%q", r.code, r.stdout, r.stderr)
	}
	if u := nodeSudo(t, node1, "id", "-u", "rucher-"+b); u.code != 0 {
		t.Fatalf("cadre %s not applied after switching store.url to B (fix regressed?)", b)
	}
	// A is absent from B's placement, so it must have been unmanaged this pass.
	if !strings.Contains(r.stdout, "removed=1") {
		t.Fatalf("cadre %s should have been unmanaged after the store switch: %q", a, r.stdout)
	}
}
