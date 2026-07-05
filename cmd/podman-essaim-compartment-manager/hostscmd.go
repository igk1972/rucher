package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"text/tabwriter"

	"podman-essaim-compartment-manager/internal/host"
	"podman-essaim-compartment-manager/internal/hoststatus"
)

func limaDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".lima")
}

// cmdHostsStatus gathers per-host status over ssh and renders it as either a
// human-readable table or, when jsonOut is set, a machine-readable JSON array.
func cmdHostsStatus(hostsDir string, names []string, live, jsonOut bool, out io.Writer) int {
	rows, err := hoststatus.Collect(host.NewExec(), hostsDir, limaDir(), names, live)
	if err != nil {
		fmt.Fprintln(out, "error:", err)
		return 1
	}
	if jsonOut {
		return renderHostsJSON(out, rows)
	}
	return renderHostsTable(out, rows, live)
}

// renderHostsTable writes the status table plus an errors detail block and, when
// live is set, per-host live status blocks. It returns 1 if any host is
// unreachable, else 0.
func renderHostsTable(out io.Writer, rows []hoststatus.Row, live bool) int {
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "HOST\tADDRESS\tREACHABLE\tREVISION\tAPPLIED\tREMOVED\tERRORS")
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
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%d\t%s\n", r.Host, r.Address, reach, r.Revision, r.Applied, r.Removed, errs)
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
				fmt.Fprintf(out, "  %s: %s\n", r.Host, e)
			}
		}
	}
	if live {
		for _, r := range rows {
			if r.Live != "" {
				fmt.Fprintf(out, "\n--- %s ---\n%s\n", r.Host, r.Live)
			}
		}
	}
	return rc
}

// renderHostsJSON writes rows as an indented JSON array followed by a newline.
// It returns 1 if any host is unreachable, else 0.
func renderHostsJSON(out io.Writer, rows []hoststatus.Row) int {
	rc := 0
	for _, r := range rows {
		if !r.Reachable {
			rc = 1
			break
		}
	}
	// Marshal an empty slice (not nil) so a no-host result is `[]`, not `null`.
	if rows == nil {
		rows = []hoststatus.Row{}
	}
	b, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		fmt.Fprintln(out, "error:", err)
		return 1
	}
	fmt.Fprintf(out, "%s\n", b)
	return rc
}
