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
func knownHostsPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "known_hosts"
	}
	return filepath.Join(home, ".config", "rucher", "known_hosts")
}

// cmdNodesStatus gathers per-host status over ssh and renders it as either a
// human-readable table or, when jsonOut is set, a machine-readable JSON array.
func cmdNodesStatus(nodesDir string, names []string, live, jsonOut bool, out io.Writer) int {
	client := sshx.NewClient(knownHostsPath(), 10*time.Second)
	rows, err := nodestatus.Collect(client, nodesDir, limaDir(), names, live)
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
// live is set, per-host live status blocks. It returns 1 if any host is
// unreachable, else 0.
func renderNodesTable(out io.Writer, rows []nodestatus.Row, live bool) int {
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NODE\tADDRESS\tREACHABLE\tREVISION\tAPPLIED\tREMOVED\tERRORS")
	rc := 0
	for _, r := range rows {
		reach := "yes"
		if !r.Reachable {
			reach, rc = "no", 1
		}
		errs := ""
		if len(r.Errors) > 0 {
			errs = strconv.Itoa(len(r.Errors))
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%d\t%s\n", r.Node, r.Address, reach, r.Revision, r.Applied, r.Removed, errs)
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
// It returns 1 if any host is unreachable, else 0.
func renderNodesJSON(out io.Writer, rows []nodestatus.Row) int {
	rc := 0
	for _, r := range rows {
		if !r.Reachable {
			rc = 1
			break
		}
	}
	// Marshal an empty slice (not nil) so a no-host result is `[]`, not `null`.
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
