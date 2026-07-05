package sshresolve

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"podman-essaim-compartment-manager/internal/hostcfg"
)

func TestNetworkAddressWins(t *testing.T) {
	cfg := hostcfg.Config{Network: hostcfg.Network{Address: "100.1.1.1"}}
	got, err := SSHArgv("web", cfg, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != "ssh" || got[len(got)-1] != "root@100.1.1.1" {
		t.Fatalf("argv = %v", got)
	}
}

func TestBaseSSHOptionsPinned(t *testing.T) {
	// Guard the security-mandated non-interactive ssh options against an
	// accidental edit to base.
	cfg := hostcfg.Config{Network: hostcfg.Network{Address: "100.1.1.1"}}
	got, err := SSHArgv("web", cfg, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][]string{
		{"-o", "BatchMode=yes"},
		{"-o", "ConnectTimeout=5"},
		{"-o", "StrictHostKeyChecking=accept-new"},
	} {
		if !containsSeq(got, pair) {
			t.Fatalf("argv %v missing option %v", got, pair)
		}
	}
}

func TestLimaWhenConfigExists(t *testing.T) {
	lima := t.TempDir()
	os.MkdirAll(filepath.Join(lima, "web"), 0o755)
	os.WriteFile(filepath.Join(lima, "web", "ssh.config"), []byte("Host lima-web\n"), 0o644)
	got, err := SSHArgv("web", hostcfg.Config{}, lima)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-F", filepath.Join(lima, "web", "ssh.config"), "lima-web"}
	if !containsSeq(got, want) {
		t.Fatalf("argv = %v, want subseq %v", got, want)
	}
}

func TestConnectionFallback(t *testing.T) {
	cfg := hostcfg.Config{Connection: hostcfg.Connection{Host: "10.0.0.5", User: "admin", Port: 2222, Identity: "/k"}}
	got, err := SSHArgv("web", cfg, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if got[len(got)-1] != "admin@10.0.0.5" || !slices.Contains(got, "2222") || !slices.Contains(got, "/k") {
		t.Fatalf("argv = %v", got)
	}
}

func TestErrorWhenUnresolvable(t *testing.T) {
	if _, err := SSHArgv("web", hostcfg.Config{}, "/nonexistent"); err == nil {
		t.Fatal("expected error when no address is resolvable")
	}
}

func containsSeq(hay, needle []string) bool {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if slices.Equal(hay[i:i+len(needle)], needle) {
			return true
		}
	}
	return false
}
