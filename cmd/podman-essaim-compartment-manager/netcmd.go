package main

import (
	"fmt"
	"io"
	"path/filepath"

	"podman-essaim-compartment-manager/internal/host"
	"podman-essaim-compartment-manager/internal/hostcfg"
	pnet "podman-essaim-compartment-manager/internal/net"
)

func parseNetJoin(args []string) (hostName, driver, overlayName, address string, err error) {
	driver = "tailscale"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--driver":
			if i+1 >= len(args) {
				return "", "", "", "", fmt.Errorf("--driver needs a value")
			}
			driver, i = args[i+1], i+1
		case "--overlay-name":
			if i+1 >= len(args) {
				return "", "", "", "", fmt.Errorf("--overlay-name needs a value")
			}
			overlayName, i = args[i+1], i+1
		case "--address":
			if i+1 >= len(args) {
				return "", "", "", "", fmt.Errorf("--address needs a value")
			}
			address, i = args[i+1], i+1
		default:
			if hostName != "" {
				return "", "", "", "", fmt.Errorf("unexpected argument %q", args[i])
			}
			hostName = args[i]
		}
	}
	if hostName == "" {
		return "", "", "", "", fmt.Errorf("usage: net join <host> [--driver ssh|tailscale] [--overlay-name N] [--address A]")
	}
	if overlayName == "" {
		overlayName = hostName
	}
	return hostName, driver, overlayName, address, nil
}

// cmdNetJoin resolves a host's overlay address and records it in its host config.
func cmdNetJoin(hostsDir string, args []string, out io.Writer) int {
	name, driver, overlayName, address, err := parseNetJoin(args)
	if err != nil {
		fmt.Fprintln(out, "error:", err)
		return 2
	}
	if address == "" {
		d, err := pnet.DriverFor(driver, host.NewExec())
		if err != nil {
			fmt.Fprintln(out, "error:", err)
			return 1
		}
		if address, err = d.ResolveAddress(overlayName); err != nil {
			fmt.Fprintln(out, "error:", err)
			return 1
		}
	}
	path := filepath.Join(hostsDir, name, "configuration.yml")
	if err := hostcfg.WriteNetwork(path, hostcfg.Network{Driver: driver, Address: address}); err != nil {
		fmt.Fprintln(out, "error:", err)
		return 1
	}
	fmt.Fprintf(out, "%s: network %s %s\n", name, driver, address)
	return 0
}
