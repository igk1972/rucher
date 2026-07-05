package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"podman-essaim-compartment-manager/internal/age"
	"podman-essaim-compartment-manager/internal/host"
	"podman-essaim-compartment-manager/internal/store"
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
		"root:id -u pecm-web": {Stdout: "1234"},
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
