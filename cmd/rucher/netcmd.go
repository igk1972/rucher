// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"rucher/internal/nodecfg"
)

// parseNetJoin reads a single positional <node>, a required --address <addr> and
// an optional --json flag that switches the success output to a JSON object.
func parseNetJoin(args []string) (nodeName, address string, jsonOut bool, err error) {
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
			// A real node name never starts with "-", so an unknown flag-looking
			// token is a typo, not the node (e.g. --drivr silently becoming a name).
			if strings.HasPrefix(args[i], "-") {
				return "", "", false, fmt.Errorf("unknown flag %q", args[i])
			}
			if nodeName != "" {
				return "", "", false, fmt.Errorf("unexpected argument %q", args[i])
			}
			nodeName = args[i]
		}
	}
	if nodeName == "" {
		return "", "", false, fmt.Errorf("usage: ops nodes join <node> --address <addr>")
	}
	if !haveAddress {
		return "", "", false, fmt.Errorf("ops nodes join requires --address")
	}
	// Trim surrounding whitespace: a padded value is cleaned before storing, and
	// an all-whitespace value collapses to "" and is rejected below as a usage
	// error rather than stored as a blank address.
	address = strings.TrimSpace(address)
	if address == "" {
		return "", "", false, fmt.Errorf("ops nodes join requires a non-empty --address")
	}
	return nodeName, address, jsonOut, nil
}

// cmdNetJoin records a node's static management address in its node config.
func cmdNetJoin(nodesDir string, args []string, out io.Writer) int {
	name, address, jsonOut, err := parseNetJoin(args)
	if err != nil {
		fmt.Fprintln(out, "error:", err)
		return 2
	}
	path := filepath.Join(nodesDir, name, "configuration.yml")
	if err := nodecfg.WriteNetwork(path, nodecfg.Network{Address: address}); err != nil {
		fmt.Fprintln(out, "error:", err)
		return 1
	}
	if jsonOut {
		// Compact machine-readable success line; field order matches the struct.
		b, err := json.Marshal(struct {
			Node    string `json:"node"`
			Address string `json:"address"`
		}{Node: name, Address: address})
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
