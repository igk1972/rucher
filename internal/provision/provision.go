// SPDX-License-Identifier: AGPL-3.0-or-later

// Package provision ensures the OS user and resource limits for a cadre.
package provision

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"rucher/internal/manifest"
	"rucher/internal/node"
)

const (
	subidCount = 65536
	subidBase  = 100000
	// MaxCadreName keeps the "rucher-<name>" user within useradd's 32-char limit.
	MaxCadreName = 25
)

// cadreNameRe constrains a cadre name to what is safe as both a Linux username
// (rucher-<name>) and a filesystem path component — no slashes, dots, spaces or
// other traversal/injection characters.
var cadreNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// ValidName reports whether name is a safe cadre name.
func ValidName(name string) bool {
	return len(name) <= MaxCadreName && cadreNameRe.MatchString(name)
}

// BaseDir is the root of every cadre's home. RUCHER_CADRES_DIR overrides it
// (tests and alternative layouts); empty falls back to the system path.
func BaseDir() string {
	if d := os.Getenv("RUCHER_CADRES_DIR"); d != "" {
		return d
	}
	return "/var/lib/rucher/cadres"
}

func UserName(name string) string { return "rucher-" + name }
func HomeDir(name string) string  { return BaseDir() + "/" + name }

// nextSubidStart returns the next free subid start, scanning both /etc/subuid and
// /etc/subgid contents so the allocated block overlaps neither map.
func nextSubidStart(subuid, subgid string) int {
	max := subidBase
	for _, content := range []string{subuid, subgid} {
		for _, line := range strings.Split(content, "\n") {
			f := strings.Split(strings.TrimSpace(line), ":")
			if len(f) != 3 {
				continue
			}
			start, err1 := strconv.Atoi(f[1])
			count, err2 := strconv.Atoi(f[2])
			if err1 != nil || err2 != nil {
				continue
			}
			if end := start + count; end > max {
				max = end
			}
		}
	}
	return max
}

// hasSubid reports whether user already owns a subuid range (idempotency guard).
func hasSubid(subuid, user string) bool {
	for _, line := range strings.Split(subuid, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), user+":") {
			return true
		}
	}
	return false
}

func EnsureUser(r node.Runner, name string) (int, error) {
	// Validate here, not just at `ops init`: on the agent path the name comes from
	// placement.yml keys straight into a username and filesystem paths, so this is the
	// one choke point every caller passes through before useradd / HomeDir / statePath.
	if !ValidName(name) {
		return 0, fmt.Errorf("invalid cadre name %q (must match [a-z0-9][a-z0-9-]* and be at most %d chars)", name, MaxCadreName)
	}
	user := UserName(name)
	if res, _ := r.Root([]string{"id", "-u", user}, nil); res.Code != 0 {
		home := HomeDir(name)
		if res, err := r.Root([]string{
			"useradd", "--create-home", "--home-dir", home,
			"--shell", "/usr/sbin/nologin", user,
		}, nil); err != nil || res.Code != 0 {
			return 0, fmt.Errorf("useradd %s: code=%d stderr=%s err=%v", user, res.Code, res.Stderr, err)
		}
	}
	// linger keeps /run/user/<uid> and the user systemd manager alive across logins.
	if _, err := r.Root([]string{"loginctl", "enable-linger", user}, nil); err != nil {
		return 0, err
	}
	// Allocate a unique, non-overlapping subuid/subgid block per cadre user.
	subuidRes, err := r.Root([]string{"cat", "/etc/subuid"}, nil)
	if err != nil {
		return 0, err
	}
	if !hasSubid(subuidRes.Stdout, user) {
		subgidRes, err := r.Root([]string{"cat", "/etc/subgid"}, nil)
		if err != nil {
			return 0, err
		}
		start := nextSubidStart(subuidRes.Stdout, subgidRes.Stdout)
		rng := fmt.Sprintf("%d-%d", start, start+subidCount-1)
		if _, err := r.Root([]string{"usermod", "--add-subuids", rng, "--add-subgids", rng, user}, nil); err != nil {
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
	// The user's systemd manager may not be up yet right after linger is enabled;
	// start it and wait for its bus socket so the first `systemctl --user` call in
	// Apply does not race the manager's startup.
	if res, err := r.Root([]string{"sh", "-c", fmt.Sprintf(
		"systemctl start user@%d.service 2>/dev/null; "+
			"for i in $(seq 1 100); do [ -S /run/user/%d/bus ] && exit 0; sleep 0.1; done; exit 1",
		uid, uid)}, nil); err != nil || res.Code != 0 {
		return 0, fmt.Errorf("user manager for %s (uid %d) not ready: code=%d stderr=%s err=%v", user, uid, res.Code, res.Stderr, err)
	}
	if err := writeStorageConf(r, user, uid, HomeDir(name)); err != nil {
		return 0, err
	}
	return uid, nil
}

// writeStorageConf installs the cadre user's ~/.config/containers/storage.conf with
// rootless paths. Harmless for the distro podman (these are already its rootless
// defaults), but required for the prebuilt podman: its shipped
// /usr/share/containers/storage.conf pins rootful runroot/graphroot, which breaks
// rootless with "RunRoot is not writable". Idempotent (overwrites).
func writeStorageConf(r node.Runner, user string, uid int, home string) error {
	conf := fmt.Sprintf("[storage]\ndriver = \"overlay\"\nrunroot = \"/run/user/%d/containers\"\ngraphroot = \"%s/.local/share/containers/storage\"\n", uid, home)
	dir := home + "/.config/containers"
	if _, err := r.User(user, uid, []string{"mkdir", "-p", dir}, nil); err != nil {
		return err
	}
	if _, err := r.User(user, uid, []string{"tee", dir + "/storage.conf"}, []byte(conf)); err != nil {
		return err
	}
	return nil
}

func ApplyResources(r node.Runner, uid int, res manifest.Resources) error {
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
	conf := dir + "/50-rucher.conf"
	// tee reads the drop-in body from stdin (never argv).
	if _, err := r.Root([]string{"tee", conf}, []byte(b.String())); err != nil {
		return err
	}
	_, err := r.Root([]string{"systemctl", "daemon-reload"}, nil)
	return err
}
