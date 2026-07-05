// Package net resolves a host's overlay address on the operator's machine.
package net

import (
	"fmt"
	"strings"

	"podman-essaim-compartment-manager/internal/host"
)

type Driver interface {
	ResolveAddress(overlayName string) (string, error)
}

func DriverFor(kind string, r host.Runner) (Driver, error) {
	switch kind {
	case "ssh":
		return sshDriver{}, nil
	case "tailscale":
		return tailscaleDriver{r: r}, nil
	default:
		return nil, fmt.Errorf("unknown network driver %q (want ssh|tailscale)", kind)
	}
}

// sshDriver has no overlay to query: the address must be supplied by the operator.
type sshDriver struct{}

func (sshDriver) ResolveAddress(string) (string, error) {
	return "", fmt.Errorf("ssh driver requires an explicit --address")
}

// tailscaleDriver resolves via the local `tailscale` client.
type tailscaleDriver struct{ r host.Runner }

func (d tailscaleDriver) ResolveAddress(overlayName string) (string, error) {
	res, err := d.r.Root([]string{"tailscale", "ip", "-4", overlayName}, nil)
	if err != nil {
		return "", fmt.Errorf("tailscale ip: %w", err)
	}
	if res.Code != 0 {
		return "", fmt.Errorf("tailscale ip %s exited %d: %s", overlayName, res.Code, res.Stderr)
	}
	addr := strings.TrimSpace(strings.SplitN(res.Stdout, "\n", 2)[0])
	if addr == "" {
		return "", fmt.Errorf("tailscale returned no address for %q", overlayName)
	}
	return addr, nil
}
