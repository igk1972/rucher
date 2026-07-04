// Package provision ensures the OS user and resource limits for a compartment.
package provision

import (
	"fmt"
	"strconv"
	"strings"

	"podman-essaim-compartment-manager/internal/host"
	"podman-essaim-compartment-manager/internal/manifest"
)

const BaseDir = "/var/lib/podman-essaim-compartment-manager"

func UserName(name string) string { return "pecm-" + name }
func HomeDir(name string) string  { return BaseDir + "/" + name }

func EnsureUser(r host.Runner, name string) (int, error) {
	user := UserName(name)
	if res, _ := r.Root([]string{"id", "-u", user}, nil); res.Code != 0 {
		home := HomeDir(name)
		if res, err := r.Root([]string{
			"useradd", "--system", "--create-home", "--home-dir", home,
			"--shell", "/usr/sbin/nologin", user,
		}, nil); err != nil || res.Code != 0 {
			return 0, fmt.Errorf("useradd %s: code=%d stderr=%s err=%v", user, res.Code, res.Stderr, err)
		}
	}
	// linger keeps /run/user/<uid> and the user systemd manager alive across logins.
	if _, err := r.Root([]string{"loginctl", "enable-linger", user}, nil); err != nil {
		return 0, err
	}
	// subuid/subgid are idempotent via usermod's add-subuids/add-subgids.
	for _, sub := range []string{"--add-subuids", "--add-subgids"} {
		if _, err := r.Root([]string{"usermod", sub, "100000-165535", user}, nil); err != nil {
			return 0, err
		}
	}
	res, err := r.Root([]string{"id", "-u", user}, nil)
	if err != nil {
		return 0, err
	}
	uid, err := strconv.Atoi(strings.TrimSpace(res.Stdout))
	if err != nil {
		return 0, fmt.Errorf("parse uid for %s: %q", user, res.Stdout)
	}
	return uid, nil
}

func ApplyResources(r host.Runner, uid int, res manifest.Resources) error {
	dir := fmt.Sprintf("/etc/systemd/system/user-%d.slice.d", uid)
	if _, err := r.Root([]string{"mkdir", "-p", dir}, nil); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("[Slice]\n")
	if res.MemoryMax != "" {
		fmt.Fprintf(&b, "MemoryMax=%s\n", res.MemoryMax)
	}
	if res.CPUQuota != "" {
		fmt.Fprintf(&b, "CPUQuota=%s\n", res.CPUQuota)
	}
	conf := dir + "/50-podman-essaim-compartment-manager.conf"
	// tee reads the drop-in body from stdin (never argv).
	if _, err := r.Root([]string{"tee", conf}, []byte(b.String())); err != nil {
		return err
	}
	_, err := r.Root([]string{"systemctl", "daemon-reload"}, nil)
	return err
}
