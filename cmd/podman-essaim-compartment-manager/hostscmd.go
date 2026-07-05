package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
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

// cmdHostsStatus prints a per-host status table gathered over ssh.
func cmdHostsStatus(hostsDir string, names []string, live bool, out io.Writer) int {
	rows, err := hoststatus.Collect(host.NewExec(), hostsDir, limaDir(), names, live)
	if err != nil {
		fmt.Fprintln(out, "error:", err)
		return 1
	}
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
			errs = fmt.Sprintf("%d", len(r.Errors))
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
				fmt.Fprintf(out, "\n--- %s ---\n%s", r.Host, r.Live)
			}
		}
	}
	return rc
}
