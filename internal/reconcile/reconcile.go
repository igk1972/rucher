// Package reconcile applies a compartment's desired state to the host.
package reconcile

import (
	"fmt"
	"path/filepath"
	"strings"

	"podman-essaim-compartment-manager/internal/compartment"
	"podman-essaim-compartment-manager/internal/host"
	"podman-essaim-compartment-manager/internal/ops"
	"podman-essaim-compartment-manager/internal/plan"
	"podman-essaim-compartment-manager/internal/provision"
	"podman-essaim-compartment-manager/internal/secrets"
	"podman-essaim-compartment-manager/internal/state"
)

// baseDirForState is a var so tests can redirect state to a temp dir.
var baseDirForState = provision.BaseDir

func statePath(name string) string {
	return filepath.Join(baseDirForState, "state", name+".json")
}

func systemdDir(name string) string {
	return provision.HomeDir(name) + "/.config/containers/systemd"
}

func ageDir(name string) string {
	return provision.HomeDir(name) + "/.config/podman-essaim-compartment-manager/age"
}

func identityPath(name string) string  { return ageDir(name) + "/identity.txt" }
func recipientPath(name string) string { return ageDir(name) + "/recipient.txt" }

// New ensures the compartment's OS user and age identity exist and returns its age recipient.
func New(r host.Runner, name string) (string, error) {
	uid, err := provision.EnsureUser(r, name)
	if err != nil {
		return "", err
	}
	user := provision.UserName(name)
	if _, err := r.User(user, uid, []string{"mkdir", "-p", ageDir(name)}, nil); err != nil {
		return "", err
	}
	idp := identityPath(name)
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

// Recipient returns the compartment's stored age recipient (root reads the user's file).
func Recipient(r host.Runner, name string) (string, error) {
	res, err := r.Root([]string{"cat", recipientPath(name)}, nil)
	if err != nil {
		return "", err
	}
	if res.Code != 0 {
		return "", fmt.Errorf("recipient for %s: %s", name, res.Stderr)
	}
	return strings.TrimSpace(res.Stdout), nil
}

// Remove stops a compartment's units; with purge it also deletes the OS user and its home.
func Remove(r host.Runner, name string, purge bool) error {
	if purge {
		if _, err := r.Root([]string{"loginctl", "disable-linger", provision.UserName(name)}, nil); err != nil {
			return err
		}
		if _, err := r.Root([]string{"userdel", "-r", provision.UserName(name)}, nil); err != nil {
			return err
		}
	}
	return nil
}

func Apply(r host.Runner, c compartment.Compartment) (plan.Plan, error) {
	uid, err := provision.EnsureUser(r, c.Name)
	if err != nil {
		return plan.Plan{}, err
	}
	o := ops.Ops{R: r, User: provision.UserName(c.Name), UID: uid}

	var secretHashes map[string]string
	var secretValues map[string]string
	if c.SopsPath != "" {
		secretValues, err = secrets.Decrypt(r, o.User, uid, identityPath(c.Name), c.SopsPath)
		if err != nil {
			return plan.Plan{}, fmt.Errorf("compartment %s: %w", c.Name, err)
		}
		secretHashes = secrets.Hashes(secretValues)
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
	// 3. write/remove files (as the compartment user, into the systemd dir)
	dir := systemdDir(c.Name)
	r.User(o.User, uid, []string{"mkdir", "-p", dir}, nil)
	for _, f := range p.WriteFiles {
		if _, err := r.User(o.User, uid, []string{"tee", filepath.Join(dir, f.Name)}, f.Content); err != nil {
			return p, err
		}
	}
	for _, name := range p.RemoveFiles {
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
		if pw, ok := secretValues[l.PasswordKey]; ok {
			if err := o.Login(l.Registry, l.Username, []byte(pw), l.Insecure); err != nil {
				return p, err
			}
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

	// 7. persist new state
	next := nextState(c, uid, secretHashes)
	if err := state.Save(statePath(c.Name), next); err != nil {
		return p, err
	}
	return p, nil
}

func nextState(c compartment.Compartment, uid int, secretHashes map[string]string) state.State {
	s := state.State{
		Name:         c.Name,
		UID:          uid,
		Files:        map[string]string{},
		SecretHashes: secretHashes,
		Resources:    c.Manifest.Resources,
		Logins:       map[string]string{},
	}
	if s.SecretHashes == nil {
		s.SecretHashes = map[string]string{}
	}
	for _, f := range c.Files {
		s.Files[f.Name] = f.Hash
		if f.IsUnit {
			s.Units = append(s.Units, f.Name)
		}
	}
	return s
}
