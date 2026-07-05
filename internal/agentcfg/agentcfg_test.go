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
  url: git@example.com:org/fleet.git
  branch: main
  sshKey: /etc/podman-essaim/deploy_key
interval: 30s
`), 0o644)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Store.URL != "git@example.com:org/fleet.git" || c.Store.Branch != "main" {
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
