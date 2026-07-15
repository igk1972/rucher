// SPDX-License-Identifier: AGPL-3.0-or-later

package agentcfg

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
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

func TestLoadUseSSLDefaultsTrue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.yml")
	// s3 store without useSSL: TLS must default on (secure by default).
	os.WriteFile(path, []byte(`
store:
  kind: s3
  endpoint: s3.example.com:9000
  bucket: infra
`), 0o644)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Store.UseSSL == nil || !*c.Store.UseSSL {
		t.Fatal("UseSSL must default to true when unspecified")
	}
}

func TestLoadUseSSLExplicitOptOut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.yml")
	// An explicit `useSSL: false` must still opt out of TLS.
	os.WriteFile(path, []byte(`
store:
  kind: s3
  endpoint: s3.example.com:9000
  bucket: infra
  useSSL: false
`), 0o644)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Store.UseSSL == nil || *c.Store.UseSSL {
		t.Fatal("explicit useSSL: false must be honored")
	}
}

func TestUseSSLFalseSurvivesMarshal(t *testing.T) {
	// deploy marshals a StoreConfig and the node reloads it: an explicit false must round-trip
	// (the old plain-bool+omitempty dropped it, silently forcing TLS on).
	f := false
	data, err := yaml.Marshal(Config{Store: StoreConfig{Kind: "s3", UseSSL: &f}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "agent.yml")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Store.UseSSL == nil || *c.Store.UseSSL {
		t.Fatalf("explicit useSSL:false did not survive marshal/reload: %s", data)
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
