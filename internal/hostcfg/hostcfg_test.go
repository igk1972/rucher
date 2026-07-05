package hostcfg

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "configuration.yml")
	os.WriteFile(path, []byte(`
network:
  driver: tailscale
  address: 100.1.2.3
connection:
  host: 10.0.0.5
  user: admin
  port: 2222
  identity: ~/.ssh/id_ed25519
`), 0o644)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Network.Driver != "tailscale" || c.Network.Address != "100.1.2.3" {
		t.Fatalf("network = %+v", c.Network)
	}
	if c.Connection.Host != "10.0.0.5" || c.Connection.User != "admin" || c.Connection.Port != 2222 {
		t.Fatalf("connection = %+v", c.Connection)
	}
}

func TestList(t *testing.T) {
	dir := t.TempDir()
	for _, h := range []string{"b", "a"} {
		os.MkdirAll(filepath.Join(dir, h), 0o755)
		os.WriteFile(filepath.Join(dir, h, "configuration.yml"), []byte("network: {}\n"), 0o644)
	}
	os.MkdirAll(filepath.Join(dir, "nocfg"), 0o755) // no configuration.yml -> skipped
	got, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("List = %v", got)
	}
}
