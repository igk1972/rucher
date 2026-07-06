package main

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"podman-essaim-compartment-manager/internal/hostcfg"
)

// parseNetJoin reads a single positional <host> and a required --address <addr>.
func parseNetJoin(args []string) (hostName, address string, err error) {
	haveAddress := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--address":
			if i+1 >= len(args) {
				return "", "", fmt.Errorf("--address needs a value")
			}
			address, haveAddress, i = args[i+1], true, i+1
		default:
			// A real host name never starts with "-", so an unknown flag-looking
			// token is a typo, not the host (e.g. --drivr silently becoming a name).
			if strings.HasPrefix(args[i], "-") {
				return "", "", fmt.Errorf("unknown flag %q", args[i])
			}
			if hostName != "" {
				return "", "", fmt.Errorf("unexpected argument %q", args[i])
			}
			hostName = args[i]
		}
	}
	if hostName == "" {
		return "", "", fmt.Errorf("usage: net join <host> --address <addr>")
	}
	if !haveAddress {
		return "", "", fmt.Errorf("net join requires --address")
	}
	return hostName, address, nil
}

// cmdNetJoin records a host's static management address in its host config.
func cmdNetJoin(hostsDir string, args []string, out io.Writer) int {
	name, address, err := parseNetJoin(args)
	if err != nil {
		fmt.Fprintln(out, "error:", err)
		return 2
	}
	path := filepath.Join(hostsDir, name, "configuration.yml")
	if err := hostcfg.WriteNetwork(path, hostcfg.Network{Address: address}); err != nil {
		fmt.Fprintln(out, "error:", err)
		return 1
	}
	fmt.Fprintf(out, "%s: network %s\n", name, address)
	return 0
}
