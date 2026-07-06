package sshresolve

import (
	"os"
	"path/filepath"
	"testing"

	"rucher/internal/nodecfg"
	"rucher/internal/sshx"
)

func TestResolveNetworkAddress(t *testing.T) {
	cfg := nodecfg.Config{Network: nodecfg.Network{Address: "100.1.1.1"}}
	got, err := Resolve("web", cfg, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	want := sshx.Target{Addr: "100.1.1.1:22", User: "root"}
	if got != want {
		t.Fatalf("target = %+v, want %+v", got, want)
	}
}

func TestResolveLima(t *testing.T) {
	lima := t.TempDir()
	os.MkdirAll(filepath.Join(lima, "web"), 0o755)
	os.WriteFile(filepath.Join(lima, "web", "ssh.config"),
		[]byte("Host lima-web\n  HostName 127.0.0.1\n  Port 2222\n  User alice\n  IdentityFile /k\n"), 0o644)
	got, err := Resolve("web", nodecfg.Config{}, lima)
	if err != nil {
		t.Fatal(err)
	}
	want := sshx.Target{Addr: "127.0.0.1:2222", User: "alice", Identity: "/k"}
	if got != want {
		t.Fatalf("target = %+v, want %+v", got, want)
	}
}

func TestResolveConnection(t *testing.T) {
	cfg := nodecfg.Config{Connection: nodecfg.Connection{Host: "10.0.0.5", User: "admin", Port: 2222, Identity: "/k"}}
	got, err := Resolve("web", cfg, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	want := sshx.Target{Addr: "10.0.0.5:2222", User: "admin", Identity: "/k"}
	if got != want {
		t.Fatalf("target = %+v, want %+v", got, want)
	}
}

func TestResolveError(t *testing.T) {
	if _, err := Resolve("web", nodecfg.Config{}, "/nonexistent"); err == nil {
		t.Fatal("expected error when no address is resolvable")
	}
}
