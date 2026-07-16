// SPDX-License-Identifier: AGPL-3.0-or-later

// Package nodestatus collects agent status from every node over ssh and aggregates it.
package nodestatus

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

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
	Pending   bool     `json:"pending,omitempty"` // reachable, but the agent has not written a status file yet
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
	// Node-supplied output (live status, error text) is printed raw to the operator's
	// terminal; strip control sequences so a malicious node can't inject ANSI/OSC escapes.
	for i := range rows {
		rows[i].Live = sanitizeNodeOutput(rows[i].Live)
		for j := range rows[i].Errors {
			rows[i].Errors[j] = sanitizeNodeOutput(rows[i].Errors[j])
		}
	}
	return rows, nil
}

// sanitizeNodeOutput drops terminal control characters from node-supplied text. Printable
// runes, newline and tab are kept; every other control character (including ESC) is removed.
func sanitizeNodeOutput(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || !unicode.IsControl(r) {
			return r
		}
		return -1
	}, s)
}

// firstLineOr returns the first non-empty line of s, or fallback if there is none.
func firstLineOr(s, fallback string) string {
	if first := strings.TrimSpace(strings.SplitN(s, "\n", 2)[0]); first != "" {
		return first
	}
	return fallback
}

// collectOne fetches one node's status. Every failure is captured in the Row
// (Reachable stays false) rather than returned, so one node never aborts the run.
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
	// The status file is 0600 root, so read it via sudo — matching --live below and the
	// rest of the operator plane, which all assume the SSH user has passwordless sudo.
	res, err := r.Run(target, []string{"sudo", "cat", statusPath}, nil)
	if err != nil {
		// Per sshx.Runner, a non-nil error is a transport/session failure (dial, auth,
		// timeout): the command never ran, so the node is genuinely unreachable. Record
		// why so the operator can tell it from a plain "node down".
		row.Errors = append(row.Errors, err.Error())
		return row // Reachable stays false
	}
	// ssh connected and ran the probe, so the node IS reachable regardless of the
	// command's own exit status.
	row.Reachable = true
	switch {
	case res.Code != 0 && strings.Contains(res.Stderr, "No such file"):
		// The status file does not exist yet: the agent has never completed a pass — a
		// freshly deployed node, or a push-mode fleet driven by `node cadre apply` with no
		// pull agent. Healthy-but-pending, not a failure: leave the revision empty and
		// record no error, so it neither reads as failed nor bumps the exit code.
		row.Pending = true
	case res.Code != 0:
		// ssh worked but reading the file failed for some other reason (e.g. sudo is not
		// passwordless): surface it — dropping the stderr would hide a broken-but-reachable
		// node behind a green status. Unlike pending, this bumps the exit code.
		row.Errors = append(row.Errors, "read agent status: "+firstLineOr(res.Stderr, fmt.Sprintf("cat exited %d", res.Code)))
	default:
		var st agent.Status
		if err := json.Unmarshal([]byte(res.Stdout), &st); err != nil {
			// A corrupt status file must not read as a healthy node (empty revision, 0
			// applied) — surface it so the operator sees something is wrong.
			row.Errors = append(row.Errors, "unreadable agent status: "+err.Error())
		} else {
			row.Revision = st.Revision
			row.Applied = len(st.Applied)
			row.Removed = len(st.Removed)
			// A pass-level failure (store sync, placement, listing) has no per-cadre
			// Result to surface, so fold it in alongside them or the node reads healthy.
			if st.Error != "" {
				row.Errors = append(row.Errors, st.Error)
			}
			for _, a := range st.Applied {
				if !a.OK {
					row.Errors = append(row.Errors, a.Name+": "+a.Error)
				}
			}
		}
	}
	// The live probe is independent of the status file, so run it for every reachable
	// node — including a pending one (a push-mode node is permanently pending, yet its
	// live unit state is exactly what --live is for).
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
