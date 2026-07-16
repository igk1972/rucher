// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"text/tabwriter"
	"time"

	"rucher/internal/nodestatus"
	"rucher/internal/sshx"
)

func limaDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".lima")
}

// knownHostsPath is where the native ssh client pins host keys (TOFU accept-new).
// RUCHER_KNOWN_HOSTS overrides the location so the store can be isolated for
// tests/CI or redirected per operator context.
func knownHostsPath() string {
	if p := os.Getenv("RUCHER_KNOWN_HOSTS"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		// No home dir: use a fresh, private (0700, unpredictable-name) temp dir so a co-tenant
		// on a shared /tmp cannot pre-create a predictable path and plant a known_hosts to
		// defeat TOFU. MkdirTemp creates the dir owned by us; pinning does not persist across
		// runs in this degraded case, which is acceptable. On the (pathological) failure to
		// make a temp dir, an empty path fails host-key setup closed downstream.
		dir, err := os.MkdirTemp("", "rucher-known-hosts-")
		if err != nil {
			return ""
		}
		return filepath.Join(dir, "known_hosts")
	}
	return filepath.Join(home, ".config", "rucher", "known_hosts")
}

// cmdNodesStatus gathers per-node status over ssh and renders it as either a
// human-readable table or, when jsonOut is set, a machine-readable JSON array.
func cmdNodesStatus(nodesDir string, names []string, live, jsonOut bool, concurrency int, out io.Writer) int {
	client := sshx.NewClient(knownHostsPath(), 10*time.Second)
	rows, err := nodestatus.Collect(client, nodesDir, limaDir(), names, live, concurrency)
	if err != nil {
		fmt.Fprintln(out, "error:", err)
		return 1
	}
	if jsonOut {
		return renderNodesJSON(out, rows)
	}
	return renderNodesTable(out, rows, live)
}

// renderNodesTable writes the status table plus an errors detail block and, when
// live is set, per-node live status blocks. It returns 1 if any node is unreachable
// or reported errors (a reachable node whose reconcile pass failed), else 0.
func renderNodesTable(out io.Writer, rows []nodestatus.Row, live bool) int {
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NODE\tADDRESS\tREACHABLE\tREVISION\tAPPLIED\tREMOVED\tERRORS")
	rc := 0
	for _, r := range rows {
		reach := "yes"
		if !r.Reachable {
			reach, rc = "no", 1
		}
		if len(r.Errors) > 0 {
			rc = 1 // a reachable node whose pass failed must not read as healthy
		}
		// A reachable node with no status file yet is pending, not failed: mark it in the
		// REVISION cell (genuinely N/A) instead of leaving a blank that reads as a healthy
		// node at the empty revision. It carries no error, so rc is left untouched.
		rev := r.Revision
		if r.Pending {
			rev = "pending"
		}
		errs := ""
		if len(r.Errors) > 0 {
			errs = strconv.Itoa(len(r.Errors))
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%d\t%s\n", r.Node, r.Address, reach, rev, r.Applied, r.Removed, errs)
	}
	tw.Flush()
	// The ERRORS column is only a count; print the actual messages below the
	// table so failures are legible without re-running a lower-level command.
	hasErrors := false
	for _, r := range rows {
		if len(r.Errors) > 0 {
			hasErrors = true
			break
		}
	}
	if hasErrors {
		fmt.Fprintln(out, "errors:")
		for _, r := range rows {
			for _, e := range r.Errors {
				fmt.Fprintf(out, "  %s: %s\n", r.Node, e)
			}
		}
	}
	if live {
		for _, r := range rows {
			if r.Live != "" {
				fmt.Fprintf(out, "\n--- %s ---\n%s\n", r.Node, r.Live)
			}
		}
	}
	return rc
}

// renderNodesJSON writes rows as an indented JSON array followed by a newline.
// It returns 1 if any node is unreachable or reported errors, else 0.
func renderNodesJSON(out io.Writer, rows []nodestatus.Row) int {
	rc := 0
	for _, r := range rows {
		if !r.Reachable || len(r.Errors) > 0 {
			rc = 1
			break
		}
	}
	// Marshal an empty slice (not nil) so a no-node result is `[]`, not `null`.
	if rows == nil {
		rows = []nodestatus.Row{}
	}
	b, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		fmt.Fprintln(out, "error:", err)
		return 1
	}
	fmt.Fprintf(out, "%s\n", b)
	return rc
}
