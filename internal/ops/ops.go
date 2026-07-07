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

func (o Ops) SecretRemove(name string) error {
	// ignore "no such secret"; treat only real failures as errors
	o.R.User(o.User, o.UID, []string{"podman", "secret", "rm", name}, nil)
	return nil
}

func (o Ops) SecretCreate(name string, value []byte) error {
	_ = o.SecretRemove(name)
	argv := []string{"podman", "secret", "create", name, "-"}
	res, err := o.R.User(o.User, o.UID, argv, value)
	return wrap(res, err, argv)
}

func (o Ops) Login(reg, user string, password []byte, insecure bool) error {
	argv := []string{"podman", "login", "--username", user, "--password-stdin"}
	if insecure {
		argv = append(argv, "--tls-verify=false")
	}
	argv = append(argv, reg)
	res, err := o.R.User(o.User, o.UID, argv, password)
	return wrap(res, err, argv)
}

// GenerateAgeKey creates the cadre's age identity in-process and writes it to
// identityPath as the cadre user, returning the corresponding recipient. Writing
// as the user (mkdir/tee/chmod) mirrors agent.installIdentity; tee honors the user's
// umask, so the private key is tightened to 0600.
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
	tee := []string{"tee", identityPath}
	res, err = o.R.User(o.User, o.UID, tee, []byte(identity+"\n"))
	if err := wrap(res, err, tee); err != nil {
		return "", err
	}
	chmod := []string{"chmod", "600", identityPath}
	res, err = o.R.User(o.User, o.UID, chmod, nil)
	if err := wrap(res, err, chmod); err != nil {
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
	case "container":
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
