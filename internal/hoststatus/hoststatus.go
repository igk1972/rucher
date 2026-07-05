// Package hoststatus collects agent status from every host over ssh and aggregates it.
package hoststatus

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"podman-essaim-compartment-manager/internal/agent"
	"podman-essaim-compartment-manager/internal/hostcfg"
	"podman-essaim-compartment-manager/internal/sshresolve"
	"podman-essaim-compartment-manager/internal/sshx"
)

const statusPath = "/var/lib/podman-essaim/agent-status.json"

type Row struct {
	Host      string   `json:"host"`
	Address   string   `json:"address"`
	Reachable bool     `json:"reachable"`
	Revision  string   `json:"revision"`
	Applied   int      `json:"applied"`
	Removed   int      `json:"removed"`
	Errors    []string `json:"errors,omitempty"`
	Live      string   `json:"live,omitempty"`
}

func Collect(r sshx.Runner, hostsDir, limaDir string, names []string, live bool) ([]Row, error) {
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
		cfg, err := hostcfg.Load(filepath.Join(hostsDir, name, "configuration.yml"))
		if err != nil {
			row.Errors = []string{err.Error()}
			rows = append(rows, row)
			continue
		}
		row.Address = cfg.Network.Address
		target, err := sshresolve.Resolve(name, cfg, limaDir)
		if err != nil {
			row.Errors = []string{err.Error()}
			rows = append(rows, row)
			continue
		}
		res, err := r.Run(target, []string{"cat", statusPath}, nil)
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
			if lv, err := r.Run(target, []string{"sudo", "pecm", "status"}, nil); err == nil {
				row.Live = lv.Stdout
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}
