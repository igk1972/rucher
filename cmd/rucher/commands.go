// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"rucher/internal/cadre"
	"rucher/internal/node"
	"rucher/internal/nodecfg"
	"rucher/internal/ops"
	"rucher/internal/plan"
	"rucher/internal/provision"
	"rucher/internal/prune"
	"rucher/internal/quadletlint"
	"rucher/internal/reconcile"
	"rucher/internal/state"
)

// discover returns cadre directories under dir, optionally filtered by names.
// When names is non-empty, every requested name must resolve to a subdirectory of dir;
// a name with no matching subdirectory is an error (guards against pointing --dir at the
// cadre folder itself instead of its parent, which would otherwise match nothing).
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
				return nil, fmt.Errorf("cadre %q not found in %s", name, dir)
			}
			if seen[name] {
				continue // a repeated name reconciles the cadre once, not twice
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
		fmt.Fprintf(out, "no cadres found in %s\n", dir)
		return 0
	}
	rc := 0
	for _, d := range dirs {
		c, err := cadre.Load(d)
		if err != nil {
			fmt.Fprintf(out, "%s: ERROR %v\n", filepath.Base(d), err)
			rc = 1
			continue
		}
		// dry-run: diff against empty prior state so the user sees the full intended
		// change, including the synthesized prune units
		c.Files = append(c.Files, prune.Files(c.Manifest.Prune)...)
		p := plan.Compute(c, nil, state.State{})
		fmt.Fprintf(out, "cadre %s:\n", c.Name)
		for _, f := range p.WriteFiles {
			fmt.Fprintf(out, "  write    %s\n", f.Name)
		}
		for _, name := range p.RemoveFiles {
			fmt.Fprintf(out, "  remove   %s\n", name)
		}
		for _, k := range p.CreateSecrets {
			fmt.Fprintf(out, "  secret+  %s\n", k)
		}
		for _, k := range p.RemoveSecrets {
			fmt.Fprintf(out, "  secret-  %s\n", k)
		}
		for _, u := range p.StartUnits {
			fmt.Fprintf(out, "  start    %s\n", u)
		}
		for _, u := range p.RestartUnits {
			fmt.Fprintf(out, "  restart  %s\n", u)
		}
		for _, u := range p.StopUnits {
			fmt.Fprintf(out, "  stop     %s\n", u)
		}
		for _, u := range p.EnableUnits {
			fmt.Fprintf(out, "  enable   %s\n", u)
		}
		for _, u := range p.RestartSystemdUnits {
			fmt.Fprintf(out, "  restart  %s\n", u)
		}
		for _, u := range p.DisableUnits {
			fmt.Fprintf(out, "  disable  %s\n", u)
		}
	}
	return rc
}

// cmdValidate loads every discovered cadre and reports its problems: a bad manifest
// (strict decode / manifest.Validate) or a bad unit file (missing [Section] or Quadlet
// type section, an EnvironmentFile pointing at a file the cadre does not ship), plus a
// deeper semantic check of every Quadlet unit via Podman's own parser (unknown key,
// missing Image, invalid values — quadletlint). It touches no node — a pure, pre-commit
// check of the cadre directory. Secret keys and resource-limit formats are not checked
// here; they need decrypted secrets / systemd's own parsing (see cadre.Validate).
// Advisory findings (cadre.Warnings + quadlet warnings) are printed as WARN lines and do
// not affect the exit code; errors are ERROR lines and fail the run.
func cmdValidate(dir string, names []string, out io.Writer) int {
	dirs, err := discover(dir, names)
	if err != nil {
		fmt.Fprintln(out, "error:", err)
		return 1
	}
	if len(dirs) == 0 {
		fmt.Fprintf(out, "no cadres found in %s\n", dir)
		return 0
	}
	rc := 0
	for _, d := range dirs {
		c, err := cadre.Load(d)
		if err != nil {
			fmt.Fprintf(out, "%s: ERROR %v\n", filepath.Base(d), err)
			rc = 1
			continue
		}
		// Deep-check the Quadlet units with Podman's parser (operator-side only).
		units := map[string]string{}
		for _, f := range c.Files {
			if f.IsUnit {
				units[f.Name] = string(f.Content)
			}
		}
		warnings, fatal := quadletlint.Check(units)
		for _, w := range c.Warnings() {
			fmt.Fprintf(out, "%s: WARN %s\n", c.Name, w)
		}
		for _, w := range warnings {
			fmt.Fprintf(out, "%s: WARN %s\n", c.Name, w)
		}
		for _, e := range fatal {
			fmt.Fprintf(out, "%s: ERROR %s\n", c.Name, e)
		}
		if len(fatal) > 0 {
			rc = 1
			continue
		}
		fmt.Fprintf(out, "%s: OK\n", filepath.Base(d))
	}
	return rc
}

// validateNodeConfigs strict-checks each nodesDir/<name>/configuration.yml. The runtime
// path (deploy/status) tolerates unknown keys; validation is where a typo is caught before
// a deploy. A missing nodes dir is not an error — a cadre-only checkout has none.
func validateNodeConfigs(nodesDir string, out io.Writer) int {
	names, err := nodecfg.List(nodesDir)
	if err != nil {
		return 0
	}
	rc := 0
	for _, n := range names {
		if err := nodecfg.ValidateMerged(nodesDir, n); err != nil {
			fmt.Fprintf(out, "node %s: ERROR %v\n", n, err)
			rc = 1
			continue
		}
		fmt.Fprintf(out, "node %s: OK\n", n)
	}
	return rc
}

// cmdNew provisions a cadre's OS user and age identity, printing its recipient.
func cmdNew(name string, out io.Writer) int {
	unlock, err := provision.LockNode()
	if err != nil {
		fmt.Fprintf(out, "error: %v\n", err)
		return 1
	}
	defer unlock()
	rec, err := reconcile.New(node.NewExec(), name)
	if err != nil {
		fmt.Fprintf(out, "error: %v\n", err)
		return 1
	}
	fmt.Fprintln(out, rec)
	return 0
}

// cmdApply reconciles each discovered cadre against the host.
func cmdApply(dir string, names []string, out io.Writer) int {
	dirs, err := discover(dir, names)
	if err != nil {
		fmt.Fprintln(out, "error:", err)
		return 1
	}
	if len(dirs) == 0 {
		fmt.Fprintf(out, "no cadres found in %s\n", dir)
		return 0
	}
	// Serialize the whole run against a concurrent agent pass (shared subuid map + state).
	unlock, err := provision.LockNode()
	if err != nil {
		fmt.Fprintln(out, "error:", err)
		return 1
	}
	defer unlock()
	rc := 0
	for _, d := range dirs {
		c, err := cadre.Load(d)
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

// cmdAgeRecipient prints the cadre's stored age recipient.
func cmdAgeRecipient(name string, out io.Writer) int {
	rec, err := reconcile.Recipient(node.NewExec(), name)
	if err != nil {
		fmt.Fprintf(out, "error: %v\n", err)
		return 1
	}
	fmt.Fprintln(out, rec)
	return 0
}

// cmdStatus prints the runtime state of each cadre's units.
// With no names it reports every cadre that has a persisted state file.
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
	fmt.Fprintln(tw, "CADRE\tUNIT\tACTIVE\tSUB")
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

// cmdLogs prints the last 200 journal lines for one of a cadre's units. rucher runs as
// root, so it reads the journal as root, filtered to the cadre user's unit
// (_SYSTEMD_USER_UNIT + _UID) — this works regardless of which journal file holds them.
func cmdLogs(name, unit string, out io.Writer) int {
	r := node.NewExec()
	res, err := r.Root([]string{"id", "-u", provision.UserName(name)}, nil)
	if err != nil || res.Code != 0 {
		fmt.Fprintf(out, "error: unknown cadre %s\n", name)
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

// cmdRm stops a cadre's units; with purge it also removes its OS user.
func cmdRm(name string, purge bool, out io.Writer) int {
	unlock, err := provision.LockNode()
	if err != nil {
		fmt.Fprintf(out, "error: %v\n", err)
		return 1
	}
	defer unlock()
	if err := reconcile.Remove(node.NewExec(), name, purge); err != nil {
		fmt.Fprintf(out, "error: %v\n", err)
		return 1
	}
	return 0
}
