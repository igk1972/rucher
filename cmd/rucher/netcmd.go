package main

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"rucher/internal/hostcfg"
)

// parseNetJoin reads a single positional <host>, a required --address <addr> and
// an optional --json flag that switches the success output to a JSON object.
func parseNetJoin(args []string) (hostName, address string, jsonOut bool, err error) {
	haveAddress := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--address":
			if i+1 >= len(args) {
				return "", "", false, fmt.Errorf("--address needs a value")
			}
			address, haveAddress, i = args[i+1], true, i+1
		case "--json":
			jsonOut = true
		default:
			// A real host name never starts with "-", so an unknown flag-looking
			// token is a typo, not the host (e.g. --drivr silently becoming a name).
			if strings.HasPrefix(args[i], "-") {
				return "", "", false, fmt.Errorf("unknown flag %q", args[i])
			}
			if hostName != "" {
				return "", "", false, fmt.Errorf("unexpected argument %q", args[i])
			}
			hostName = args[i]
		}
	}
	if hostName == "" {
		return "", "", false, fmt.Errorf("usage: net join <host> --address <addr>")
	}
	if !haveAddress {
		return "", "", false, fmt.Errorf("net join requires --address")
	}
	// Trim surrounding whitespace: a padded value is cleaned before storing, and
	// an all-whitespace value collapses to "" and is rejected below as a usage
	// error rather than stored as a blank address.
	address = strings.TrimSpace(address)
	if address == "" {
		return "", "", false, fmt.Errorf("net join requires a non-empty --address")
	}
	return hostName, address, jsonOut, nil
}

// cmdNetJoin records a host's static management address in its host config.
func cmdNetJoin(hostsDir string, args []string, out io.Writer) int {
	name, address, jsonOut, err := parseNetJoin(args)
	if err != nil {
		fmt.Fprintln(out, "error:", err)
		return 2
	}
	path := filepath.Join(hostsDir, name, "configuration.yml")
	if err := hostcfg.WriteNetwork(path, hostcfg.Network{Address: address}); err != nil {
		fmt.Fprintln(out, "error:", err)
		return 1
	}
	if jsonOut {
		// Compact machine-readable success line; field order matches the struct.
		b, err := json.Marshal(struct {
			Host    string `json:"host"`
			Address string `json:"address"`
		}{Host: name, Address: address})
		if err != nil {
			fmt.Fprintln(out, "error:", err)
			return 1
		}
		fmt.Fprintf(out, "%s\n", b)
		return 0
	}
	fmt.Fprintf(out, "%s: network %s\n", name, address)
	return 0
}
