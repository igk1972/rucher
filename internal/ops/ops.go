// SPDX-License-Identifier: AGPL-3.0-or-later

// Package ops wraps per-cadre systemd, podman, secret and age operations.
package ops

import (
	"fmt"
	"path/filepath"
	"strings"

	"rucher/internal/age"
	"rucher/internal/node"
)

type Ops struct {
	R    node.Runner
	User string
	UID  int
}

func (o Ops) sc(args ...string) error {
	argv := append([]string{"systemctl", "--user"}, args...)
	res, err := o.R.User(o.User, o.UID, argv, nil)
	return wrap(res, err, argv)
}

func (o Ops) DaemonReload() error    { return o.sc("daemon-reload") }
func (o Ops) Start(u string) error   { return o.sc("start", UnitService(u)) }
func (o Ops) Restart(u string) error { return o.sc("restart", UnitService(u)) }
func (o Ops) Stop(u string) error    { return o.sc("stop", UnitService(u)) }

// Native systemd units (.timer/.socket/.path) are their own unit name — no Quadlet
// service mapping. EnableNow arms a new one (and persists it via the wants symlink so
// it survives reboot under linger); RestartUnit re-reads a changed one; DisableNow
// stops and unlinks a removed one.
func (o Ops) EnableNow(u string) error   { return o.sc("enable", "--now", u) }
func (o Ops) RestartUnit(u string) error { return o.sc("restart", u) }
func (o Ops) DisableNow(u string) error  { return o.sc("disable", "--now", u) }

// StopAllUserServices gracefully stops every loaded service of the user's manager —
// wider than the units tracked in state. argv is exec'd directly (no shell), so the
// '*.service' glob reaches systemctl intact and systemd expands it over loaded units.
func (o Ops) StopAllUserServices() error { return o.sc("stop", "*.service") }

// KillPause tears down the rootless pause process (and any running containers), which
// otherwise outlives the user manager and would block userdel. Best-effort.
func (o Ops) KillPause() {
	o.R.User(o.User, o.UID, []string{"podman", "system", "migrate"}, nil)
}

func (o Ops) SecretRemove(name string) error {
	// Removing an absent secret is fine (idempotent); any other non-zero exit is a real
	// failure a caller should see, so a rotated-away secret is not silently left behind.
	res, err := o.R.User(o.User, o.UID, []string{"podman", "secret", "rm", "--", name}, nil)
	if err != nil {
		return fmt.Errorf("secret rm %s: %w", name, err)
	}
	if res.Code != 0 && !strings.Contains(res.Stderr, "no such secret") && !strings.Contains(res.Stderr, "not found") {
		return fmt.Errorf("secret rm %s exited %d: %s", name, res.Code, strings.TrimSpace(res.Stderr))
	}
	return nil
}

func (o Ops) SecretCreate(name string, value []byte) error {
	_ = o.SecretRemove(name)
	// `--` ends options so a secret name beginning with '-' is not parsed as a flag; the
	// trailing '-' is then the FILE arg meaning "read the value from stdin".
	argv := []string{"podman", "secret", "create", "--", name, "-"}
	res, err := o.R.User(o.User, o.UID, argv, value)
	return wrap(res, err, argv)
}

func (o Ops) Login(reg, user string, password []byte, insecure bool) error {
	argv := []string{"podman", "login", "--username", user, "--password-stdin"}
	if insecure {
		argv = append(argv, "--tls-verify=false")
	}
	argv = append(argv, "--", reg)
	res, err := o.R.User(o.User, o.UID, argv, password)
	return wrap(res, err, argv)
}

// GenerateAgeKey creates the cadre's age identity in-process and writes it to
// identityPath as the cadre user, returning the corresponding recipient. The key is
// written with `install -m600` so it lands at 0600 atomically — never briefly at the
// user's umask (0644) as a tee+chmod pair would leave it. Mirrors agent.installIdentity.
func (o Ops) GenerateAgeKey(identityPath string) (string, error) {
	identity, recipient, err := age.GenerateIdentity()
	if err != nil {
		return "", err
	}

	mkdir := []string{"mkdir", "-p", filepath.Dir(identityPath)}
	res, err := o.R.User(o.User, o.UID, mkdir, nil)
	if err := wrap(res, err, mkdir); err != nil {
		return "", err
	}
	inst := []string{"install", "-m", "600", "/dev/stdin", identityPath}
	res, err = o.R.User(o.User, o.UID, inst, []byte(identity+"\n"))
	if err := wrap(res, err, inst); err != nil {
		return "", err
	}
	return recipient, nil
}

// UnitService maps a Quadlet unit filename to its generated .service name.
func UnitService(unit string) string {
	dot := strings.LastIndex(unit, ".")
	if dot < 0 {
		return unit // already a bare/service-style name; nothing to map
	}
	stem, ext := unit[:dot], unit[dot+1:]
	switch ext {
	case "container", "kube":
		// Quadlet names a .container's and a .kube's service after the bare stem
		// (foo.service); .volume/.network/.pod/.image/.build get the -<ext> suffix.
		return stem + ".service"
	default:
		return stem + "-" + ext + ".service"
	}
}

func wrap(res node.Result, err error, argv []string) error {
	if err != nil {
		return fmt.Errorf("%s: %w", argv[0], err)
	}
	if res.Code != 0 {
		return fmt.Errorf("%s exited %d: %s", strings.Join(argv, " "), res.Code, res.Stderr)
	}
	return nil
}
