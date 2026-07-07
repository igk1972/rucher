//go:build integration

package integration

import (
	"strings"
	"testing"
	"time"
)

// A long-lived container unit: alpine is cached on the nodes and `sleep infinity`
// stays active without flapping, so it is a stable target for exec/restart checks.
func alpineUnit(extra string) string {
	return "[Container]\nImage=docker.io/library/alpine:latest\nExec=sleep infinity\n" +
		extra + "[Install]\nWantedBy=default.target\n"
}

// cadreUser runs argv inside a cadre user's systemd/podman session (root -> runuser).
func cadreUser(t *testing.T, node, name string, argv ...string) result {
	t.Helper()
	uid := nodeSudo(t, node, "id", "-u", "rucher-"+name).out()
	full := append([]string{
		"runuser", "-u", "rucher-" + name, "--",
		"env", "XDG_RUNTIME_DIR=/run/user/" + uid,
		"DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/" + uid + "/bus",
	}, argv...)
	return nodeSudo(t, node, full...)
}

// T2.2 — a podman secret is delivered into the running container as the target
// env var (the full SOPS -> podman secret -> container-env path).
func TestSecretReachesContainerEnv(t *testing.T) {
	requireNodes(t, node1)
	const name = "itenv"
	const value = "s3cr3t_env_value"
	t.Cleanup(func() { cleanupCadre(t, name, node1) })
	cleanupCadre(t, name, node1)

	rec := rucherNode(t, node1, "node", "cadre", "new", name)
	if rec.code != 0 {
		t.Fatalf("new: %s", rec.stderr)
	}
	parent := newCadre(t, name, map[string]string{
		"rucher.yml":    "secrets:\n  from: secrets.sops.yaml\n",
		"app.container": alpineUnit("Secret=db_password,type=env,target=DB_PASSWORD\n"),
	})
	sopsEncrypt(t, rec.out(), "db_password: "+value+"\n", parent+"/"+name+"/secrets.sops.yaml")
	if r := nodeApply(t, node1, parent, name); r.code != 0 {
		t.Fatalf("apply: code=%d err=%q", r.code, r.stderr)
	}

	// The container is `systemd-<unit-stem>`; read the env var from inside it.
	var got string
	for i := 0; i < 8; i++ {
		r := cadreUser(t, node1, name, "podman", "exec", "systemd-app", "printenv", "DB_PASSWORD")
		if r.code == 0 {
			got = strings.TrimSpace(r.stdout)
			break
		}
		time.Sleep(1 * time.Second)
	}
	if got != value {
		t.Fatalf("DB_PASSWORD in container = %q, want %q", got, value)
	}
}

// T2.3 — editing a support file restarts only the units that reference it: the
// unit with EnvironmentFile=app.env restarts, the unrelated unit does not.
func TestSelectiveRestartOnSupportFileChange(t *testing.T) {
	requireNodes(t, node1)
	const name = "itdrift"
	t.Cleanup(func() { cleanupCadre(t, name, node1) })
	cleanupCadre(t, name, node1)

	files := map[string]string{
		"rucher.yml":      "{}\n",
		"web.container":   alpineUnit("EnvironmentFile=%h/.config/containers/systemd/app.env\n"),
		"other.container": alpineUnit(""),
		"app.env":         "A=1\n",
	}
	parent := newCadre(t, name, files)
	if r := nodeApply(t, node1, parent, name); r.code != 0 {
		t.Fatalf("apply: code=%d err=%q", r.code, r.stderr)
	}

	invID := func(svc string) string {
		return strings.TrimSpace(cadreUser(t, node1, name, "systemctl", "--user", "show", svc, "-p", "InvocationID", "--value").stdout)
	}
	webBefore, otherBefore := invID("web.service"), invID("other.service")
	if webBefore == "" || otherBefore == "" {
		t.Fatalf("units not running: web=%q other=%q", webBefore, otherBefore)
	}

	// Change only app.env, which only web references.
	files["app.env"] = "A=2\n"
	parent2 := newCadre(t, name, files)
	r := nodeApply(t, node1, parent2, name)
	if r.code != 0 {
		t.Fatalf("second apply: %s", r.stderr)
	}
	if !strings.Contains(r.stdout, "restarted=1") {
		t.Fatalf("expected exactly one restart, got: %q", r.stdout)
	}

	webAfter, otherAfter := invID("web.service"), invID("other.service")
	if webAfter == webBefore {
		t.Fatalf("web should have restarted (InvocationID unchanged: %s)", webAfter)
	}
	if otherAfter != otherBefore {
		t.Fatalf("other should NOT have restarted (InvocationID changed %s -> %s)", otherBefore, otherAfter)
	}
}
