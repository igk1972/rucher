// SPDX-License-Identifier: AGPL-3.0-or-later
//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStatusToleratesRegistriesConfig: the runtime status path must tolerate a
// podman.registries block another tool may add to the shared configuration.yml — decode it
// leniently rather than reject the whole file. Strict checking lives in `ops validate`.
func TestStatusToleratesRegistriesConfig(t *testing.T) {
	requireNodes(t, node1)
	cfgPath := filepath.Join(nodesDir(t), node1, "configuration.yml")
	orig, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.WriteFile(cfgPath, orig, 0o644) })

	augmented := append(append([]byte{}, orig...), []byte("\npodman:\n  registries:\n    search: [docker.io, quay.io]\n")...)
	if err := os.WriteFile(cfgPath, augmented, 0o644); err != nil {
		t.Fatal(err)
	}

	r := host(t, nodesDir(t), "ops", "nodes", "--dir", nodesDir(t), "status", node1)
	// A strict decode would surface "field registries not found in type nodecfg.Podman"; the
	// lenient runtime path must not.
	if strings.Contains(r.stdout, "not found in type") {
		t.Fatalf("status must tolerate podman.registries, got:\n%s", r.stdout)
	}
}
