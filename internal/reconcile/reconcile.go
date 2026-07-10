// SPDX-License-Identifier: AGPL-3.0-or-later

// Package reconcile applies a cadre's desired state to the host.
package reconcile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"rucher/internal/cadre"
	"rucher/internal/fileset"
	"rucher/internal/node"
	"rucher/internal/ops"
	"rucher/internal/plan"
	"rucher/internal/provision"
	"rucher/internal/secrets"
	"rucher/internal/state"
)

// stateBaseDir is where per-cadre state files live. RUCHER_STATE_DIR overrides the
// default (useful for tests and alternative layouts); empty falls back to provision.BaseDir().
func stateBaseDir() string {
	if d := os.Getenv("RUCHER_STATE_DIR"); d != "" {
		return d
	}
	return provision.BaseDir()
}

func statePath(name string) string {
	return filepath.Join(stateBaseDir(), "state", name+".json")
}

func systemdDir(name string) string {
	return provision.HomeDir(name) + "/.config/containers/systemd"
}

// userUnitDir is where a cadre's native systemd units (.timer/.socket/.path) are
// installed. systemd's user manager reads this path; it does not read the Quadlet dir.
func userUnitDir(name string) string {
	return provision.HomeDir(name) + "/.config/systemd/user"
}

func ageDir(name string) string {
	return provision.HomeDir(name) + "/.config/rucher/age"
}

func IdentityPath(name string) string  { return ageDir(name) + "/identity.txt" }
func recipientPath(name string) string { return ageDir(name) + "/recipient.txt" }

// New ensures the cadre's OS user and age identity exist and returns its age recipient.
func New(r node.Runner, name string) (string, error) {
	uid, err := provision.EnsureUser(r, name)
	if err != nil {
		return "", err
	}
	user := provision.UserName(name)
	if _, err := r.User(user, uid, []string{"mkdir", "-p", ageDir(name)}, nil); err != nil {
		return "", err
	}
	idp := IdentityPath(name)
	if res, _ := r.User(user, uid, []string{"test", "-f", idp}, nil); res.Code != 0 {
		recipient, err := ops.Ops{R: r, User: user, UID: uid}.GenerateAgeKey(idp)
		if err != nil {
			return "", err
		}
		if _, err := r.User(user, uid, []string{"tee", recipientPath(name)}, []byte(recipient+"\n")); err != nil {
			return "", err
		}
		return recipient, nil
	}
	return Recipient(r, name)
}

// Recipient returns the cadre's stored age recipient (root reads the user's file).
func Recipient(r node.Runner, name string) (string, error) {
	res, err := r.Root([]string{"cat", recipientPath(name)}, nil)
	if err != nil {
		return "", err
	}
	if res.Code != 0 {
		return "", fmt.Errorf("recipient for %s: %s", name, res.Stderr)
	}
	return strings.TrimSpace(res.Stdout), nil
}

// List returns the names of cadres that have a persisted state file.
func List() ([]string, error) {
	dir := filepath.Join(stateBaseDir(), "state")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if name, ok := strings.CutSuffix(e.Name(), ".json"); ok {
			names = append(names, name)
		}
	}
	return names, nil
}

// UnitStatus is the runtime state of one of a cadre's units.
type UnitStatus struct{ Unit, Active, Sub string }

// Status reports the ActiveState/SubState of each unit in the cadre's last-applied state.
func Status(r node.Runner, name string) ([]UnitStatus, error) {
	prior, err := state.Load(statePath(name))
	if err != nil {
		return nil, err
	}
	user := provision.UserName(name)
	var out []UnitStatus
	// show queries one unit; target is the systemd name to query (the Quadlet-generated
	// service for a Quadlet unit, or the unit itself for a native systemd unit), while
	// unit is the cadre-facing filename reported back.
	show := func(unit, target string) error {
		argv := []string{"systemctl", "--user", "show", target, "-p", "ActiveState", "-p", "SubState", "--value"}
		res, err := r.User(user, prior.UID, argv, nil)
		if err != nil {
			return err
		}
		// --value prints the properties' values one per line, in the -p order.
		lines := strings.Split(strings.TrimRight(res.Stdout, "\n"), "\n")
		st := UnitStatus{Unit: unit}
		if len(lines) > 0 {
			st.Active = lines[0]
		}
		if len(lines) > 1 {
			st.Sub = lines[1]
		}
		out = append(out, st)
		return nil
	}
	for _, u := range prior.Units {
		if err := show(u, ops.UnitService(u)); err != nil {
			return nil, err
		}
	}
	for _, u := range prior.SystemdUnits {
		if err := show(u, u); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Remove unmanages a cadre: it stops the workloads and deletes their unit files
// (so nothing restarts on boot), then drops the state file. The user, its podman
// secrets/volumes and the age identity are kept. With purge it additionally tears down
// the OS user and its home.
func Remove(r node.Runner, name string, purge bool) error {
	prior, _ := state.Load(statePath(name))
	user := provision.UserName(name)

	if prior.UID != 0 {
		o := ops.Ops{R: r, User: user, UID: prior.UID}
		// Stop workloads and remove their unit files so they don't come back on boot.
		// Best-effort: a cadre with no live manager makes these no-ops.
		for _, u := range prior.Units {
			o.Stop(u)
		}
		for _, u := range prior.SystemdUnits {
			o.DisableNow(u)
		}
		r.User(o.User, prior.UID, []string{"rm", "-rf", systemdDir(name)}, nil)
		r.User(o.User, prior.UID, []string{"rm", "-rf", userUnitDir(name)}, nil)
		o.DaemonReload()
	}

	// The cadre is no longer managed once its state file is gone.
	r.Root([]string{"rm", "-f", statePath(name)}, nil)

	if !purge {
		return nil
	}
	// Graceful teardown before deleting the account: SIGKILL is a last resort, not the
	// path. disable-linger first so the manager isn't re-spawned; stop every loaded
	// service so each workload gets its TimeoutStopSec (stateful workloads exit cleanly);
	// kill the rootless pause process (it outlives the manager and would block userdel —
	// why the old code went straight to SIGKILL); stop the manager; then wait, bounded,
	// for the processes to exit. All best-effort (a user with no live manager -> no-ops).
	r.Root([]string{"loginctl", "disable-linger", user}, nil)
	if prior.UID != 0 {
		o := ops.Ops{R: r, User: user, UID: prior.UID}
		o.StopAllUserServices()
		o.KillPause()
		r.Root([]string{"systemctl", "stop", fmt.Sprintf("user@%d.service", prior.UID)}, nil)
	}
	r.Root([]string{"loginctl", "terminate-user", user}, nil)
	// Bounded wait for the user's processes to exit ($1 = user, no shell injection),
	// instead of a fixed sleep that races the async terminate.
	r.Root([]string{"sh", "-c", `for i in $(seq 1 100); do pgrep -u "$1" >/dev/null 2>&1 || exit 0; sleep 0.1; done`, "_", user}, nil)
	r.Root([]string{"pkill", "-KILL", "-u", user}, nil) // last resort for anything that ignored SIGTERM
	r.Root([]string{"sleep", "1"}, nil)                 // let the kernel reap any killed processes
	res, err := r.Root([]string{"userdel", "-r", user}, nil)
	if err != nil {
		return err
	}
	if res.Code != 0 {
		return fmt.Errorf("userdel %s: %s", user, res.Stderr)
	}
	// The resource slice drop-in (provision.ApplyResources) is keyed by uid; leaving it
	// orphaned would silently bind a future user that reuses this uid. Best-effort.
	if prior.UID != 0 {
		r.Root([]string{"rm", "-rf", fmt.Sprintf("/etc/systemd/system/user-%d.slice.d", prior.UID)}, nil)
		r.Root([]string{"systemctl", "daemon-reload"}, nil)
	}
	return nil
}

func Apply(r node.Runner, c cadre.Cadre) (plan.Plan, error) {
	uid, err := provision.EnsureUser(r, c.Name)
	if err != nil {
		return plan.Plan{}, err
	}
	o := ops.Ops{R: r, User: provision.UserName(c.Name), UID: uid}

	var secretHashes map[string]string
	var secretValues map[string]string
	if c.SopsPath != "" {
		secretValues, err = secrets.Decrypt(IdentityPath(c.Name), c.SopsPath)
		if err != nil {
			return plan.Plan{}, fmt.Errorf("cadre %s: %w", c.Name, err)
		}
		// Which decrypted keys become podman secrets: all of them, or exactly the allowlist.
		forCreate := secretValues
		if create := c.Manifest.Secrets.Create; len(create) > 0 {
			forCreate = map[string]string{}
			for _, k := range create {
				v, ok := secretValues[k]
				if !ok {
					return plan.Plan{}, fmt.Errorf("cadre %s: secrets.create lists %q, absent from %s", c.Name, k, c.SopsPath)
				}
				forCreate[k] = v
			}
		}
		secretHashes = secrets.Hashes(forCreate)
	}

	prior, err := state.Load(statePath(c.Name))
	if err != nil {
		return plan.Plan{}, err
	}
	p := plan.Compute(c, secretHashes, prior)

	// 1. resource limits
	if p.Resources != nil {
		if err := provision.ApplyResources(r, uid, *p.Resources); err != nil {
			return p, err
		}
	}
	// 2. stop removed units first, while their generated .service still resolves
	//    (once the .container file is deleted below, systemctl can no longer map it)
	for _, u := range p.StopUnits {
		o.Stop(u)
	}
	for _, u := range p.DisableUnits {
		o.DisableNow(u) // best-effort: stop + unlink while the unit file still exists
	}
	// 3. write/remove files as the cadre user. Quadlet units + support files go to the
	//    Quadlet dir; native systemd units go to the user unit dir (~/.config/systemd/user).
	qDir := systemdDir(c.Name)
	uDir := userUnitDir(c.Name)
	r.User(o.User, uid, []string{"mkdir", "-p", qDir}, nil)
	if writesSystemdUnit(p.WriteFiles) {
		r.User(o.User, uid, []string{"mkdir", "-p", uDir}, nil)
	}
	for _, f := range p.WriteFiles {
		dir := qDir
		if f.IsSystemdUnit {
			dir = uDir
		}
		if _, err := r.User(o.User, uid, []string{"tee", filepath.Join(dir, f.Name)}, f.Content); err != nil {
			return p, err
		}
	}
	for _, name := range p.RemoveFiles {
		dir := qDir
		if fileset.IsSystemdUnit(name) {
			dir = uDir
		}
		r.User(o.User, uid, []string{"rm", "-f", filepath.Join(dir, name)}, nil)
	}
	// 4. secrets
	for _, key := range p.CreateSecrets {
		if err := o.SecretCreate(key, []byte(secretValues[key])); err != nil {
			return p, err
		}
	}
	for _, key := range p.RemoveSecrets {
		o.SecretRemove(key)
	}
	// 5. registry logins
	for _, l := range c.Manifest.Registries.Login {
		pw, ok := secretValues[l.PasswordKey]
		if !ok {
			return p, fmt.Errorf("cadre %s: registry %s: passwordKey %q not in secrets", c.Name, l.Registry, l.PasswordKey)
		}
		if err := o.Login(l.Registry, l.Username, []byte(pw), l.Insecure); err != nil {
			return p, err
		}
	}
	// 6. daemon-reload + unit start/restart (stops already happened above)
	if p.DaemonReload {
		if err := o.DaemonReload(); err != nil {
			return p, err
		}
	}
	for _, u := range p.StartUnits {
		if err := o.Start(u); err != nil {
			return p, err
		}
	}
	for _, u := range p.RestartUnits {
		if err := o.Restart(u); err != nil {
			return p, err
		}
	}
	for _, u := range p.EnableUnits {
		if err := o.EnableNow(u); err != nil {
			return p, err
		}
	}
	for _, u := range p.RestartSystemdUnits {
		if err := o.RestartUnit(u); err != nil {
			return p, err
		}
	}

	// 7. persist new state
	next := nextState(c, uid, secretHashes)
	if err := state.Save(statePath(c.Name), next); err != nil {
		return p, err
	}
	return p, nil
}

func nextState(c cadre.Cadre, uid int, secretHashes map[string]string) state.State {
	s := state.State{
		Name:         c.Name,
		UID:          uid,
		Files:        map[string]string{},
		SecretHashes: secretHashes,
		Resources:    c.Manifest.Resources,
	}
	if s.SecretHashes == nil {
		s.SecretHashes = map[string]string{}
	}
	for _, f := range c.Files {
		s.Files[f.Name] = f.Hash
		switch {
		case f.IsUnit:
			s.Units = append(s.Units, f.Name)
		case f.IsSystemdUnit:
			s.SystemdUnits = append(s.SystemdUnits, f.Name)
		}
	}
	return s
}

// writesSystemdUnit reports whether any file to write is a native systemd unit, so the
// user unit dir is created only for cadres that ship one.
func writesSystemdUnit(files []cadre.File) bool {
	for _, f := range files {
		if f.IsSystemdUnit {
			return true
		}
	}
	return false
}
