package agent

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"rucher/internal/age"
	"rucher/internal/host"
	"rucher/internal/state"
	"rucher/internal/store"
)

func TestRunAppliesAssignedCompartment(t *testing.T) {
	// node key + a compartment identity sealed to it
	nodeID, nodeRcpt, _ := age.GenerateIdentity()
	compID, _, _ := age.GenerateIdentity()
	sealed, _ := age.Seal(nodeRcpt, []byte(compID))

	// build a fake checkout: placement + one compartment assigned to this node
	co := t.TempDir()
	os.WriteFile(filepath.Join(co, "placement.yml"), []byte("placements: {web: node-a}\n"), 0o644)
	cdir := filepath.Join(co, "compartments", "web")
	os.MkdirAll(cdir, 0o755)
	os.WriteFile(filepath.Join(cdir, "compartment.yml"), []byte("name: web\n"), 0o644)
	os.WriteFile(filepath.Join(cdir, "web.container"), []byte("[Container]\nImage=nginx\n"), 0o644)
	os.WriteFile(filepath.Join(cdir, "identity.age"), sealed, 0o644)

	f := &host.Fake{Responses: map[string]host.Result{
		"root:id -u rucher-web": {Stdout: "1234"},
	}}
	fs := &store.Fake{Checkout: co, Revision: "rev1"}

	st, err := Run(context.Background(), f, fs, "node-a", nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if st.Revision != "rev1" || len(st.Applied) != 1 || !st.Applied[0].OK {
		t.Fatalf("status = %+v", st)
	}
	// the unsealed identity must have been written to the compartment's identity path via the user
	var wroteIdentity, chmodIdentity bool
	for _, c := range f.Calls {
		if len(c.Argv) >= 2 && c.Argv[0] == "tee" && strings.HasSuffix(c.Argv[1], "/age/identity.txt") && string(c.Stdin) == compID {
			wroteIdentity = true
		}
		// the private key must be locked down to 0600
		if len(c.Argv) >= 3 && c.Argv[0] == "chmod" && c.Argv[1] == "600" && strings.HasSuffix(c.Argv[2], "/age/identity.txt") {
			chmodIdentity = true
		}
	}
	if !wroteIdentity {
		t.Fatal("unsealed compartment identity was not installed")
	}
	if !chmodIdentity {
		t.Fatal("unsealed compartment identity was not chmod 600")
	}
}

// TestRunRemovesUnassignedManaged covers the removal branch: a compartment with a
// persisted state file that the placement no longer assigns to this node must be
// unmanaged (its units stopped) and reported in Status.Removed.
func TestRunRemovesUnassignedManaged(t *testing.T) {
	// checkout whose placement assigns nothing to node-a
	co := t.TempDir()
	os.WriteFile(filepath.Join(co, "placement.yml"), []byte("placements: {web: node-b}\n"), 0o644)

	// persist state for "old" so reconcile.List() reports it as managed on this node
	statePath := filepath.Join(os.Getenv("RUCHER_STATE_DIR"), "state", "old.json")
	if err := state.Save(statePath, state.State{Name: "old", UID: 1234, Units: []string{"old.container"}}); err != nil {
		t.Fatal(err)
	}

	f := &host.Fake{}
	fs := &store.Fake{Checkout: co, Revision: "rev1"}

	st, err := Run(context.Background(), f, fs, "node-a", "node-key-unused")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(st.Removed, "old") {
		t.Fatalf("Removed = %v, want to contain %q", st.Removed, "old")
	}
	// removal must have stopped old's unit (systemctl --user stop old.service)
	var stopped bool
	for _, c := range f.Calls {
		if !c.Root && len(c.Argv) >= 4 &&
			c.Argv[0] == "systemctl" && c.Argv[1] == "--user" &&
			c.Argv[2] == "stop" && c.Argv[3] == "old.service" {
			stopped = true
		}
	}
	if !stopped {
		t.Fatal("old.service was not stopped during removal")
	}
}

// TestRunFailsCompartmentIsolated covers the failure path: a compartment whose sealed
// identity is corrupt fails to apply, but the failure is isolated to that compartment's
// Result and Run still returns a non-nil error while propagating the store revision.
func TestRunFailsCompartmentIsolated(t *testing.T) {
	// node key: valid, but the compartment's identity.age is not real age ciphertext
	nodeID, _, _ := age.GenerateIdentity()

	co := t.TempDir()
	os.WriteFile(filepath.Join(co, "placement.yml"), []byte("placements: {web: node-a}\n"), 0o644)
	cdir := filepath.Join(co, "compartments", "web")
	os.MkdirAll(cdir, 0o755)
	os.WriteFile(filepath.Join(cdir, "compartment.yml"), []byte("name: web\n"), 0o644)
	os.WriteFile(filepath.Join(cdir, "web.container"), []byte("[Container]\nImage=nginx\n"), 0o644)
	os.WriteFile(filepath.Join(cdir, "secrets.sops.yaml"), []byte("data: ENC[age]\n"), 0o644)
	os.WriteFile(filepath.Join(cdir, "identity.age"), []byte("not-valid-age-ciphertext"), 0o644)

	// EnsureUser must succeed so the failure lands in age.Unseal, not user setup
	f := &host.Fake{Responses: map[string]host.Result{
		"root:id -u rucher-web": {Stdout: "1234"},
	}}
	fs := &store.Fake{Checkout: co, Revision: "rev1"}

	st, err := Run(context.Background(), f, fs, "node-a", nodeID)
	if err == nil {
		t.Fatal("Run returned nil error, want a failure for the corrupt compartment")
	}
	if st.Revision != "rev1" {
		t.Fatalf("Revision = %q, want propagated %q", st.Revision, "rev1")
	}
	if len(st.Applied) != 1 {
		t.Fatalf("Applied = %+v, want exactly one result", st.Applied)
	}
	if st.Applied[0].Name != "web" {
		t.Fatalf("Applied[0].Name = %q, want %q", st.Applied[0].Name, "web")
	}
	if st.Applied[0].OK {
		t.Fatal("compartment Result.OK = true, want false for the failed apply")
	}
	// Pin the failure CAUSE: it must be the identity unseal, not some earlier step.
	if !strings.Contains(st.Applied[0].Error, "unseal") {
		t.Fatalf("Applied[0].Error = %q, want it to mention the unseal failure", st.Applied[0].Error)
	}
}
