// SPDX-License-Identifier: AGPL-3.0-or-later

package reconcile

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"rucher/internal/age"
	"rucher/internal/cadre"
	"rucher/internal/fileset"
	"rucher/internal/manifest"
	"rucher/internal/node"
	"rucher/internal/provision"
	"rucher/internal/sopsage"
	"rucher/internal/state"
)

// writeCadreSecrets provisions a real age identity for the cadre under the
// (test-overridden) base dir and writes a SOPS+age file with the given values,
// so the in-process secrets.Decrypt runs for real. Returns the sops file path.
// Set RUCHER_CADRES_DIR before calling so IdentityPath lands in a temp dir.
func writeCadreSecrets(t *testing.T, name string, kv []sopsage.KV) string {
	t.Helper()
	id, rec, err := age.GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	idp := IdentityPath(name)
	if err := os.MkdirAll(filepath.Dir(idp), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(idp, []byte(id+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	enc, err := sopsage.Encrypt([]string{rec}, kv, "2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	sopsPath := filepath.Join(t.TempDir(), "secrets.sops.yaml")
	if err := os.WriteFile(sopsPath, enc, 0o600); err != nil {
		t.Fatal(err)
	}
	return sopsPath
}

func TestApplyFreshWritesFilesAndStarts(t *testing.T) {
	c := cadre.Cadre{Name: "web"}
	body := "[Container]\nImage=nginx\n"
	c.Files = []cadre.File{{Name: "web.container", Content: []byte(body), Hash: fileset.Hash([]byte(body)), IsUnit: true}}

	f := &node.Fake{Responses: map[string]node.Result{
		"root:id -u rucher-web": {Stdout: "1234", Code: 0},
	}}
	t.Setenv("RUCHER_STATE_DIR", t.TempDir())

	p, err := Apply(f, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.StartUnits) != 1 || p.StartUnits[0] != "web.container" {
		t.Fatalf("StartUnits = %v", p.StartUnits)
	}
	var all strings.Builder
	for _, call := range f.Calls {
		all.WriteString(strings.Join(call.Argv, " ") + "\n")
	}
	if !strings.Contains(all.String(), "daemon-reload") {
		t.Fatalf("expected daemon-reload:\n%s", all.String())
	}
}

func TestApplyRoutesSystemdUnitToUserDirAndEnables(t *testing.T) {
	t.Setenv("RUCHER_CADRES_DIR", t.TempDir())
	t.Setenv("RUCHER_STATE_DIR", t.TempDir())

	container := "[Container]\nImage=busybox\n"
	timer := "[Timer]\nOnCalendar=daily\n[Install]\nWantedBy=timers.target\n"
	c := cadre.Cadre{Name: "web"}
	c.Files = []cadre.File{
		{Name: "backup.container", Content: []byte(container), Hash: fileset.Hash([]byte(container)), IsUnit: true},
		{Name: "backup.timer", Content: []byte(timer), Hash: fileset.Hash([]byte(timer)), IsSystemdUnit: true},
	}

	f := &node.Fake{Responses: map[string]node.Result{
		"root:id -u rucher-web": {Stdout: "1234", Code: 0},
	}}
	if _, err := Apply(f, c); err != nil {
		t.Fatal(err)
	}

	wantTimerTee := "tee " + userUnitDir("web") + "/backup.timer"        // -> user unit dir
	wantContainerTee := "tee " + systemdDir("web") + "/backup.container" // -> Quadlet dir
	wantEnable := "systemctl --user enable --now backup.timer"
	var sawTimerTee, sawContainerTee, sawEnable bool
	for _, call := range f.Calls {
		switch strings.Join(call.Argv, " ") {
		case wantTimerTee:
			sawTimerTee = true
			if string(call.Stdin) != timer {
				t.Fatalf("timer body via stdin = %q, want %q", call.Stdin, timer)
			}
		case wantContainerTee:
			sawContainerTee = true
		case wantEnable:
			sawEnable = true
		}
	}
	if !sawTimerTee {
		t.Errorf("timer must be written to the user unit dir (%s)", wantTimerTee)
	}
	if !sawContainerTee {
		t.Errorf("container must stay in the Quadlet dir (%s)", wantContainerTee)
	}
	if !sawEnable {
		t.Error("timer must be enabled with `systemctl --user enable --now`")
	}
}

func TestNewGeneratesIdentityAndReturnsRecipient(t *testing.T) {
	idp := provision.HomeDir("web") + "/.config/rucher/age/identity.txt"
	recp := provision.HomeDir("web") + "/.config/rucher/age/recipient.txt"
	f := &node.Fake{Responses: map[string]node.Result{
		"root:id -u rucher-web":    {Stdout: "1500"},
		"root:cat /etc/subuid":     {},
		"root:cat /etc/subgid":     {},
		"user:1500:test -f " + idp: {Code: 1}, // force generation
	}}

	rec, err := New(f, "web")
	if err != nil {
		t.Fatal(err)
	}
	// The identity is generated in-process, so the recipient is a real random age1 key.
	if !strings.HasPrefix(rec, "age1") {
		t.Fatalf("recipient = %q, want a valid age1 recipient", rec)
	}

	var idCall, teed *node.Call
	for i := range f.Calls {
		c := &f.Calls[i]
		if len(c.Argv) == 2 && c.Argv[0] == "tee" && c.Argv[1] == idp {
			idCall = c
		}
		if len(c.Argv) == 2 && c.Argv[0] == "tee" && c.Argv[1] == recp {
			teed = c
		}
	}
	// The identity written to disk must back-derive to the returned recipient.
	if idCall == nil {
		t.Fatalf("no tee %s call recorded", idp)
	}
	back, err := age.RecipientFor(strings.TrimSpace(string(idCall.Stdin)))
	if err != nil {
		t.Fatal(err)
	}
	if back != rec {
		t.Fatalf("recipient from written identity = %q, want returned %q", back, rec)
	}
	if teed == nil {
		t.Fatalf("no tee %s call recorded", recp)
	}
	if string(teed.Stdin) != rec+"\n" {
		t.Fatalf("tee stdin = %q, want %q", teed.Stdin, rec+"\n")
	}
}

func TestStatusReportsUnitStates(t *testing.T) {
	t.Setenv("RUCHER_STATE_DIR", t.TempDir())
	if err := state.Save(statePath("web"), state.State{Name: "web", UID: 1234, Units: []string{"web.container"}}); err != nil {
		t.Fatal(err)
	}

	f := &node.Fake{Responses: map[string]node.Result{
		"user:1234:systemctl --user show web.service -p ActiveState -p SubState --value": {Stdout: "active\nrunning\n"},
	}}
	got, err := Status(f, "web")
	if err != nil {
		t.Fatal(err)
	}
	want := []UnitStatus{{Unit: "web.container", Active: "active", Sub: "running"}}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("Status = %+v, want %+v", got, want)
	}
}

func TestListReturnsCadresWithState(t *testing.T) {
	t.Setenv("RUCHER_STATE_DIR", t.TempDir())
	if err := state.Save(statePath("web"), state.State{Name: "web"}); err != nil {
		t.Fatal(err)
	}
	if err := state.Save(statePath("api"), state.State{Name: "api"}); err != nil {
		t.Fatal(err)
	}
	got, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !slices.Contains(got, "web") || !slices.Contains(got, "api") {
		t.Fatalf("List = %v, want web and api", got)
	}
}

func TestListEmptyWhenNoStateDir(t *testing.T) {
	t.Setenv("RUCHER_STATE_DIR", t.TempDir())
	got, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("List = %v, want empty", got)
	}
}

func TestStatusEmptyWhenNoState(t *testing.T) {
	t.Setenv("RUCHER_STATE_DIR", t.TempDir())
	f := &node.Fake{Responses: map[string]node.Result{}}
	got, err := Status(f, "web")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("Status = %+v, want empty", got)
	}
}

func TestRemoveStopsUnitsAndFilesWithoutPurge(t *testing.T) {
	t.Setenv("RUCHER_STATE_DIR", t.TempDir())
	if err := state.Save(statePath("web"), state.State{Name: "web", UID: 1234, Units: []string{"web.container"}}); err != nil {
		t.Fatal(err)
	}

	f := &node.Fake{Responses: map[string]node.Result{}}
	if err := Remove(f, "web", false); err != nil {
		t.Fatal(err)
	}

	var sawStop, sawRmDir, sawUserdel bool
	wantRmDir := "rm -rf " + systemdDir("web")
	for _, c := range f.Calls {
		joined := strings.Join(c.Argv, " ")
		switch {
		case !c.Root && joined == "systemctl --user stop web.service":
			sawStop = true
		case !c.Root && joined == wantRmDir:
			sawRmDir = true
		case c.Argv[0] == "userdel":
			sawUserdel = true
		}
	}
	if !sawStop {
		t.Errorf("expected a `systemctl --user stop web.service` user call")
	}
	if !sawRmDir {
		t.Errorf("expected a `%s` user call", wantRmDir)
	}
	if sawUserdel {
		t.Errorf("userdel must not run without --purge")
	}
}

func TestRemovePurgeDeletesUser(t *testing.T) {
	t.Setenv("RUCHER_STATE_DIR", t.TempDir())
	if err := state.Save(statePath("web"), state.State{Name: "web", UID: 1234, Units: []string{"web.container"}}); err != nil {
		t.Fatal(err)
	}

	f := &node.Fake{Responses: map[string]node.Result{}}
	if err := Remove(f, "web", true); err != nil {
		t.Fatal(err)
	}

	var sawUserdel, sawRmSlice bool
	wantRmSlice := "rm -rf /etc/systemd/system/user-1234.slice.d"
	for _, c := range f.Calls {
		joined := strings.Join(c.Argv, " ")
		if c.Root && joined == "userdel -r rucher-web" {
			sawUserdel = true
		}
		if c.Root && joined == wantRmSlice {
			sawRmSlice = true
		}
	}
	if !sawUserdel {
		t.Errorf("expected a root `userdel -r rucher-web` call")
	}
	if !sawRmSlice {
		t.Errorf("expected a root `%s` call", wantRmSlice)
	}
}

// TestRemovePurgeGracefulTeardown checks that --purge stops the user's services and its
// rootless pause process gracefully before the last-resort SIGKILL and userdel.
func TestRemovePurgeGracefulTeardown(t *testing.T) {
	t.Setenv("RUCHER_STATE_DIR", t.TempDir())
	if err := state.Save(statePath("web"), state.State{Name: "web", UID: 1234, Units: []string{"web.container"}}); err != nil {
		t.Fatal(err)
	}
	f := &node.Fake{Responses: map[string]node.Result{}}
	if err := Remove(f, "web", true); err != nil {
		t.Fatal(err)
	}
	idx := func(want string) int {
		for i, c := range f.Calls {
			if strings.Join(c.Argv, " ") == want {
				return i
			}
		}
		return -1
	}
	disableLinger := idx("loginctl disable-linger rucher-web")
	stopAll := idx("systemctl --user stop *.service")
	migrate := idx("podman system migrate")
	stopMgr := idx("systemctl stop user@1234.service")
	kill := idx("pkill -KILL -u rucher-web")
	userdel := idx("userdel -r rucher-web")

	for name, i := range map[string]int{
		"disable-linger": disableLinger, "stop *.service": stopAll, "pause migrate": migrate,
		"stop user@": stopMgr, "pkill -KILL": kill, "userdel": userdel,
	} {
		if i < 0 {
			t.Fatalf("teardown missing %q call", name)
		}
	}
	// Graceful stops precede the last-resort SIGKILL, which precedes userdel.
	if !(disableLinger < stopAll && stopAll < stopMgr && stopMgr < kill && kill < userdel) {
		t.Errorf("teardown order wrong: disable-linger=%d stop*=%d stopMgr=%d kill=%d userdel=%d",
			disableLinger, stopAll, stopMgr, kill, userdel)
	}
	if migrate > kill {
		t.Errorf("pause migrate (%d) must precede SIGKILL (%d)", migrate, kill)
	}
}

func TestApplyHonorsSecretsCreateAllowlist(t *testing.T) {
	t.Setenv("RUCHER_CADRES_DIR", t.TempDir())
	t.Setenv("RUCHER_STATE_DIR", t.TempDir())

	sopsPath := writeCadreSecrets(t, "web", []sopsage.KV{
		{Key: "db_password", Value: "pw1"}, {Key: "ghcr_token", Value: "tok"},
	})
	f := &node.Fake{Responses: map[string]node.Result{
		"root:id -u rucher-web": {Stdout: "1234", Code: 0},
	}}

	c := cadre.Cadre{
		Name:     "web",
		SopsPath: sopsPath,
		Manifest: manifest.Manifest{
			Secrets: manifest.Secrets{Create: []string{"db_password"}},
		},
	}

	if _, err := Apply(f, c); err != nil {
		t.Fatal(err)
	}

	var sawDBCreate, sawGhcrCreate bool
	for _, call := range f.Calls {
		switch strings.Join(call.Argv, " ") {
		case "podman secret create db_password -":
			sawDBCreate = true
		case "podman secret create ghcr_token -":
			sawGhcrCreate = true
		}
	}
	if !sawDBCreate {
		t.Errorf("expected a `podman secret create db_password -` user call")
	}
	if sawGhcrCreate {
		t.Errorf("ghcr_token must not become a podman secret (not in secrets.create)")
	}
}

func TestApplyErrorsOnMissingLoginPasswordKey(t *testing.T) {
	t.Setenv("RUCHER_CADRES_DIR", t.TempDir())
	t.Setenv("RUCHER_STATE_DIR", t.TempDir())

	sopsPath := writeCadreSecrets(t, "web", []sopsage.KV{{Key: "db_password", Value: "pw1"}})
	f := &node.Fake{Responses: map[string]node.Result{
		"root:id -u rucher-web": {Stdout: "1234", Code: 0},
	}}

	c := cadre.Cadre{
		Name:     "web",
		SopsPath: sopsPath,
		Manifest: manifest.Manifest{
			Registries: manifest.Registries{Login: []manifest.Login{
				{Registry: "ghcr.io", Username: "u", PasswordKey: "ghcr_token"},
			}},
		},
	}

	_, err := Apply(f, c)
	if err == nil {
		t.Fatal("expected error for missing login passwordKey")
	}
	if !strings.Contains(err.Error(), "ghcr_token") {
		t.Fatalf("error = %v, want mention of ghcr_token", err)
	}
}

func TestRecipientReadsFile(t *testing.T) {
	recp := provision.HomeDir("web") + "/.config/rucher/age/recipient.txt"
	f := &node.Fake{Responses: map[string]node.Result{
		"root:cat " + recp: {Stdout: "age1abc\n"},
	}}
	rec, err := Recipient(f, "web")
	if err != nil {
		t.Fatal(err)
	}
	if rec != "age1abc" {
		t.Fatalf("recipient = %q, want age1abc", rec)
	}
}
