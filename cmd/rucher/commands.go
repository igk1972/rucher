package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"rucher/internal/compartment"
	"rucher/internal/node"
	"rucher/internal/ops"
	"rucher/internal/plan"
	"rucher/internal/provision"
	"rucher/internal/reconcile"
	"rucher/internal/state"
)

// discover returns compartment directories under dir, optionally filtered by names.
// When names is non-empty, every requested name must resolve to a subdirectory of dir;
// a name with no matching subdirectory is an error (guards against pointing --dir at the
// compartment folder itself instead of its parent, which would otherwise match nothing).
func discover(dir string, names []string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	subdirs := make(map[string]bool)
	var dirs []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		subdirs[e.Name()] = true
		if len(names) == 0 {
			dirs = append(dirs, filepath.Join(dir, e.Name()))
		}
	}
	if len(names) > 0 {
		seen := make(map[string]bool)
		for _, name := range names {
			if !subdirs[name] {
				return nil, fmt.Errorf("compartment %q not found in %s", name, dir)
			}
			if seen[name] {
				continue // a repeated name reconciles the compartment once, not twice
			}
			seen[name] = true
			dirs = append(dirs, filepath.Join(dir, name))
		}
	}
	return dirs, nil
}

func cmdPlan(dir string, names []string, out io.Writer) int {
	dirs, err := discover(dir, names)
	if err != nil {
		fmt.Fprintln(out, "error:", err)
		return 1
	}
	if len(dirs) == 0 {
		fmt.Fprintf(out, "no compartments found in %s\n", dir)
		return 0
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
	rec, err := reconcile.New(node.NewExec(), name)
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
	if len(dirs) == 0 {
		fmt.Fprintf(out, "no compartments found in %s\n", dir)
		return 0
	}
	rc := 0
	for _, d := range dirs {
		c, err := compartment.Load(d)
		if err != nil {
			fmt.Fprintf(out, "%s: ERROR %v\n", filepath.Base(d), err)
			rc = 1
			continue
		}
		p, err := reconcile.Apply(node.NewExec(), c)
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
	rec, err := reconcile.Recipient(node.NewExec(), name)
	if err != nil {
		fmt.Fprintf(out, "error: %v\n", err)
		return 1
	}
	fmt.Fprintln(out, rec)
	return 0
}

// cmdStatus prints the runtime state of each compartment's units.
// With no names it reports every compartment that has a persisted state file.
func cmdStatus(names []string, out io.Writer) int {
	if len(names) == 0 {
		listed, err := reconcile.List()
		if err != nil {
			fmt.Fprintln(out, "error:", err)
			return 1
		}
		names = listed
	}
	r := node.NewExec()
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "COMPARTMENT\tUNIT\tACTIVE\tSUB")
	rc := 0
	for _, name := range names {
		units, err := reconcile.Status(r, name)
		if err != nil {
			fmt.Fprintf(tw, "%s\tERROR\t%v\t\n", name, err)
			rc = 1
			continue
		}
		for _, u := range units {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", name, u.Unit, u.Active, u.Sub)
		}
	}
	tw.Flush()
	return rc
}

// cmdLogs prints the last 200 journal lines for one of a compartment's units.
// A system user's own `journalctl --user` cannot open the journal, so the entries
// are read as root filtered to the user's unit (_SYSTEMD_USER_UNIT + _UID).
func cmdLogs(name, unit string, out io.Writer) int {
	r := node.NewExec()
	res, err := r.Root([]string{"id", "-u", provision.UserName(name)}, nil)
	if err != nil || res.Code != 0 {
		fmt.Fprintf(out, "error: unknown compartment %s\n", name)
		return 1
	}
	uid := strings.TrimSpace(res.Stdout)
	argv := []string{
		"journalctl", "_SYSTEMD_USER_UNIT=" + ops.UnitService(unit), "_UID=" + uid,
		"-n", "200", "--no-pager",
	}
	res, err = r.Root(argv, nil)
	if err != nil {
		fmt.Fprintf(out, "error: %v\n", err)
		return 1
	}
	fmt.Fprint(out, res.Stdout)
	if res.Code != 0 {
		fmt.Fprint(out, res.Stderr)
		return 1
	}
	return 0
}

// cmdRm stops a compartment's units; with purge it also removes its OS user.
func cmdRm(name string, purge bool, out io.Writer) int {
	if err := reconcile.Remove(node.NewExec(), name, purge); err != nil {
		fmt.Fprintf(out, "error: %v\n", err)
		return 1
	}
	return 0
}
