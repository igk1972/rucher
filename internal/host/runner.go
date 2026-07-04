// Package host runs commands as root or as a compartment user (shell-out).
package host

import (
	"bytes"
	"fmt"
	"os/exec"
)

type Result struct {
	Stdout string
	Stderr string
	Code   int
}

type Runner interface {
	Root(argv []string, stdin []byte) (Result, error)
	User(user string, uid int, argv []string, stdin []byte) (Result, error)
}

type execRunner struct{}

func NewExec() Runner { return execRunner{} }

func (execRunner) Root(argv []string, stdin []byte) (Result, error) {
	return runExec(argv, stdin)
}

func (execRunner) User(user string, uid int, argv []string, stdin []byte) (Result, error) {
	full := append([]string{"runuser", "-u", user, "--"}, userEnvArgv(uid, argv)...)
	return runExec(full, stdin)
}

// userEnvArgv wraps argv so it runs inside the user's systemd/DBus session.
func userEnvArgv(uid int, argv []string) []string {
	env := []string{
		"env",
		fmt.Sprintf("XDG_RUNTIME_DIR=/run/user/%d", uid),
		fmt.Sprintf("DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/%d/bus", uid),
	}
	return append(env, argv...)
}

func runExec(argv []string, stdin []byte) (Result, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	res := Result{Stdout: out.String(), Stderr: errb.String()}
	if ee, ok := err.(*exec.ExitError); ok {
		res.Code = ee.ExitCode()
		return res, nil // non-zero exit is not a Go error for callers to inspect
	}
	return res, err
}
