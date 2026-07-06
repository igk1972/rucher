// Package nodestatus collects agent status from every node over ssh and aggregates it.
package nodestatus

import (
	"encoding/json"
	"fmt"
	"strings"

	"rucher/internal/agent"
	"rucher/internal/nodecfg"
	"rucher/internal/sshresolve"
	"rucher/internal/sshx"
)

const statusPath = "/var/lib/rucher/agent-status.json"

type Row struct {
	Node      string   `json:"node"`
	Address   string   `json:"address"`
	Reachable bool     `json:"reachable"`
	Revision  string   `json:"revision"`
	Applied   int      `json:"applied"`
	Removed   int      `json:"removed"`
	Errors    []string `json:"errors,omitempty"`
	Live      string   `json:"live,omitempty"`
}

func Collect(r sshx.Runner, nodesDir, limaDir string, names []string, live bool) ([]Row, error) {
	if len(names) == 0 {
		listed, err := nodecfg.List(nodesDir)
		if err != nil {
			return nil, err
		}
		names = listed
	}
	var rows []Row
	for _, name := range names {
		row := Row{Node: name}
		cfg, err := nodecfg.LoadMerged(nodesDir, name)
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
			if lv, err := r.Run(target, []string{"sudo", "rucher", "status"}, nil); err == nil {
				row.Live = lv.Stdout
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}
