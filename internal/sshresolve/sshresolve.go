// Package sshresolve builds the `ssh` argv to reach a host, per config precedence.
package sshresolve

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"podman-essaim-compartment-manager/internal/hostcfg"
)

// common non-interactive ssh options.
var base = []string{"ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", "-o", "StrictHostKeyChecking=accept-new"}

// newArgv returns a fresh copy of base so appends never alias the shared slice.
func newArgv() []string { return append([]string(nil), base...) }

func userOr(u, def string) string {
	if u == "" {
		return def
	}
	return u
}

// SSHArgv returns the ssh argv prefix (without the remote command) for a host.
// Precedence: network.address -> lima ssh.config -> connection block.
func SSHArgv(name string, cfg hostcfg.Config, limaDir string) ([]string, error) {
	if cfg.Network.Address != "" {
		return append(newArgv(), userOr(cfg.Connection.User, "root")+"@"+cfg.Network.Address), nil
	}
	limaCfg := filepath.Join(limaDir, name, "ssh.config")
	if _, err := os.Stat(limaCfg); err == nil {
		return append(newArgv(), "-F", limaCfg, "lima-"+name), nil
	}
	if cfg.Connection.Host != "" {
		argv := newArgv()
		if cfg.Connection.Identity != "" {
			argv = append(argv, "-i", cfg.Connection.Identity)
		}
		if cfg.Connection.Port != 0 {
			argv = append(argv, "-p", strconv.Itoa(cfg.Connection.Port))
		}
		return append(argv, userOr(cfg.Connection.User, "root")+"@"+cfg.Connection.Host), nil
	}
	return nil, fmt.Errorf("host %s: no network.address, lima ssh.config, or connection.host", name)
}
