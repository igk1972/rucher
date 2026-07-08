// SPDX-License-Identifier: AGPL-3.0-or-later

package agentcfg

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAndNodeID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.yml")
	os.WriteFile(path, []byte(`
node: lima-essaim-01
store:
  kind: git
  url: git@example.com:org/infrastructure.git
  branch: main
  sshKey: /etc/rucher/deploy_key
interval: 30s
`), 0o644)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Store.URL != "git@example.com:org/infrastructure.git" || c.Store.Branch != "main" {
		t.Fatalf("store = %+v", c.Store)
	}
	id, err := c.NodeID()
	if err != nil {
		t.Fatal(err)
	}
	if id != "lima-essaim-01" {
		t.Fatalf("NodeID = %q", id)
	}
}

func TestLoadDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.yml")
	// store block omits kind and branch: Load must inject the defaults.
	os.WriteFile(path, []byte(`
node: lima-essaim-01
store:
  url: git@example.com:org/infrastructure.git
`), 0o644)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Store.Kind != "git" {
		t.Fatalf("Store.Kind = %q, want %q", c.Store.Kind, "git")
	}
	if c.Store.Branch != "main" {
		t.Fatalf("Store.Branch = %q, want %q", c.Store.Branch, "main")
	}
}

func TestNodeIDDefaultsToHostname(t *testing.T) {
	c := Config{}
	id, err := c.NodeID()
	if err != nil {
		t.Fatal(err)
	}
	host, _ := os.Hostname()
	if id != host {
		t.Fatalf("NodeID = %q, want hostname %q", id, host)
	}
}
