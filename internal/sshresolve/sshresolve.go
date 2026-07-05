// Package sshresolve builds the `ssh` argv to reach a host, per config precedence.
package sshresolve

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kevinburke/ssh_config"

	"podman-essaim-compartment-manager/internal/hostcfg"
	"podman-essaim-compartment-manager/internal/sshx"
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

// Resolve turns a host config into a native sshx.Target.
// Precedence: network.address -> lima ssh.config -> connection block.
func Resolve(name string, cfg hostcfg.Config, limaDir string) (sshx.Target, error) {
	if cfg.Network.Address != "" {
		return sshx.Target{
			Addr:     net.JoinHostPort(cfg.Network.Address, "22"),
			User:     userOr(cfg.Connection.User, "root"),
			Identity: cfg.Connection.Identity,
		}, nil
	}
	limaCfg := filepath.Join(limaDir, name, "ssh.config")
	if _, err := os.Stat(limaCfg); err == nil {
		if f, err := os.Open(limaCfg); err == nil {
			defer f.Close()
			if parsed, err := ssh_config.Decode(f); err == nil {
				alias := "lima-" + name
				hostName, _ := parsed.Get(alias, "HostName")
				if hostName != "" {
					port, _ := parsed.Get(alias, "Port")
					if port == "" {
						port = "22"
					}
					user, _ := parsed.Get(alias, "User")
					identity, _ := parsed.Get(alias, "IdentityFile")
					identity = expandHome(identity)
					return sshx.Target{
						Addr:     net.JoinHostPort(hostName, port),
						User:     user,
						Identity: identity,
					}, nil
				}
			}
		}
	}
	if cfg.Connection.Host != "" {
		port := "22"
		if cfg.Connection.Port != 0 {
			port = strconv.Itoa(cfg.Connection.Port)
		}
		return sshx.Target{
			Addr:     net.JoinHostPort(cfg.Connection.Host, port),
			User:     userOr(cfg.Connection.User, "root"),
			Identity: cfg.Connection.Identity,
		}, nil
	}
	return sshx.Target{}, fmt.Errorf("host %s: no network.address, lima ssh.config, or connection.host", name)
}

// expandHome rewrites a leading "~/" to the user's home directory.
func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}
