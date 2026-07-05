// Package hoststatus collects agent status from every host over ssh and aggregates it.
package hoststatus

import (
	"encoding/json"
	"fmt"
	"strings"

	"podman-essaim-compartment-manager/internal/agent"
	"podman-essaim-compartment-manager/internal/host"
	"podman-essaim-compartment-manager/internal/hostcfg"
	"podman-essaim-compartment-manager/internal/sshresolve"
)

const statusPath = "/var/lib/podman-essaim/agent-status.json"

type Row struct {
	Host      string
	Address   string
	Reachable bool
	Revision  string
	Applied   int
	Removed   int
	Errors    []string
	Live      string
}

func Collect(r host.Runner, hostsDir, limaDir string, names []string, live bool) ([]Row, error) {
	if len(names) == 0 {
		listed, err := hostcfg.List(hostsDir)
		if err != nil {
			return nil, err
		}
		names = listed
	}
	var rows []Row
	for _, name := range names {
		row := Row{Host: name}
		cfg, err := hostcfg.Load(hostsDir + "/" + name + "/configuration.yml")
		if err != nil {
			row.Errors = []string{err.Error()}
			rows = append(rows, row)
			continue
		}
		row.Address = cfg.Network.Address
		argv, err := sshresolve.SSHArgv(name, cfg, limaDir)
		if err != nil {
			row.Errors = []string{err.Error()}
			rows = append(rows, row)
			continue
		}
		res, err := r.Root(append(argv, "cat", statusPath), nil)
		if err != nil || res.Code != 0 {
			// Record why the host is unreachable so the operator can tell a
			// transport/config failure from a plain "host down".
			switch {
			case err != nil:
				row.Errors = append(row.Errors, err.Error())
			default:
				if first := strings.TrimSpace(strings.SplitN(res.Stderr, "\n", 2)[0]); first != "" {
					row.Errors = append(row.Errors, first)
				} else {
					row.Errors = append(row.Errors, fmt.Sprintf("ssh exited %d", res.Code))
				}
			}
			rows = append(rows, row) // Reachable stays false
			continue
		}
		row.Reachable = true
		var st agent.Status
		if json.Unmarshal([]byte(res.Stdout), &st) == nil {
			row.Revision = st.Revision
			row.Applied = len(st.Applied)
			row.Removed = len(st.Removed)
			for _, a := range st.Applied {
				if !a.OK {
					row.Errors = append(row.Errors, a.Name+": "+a.Error)
				}
			}
		}
		if live {
			if lv, err := r.Root(append(argv, "sudo", "pecm", "status"), nil); err == nil {
				row.Live = lv.Stdout
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}
