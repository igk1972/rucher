package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"podman-essaim-compartment-manager/internal/compartment"
	"podman-essaim-compartment-manager/internal/host"
	"podman-essaim-compartment-manager/internal/plan"
	"podman-essaim-compartment-manager/internal/reconcile"
	"podman-essaim-compartment-manager/internal/state"
)

// discover returns compartment directories under dir, optionally filtered by names.
func discover(dir string, names []string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if len(names) > 0 && !slices.Contains(names, e.Name()) {
			continue
		}
		dirs = append(dirs, filepath.Join(dir, e.Name()))
	}
	return dirs, nil
}

func cmdPlan(dir string, names []string, out io.Writer) int {
	dirs, err := discover(dir, names)
	if err != nil {
		fmt.Fprintln(out, "error:", err)
		return 1
	}
	rc := 0
	for _, d := range dirs {
		c, err := compartment.Load(d)
		if err != nil {
			fmt.Fprintf(out, "%s: ERROR %v\n", filepath.Base(d), err)
			rc = 1
			continue
		}
		// dry-run: diff against empty prior state so the user sees the full intended change
		p := plan.Compute(c, nil, state.State{})
		fmt.Fprintf(out, "compartment %s:\n", c.Name)
		for _, u := range p.StartUnits {
			fmt.Fprintf(out, "  start   %s\n", u)
		}
		for _, u := range p.RestartUnits {
			fmt.Fprintf(out, "  restart %s\n", u)
		}
		for _, f := range p.WriteFiles {
			fmt.Fprintf(out, "  write   %s\n", f.Name)
		}
	}
	return rc
}

// cmdNew provisions a compartment's OS user and age identity, printing its recipient.
func cmdNew(name string, out io.Writer) int {
	rec, err := reconcile.New(host.NewExec(), name)
	if err != nil {
		fmt.Fprintf(out, "error: %v\n", err)
		return 1
	}
	fmt.Fprintln(out, rec)
	return 0
}

// cmdApply reconciles each discovered compartment against the host.
func cmdApply(dir string, names []string, out io.Writer) int {
	dirs, err := discover(dir, names)
	if err != nil {
		fmt.Fprintln(out, "error:", err)
		return 1
	}
	rc := 0
	for _, d := range dirs {
		c, err := compartment.Load(d)
		if err != nil {
			fmt.Fprintf(out, "%s: ERROR %v\n", filepath.Base(d), err)
			rc = 1
			continue
		}
		p, err := reconcile.Apply(host.NewExec(), c)
		if err != nil {
			fmt.Fprintf(out, "%s: ERROR %v\n", c.Name, err)
			rc = 1
			continue
		}
		fmt.Fprintf(out, "%s: started=%d restarted=%d\n", c.Name, len(p.StartUnits), len(p.RestartUnits))
	}
	return rc
}

// cmdAgeRecipient prints the compartment's stored age recipient.
func cmdAgeRecipient(name string, out io.Writer) int {
	rec, err := reconcile.Recipient(host.NewExec(), name)
	if err != nil {
		fmt.Fprintf(out, "error: %v\n", err)
		return 1
	}
	fmt.Fprintln(out, rec)
	return 0
}

// cmdRm stops a compartment's units; with purge it also removes its OS user.
func cmdRm(name string, purge bool, out io.Writer) int {
	if err := reconcile.Remove(host.NewExec(), name, purge); err != nil {
		fmt.Fprintf(out, "error: %v\n", err)
		return 1
	}
	return 0
}
