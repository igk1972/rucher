// SPDX-License-Identifier: AGPL-3.0-or-later

// Package nodestatus collects agent status from every node over ssh and aggregates it.
package nodestatus

import (
	"encoding/json"
	"fmt"
	"strings"

	"rucher/internal/agent"
	"rucher/internal/nodecfg"
	"rucher/internal/parallel"
	"rucher/internal/sshresolve"
	"rucher/internal/sshx"
)

// statusPath aliases agent.StatusPath so the reader and writer share one constant.
const statusPath = agent.StatusPath

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

// Collect gathers each node's status over ssh, at most concurrency nodes at a
// time (see parallel.Map for how concurrency <= 0 is interpreted). The returned
// rows are in the order of names, independent of the concurrency level.
func Collect(r sshx.Runner, nodesDir, limaDir string, names []string, live bool, concurrency int) ([]Row, error) {
	if len(names) == 0 {
		listed, err := nodecfg.List(nodesDir)
		if err != nil {
			return nil, err
		}
		names = listed
	}
	rows := parallel.Map(names, concurrency, func(name string) Row {
		return collectOne(r, nodesDir, limaDir, name, live)
	})
	return rows, nil
}

// collectOne fetches one node's status. Every failure is captured in the Row
// (Reachable stays false) rather than returned, so one node never aborts the run.
func firstLineOr(s, fallback string) string {
	if first := strings.TrimSpace(strings.SplitN(s, "\n", 2)[0]); first != "" {
		return first
	}
	return fallback
}

func collectOne(r sshx.Runner, nodesDir, limaDir, name string, live bool) Row {
	row := Row{Node: name}
	cfg, err := nodecfg.LoadMerged(nodesDir, name)
	if err != nil {
		row.Errors = []string{err.Error()}
		return row
	}
	row.Address = cfg.Network.Address
	target, err := sshresolve.Resolve(name, cfg, limaDir)
	if err != nil {
		row.Errors = []string{err.Error()}
		return row
	}
	res, err := r.Run(target, []string{"cat", statusPath}, nil)
	if err != nil || res.Code != 0 {
		// Record why the node is unreachable so the operator can tell a
		// transport/config failure from a plain "node down".
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
		return row // Reachable stays false
	}
	row.Reachable = true
	var st agent.Status
	if err := json.Unmarshal([]byte(res.Stdout), &st); err != nil {
		// A corrupt status file must not read as a healthy node (empty revision, 0
		// applied) — surface it so the operator sees something is wrong.
		row.Errors = append(row.Errors, "unreadable agent status: "+err.Error())
	} else {
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
		lv, err := r.Run(target, []string{"sudo", "rucher", "node", "cadre", "status"}, nil)
		switch {
		case err != nil:
			row.Errors = append(row.Errors, "live status: "+err.Error())
		case lv.Code != 0:
			row.Errors = append(row.Errors, "live status: "+firstLineOr(lv.Stderr, fmt.Sprintf("exited %d", lv.Code)))
		default:
			row.Live = lv.Stdout
		}
	}
	return row
}
