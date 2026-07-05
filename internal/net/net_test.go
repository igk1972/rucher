package net

import (
	"testing"

	"podman-essaim-compartment-manager/internal/host"
)

func TestTailscaleResolveAddress(t *testing.T) {
	f := &host.Fake{Responses: map[string]host.Result{
		"root:tailscale ip -4 web": {Stdout: "100.5.6.7\n"},
	}}
	d, err := DriverFor("tailscale", f)
	if err != nil {
		t.Fatal(err)
	}
	got, err := d.ResolveAddress("web")
	if err != nil {
		t.Fatal(err)
	}
	if got != "100.5.6.7" {
		t.Fatalf("address = %q", got)
	}
}

func TestSSHDriverRequiresAddress(t *testing.T) {
	d, _ := DriverFor("ssh", &host.Fake{})
	if _, err := d.ResolveAddress("web"); err == nil {
		t.Fatal("expected error from ssh driver ResolveAddress")
	}
}

func TestUnknownDriver(t *testing.T) {
	if _, err := DriverFor("wireguard", &host.Fake{}); err == nil {
		t.Fatal("expected error for unknown driver")
	}
}
