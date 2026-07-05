// Package ops wraps per-compartment systemd, podman, secret and age operations.
package ops

import (
	"fmt"
	"strings"

	"podman-essaim-compartment-manager/internal/host"
)

type Ops struct {
	R    host.Runner
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

func (o Ops) GenerateAgeKey(identityPath string) (string, error) {
	argv := []string{"age-keygen", "-o", identityPath}
	res, err := o.R.User(o.User, o.UID, argv, nil)
	if err := wrap(res, err, argv); err != nil {
		return "", err
	}
	// age-keygen prints "Public key: age1..." to stderr.
	for _, line := range strings.Split(res.Stderr, "\n") {
		if r, ok := strings.CutPrefix(strings.TrimSpace(line), "Public key: "); ok {
			return r, nil
		}
	}
	return "", fmt.Errorf("age-keygen: recipient not found in output")
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

func wrap(res host.Result, err error, argv []string) error {
	if err != nil {
		return fmt.Errorf("%s: %w", argv[0], err)
	}
	if res.Code != 0 {
		return fmt.Errorf("%s exited %d: %s", strings.Join(argv, " "), res.Code, res.Stderr)
	}
	return nil
}
