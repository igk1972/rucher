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

func TestApplyFailsWhenFileWriteExitsNonZero(t *testing.T) {
	t.Setenv("RUCHER_STATE_DIR", t.TempDir())

	body := "[Container]\nImage=nginx\n"
	c := cadre.Cadre{Name: "web"}
	c.Files = []cadre.File{{Name: "web.container", Content: []byte(body), Hash: fileset.Hash([]byte(body)), IsUnit: true}}

	// tee exits non-zero (e.g. disk quota). The runner reports this via Code, not err.
	f := &node.Fake{Responses: map[string]node.Result{
		"root:id -u rucher-web":                                 {Stdout: "1234", Code: 0},
		"user:1234:tee " + systemdDir("web") + "/web.container": {Code: 1, Stderr: "No space left on device"},
	}}

	if _, err := Apply(f, c); err == nil {
		t.Fatal("Apply must fail when a file write exits non-zero")
	}

	// State must NOT record the file: a failed write that is silently saved would be
	// invisible to every future diff. An absent state file (load returns empty) is correct.
	st, err := state.Load(statePath("web"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := st.Files["web.container"]; ok {
		t.Fatalf("state recorded web.container despite the failed write: %+v", st.Files)
	}
}

func TestApplyReappliesResourcesOnUidChange(t *testing.T) {
	t.Setenv("RUCHER_STATE_DIR", t.TempDir())
	// Different prior uid, identical limits: the plan's Resources gate stays quiet, but the
	// new uid must still get the drop-in — and the old uid's drop-in must be left untouched
	// (it may belong to another cadre that reused that uid).
	if err := state.Save(statePath("web"), state.State{
		Name: "web", UID: 999, Files: map[string]string{}, SecretHashes: map[string]string{},
		Resources: manifest.Resources{MemoryMax: "512M"},
	}); err != nil {
		t.Fatal(err)
	}
	c := cadre.Cadre{Name: "web", Manifest: manifest.Manifest{Resources: manifest.Resources{MemoryMax: "512M"}}}
	f := &node.Fake{Responses: map[string]node.Result{"root:id -u rucher-web": {Stdout: "1234"}}}
	if _, err := Apply(f, c); err != nil {
		t.Fatal(err)
	}

	var sawOldSliceRm, sawNewDropIn bool
	for _, call := range f.Calls {
		switch strings.Join(call.Argv, " ") {
		case "rm -rf /etc/systemd/system/user-999.slice.d":
			sawOldSliceRm = true
		case "tee /etc/systemd/system/user-1234.slice.d/50-rucher.conf":
			sawNewDropIn = true
		}
	}
	if sawOldSliceRm {
		t.Error("the previous uid's slice drop-in must NOT be removed: it may have been reused by another cadre")
	}
	if !sawNewDropIn {
		t.Error("resource limits must be re-applied to the new uid")
	}
}

func TestApplyReappliesSecretsAndFilesOnUidChange(t *testing.T) {
	t.Setenv("RUCHER_CADRES_DIR", t.TempDir())
	t.Setenv("RUCHER_STATE_DIR", t.TempDir())

	sopsPath := writeCadreSecrets(t, "web", []sopsage.KV{{Key: "db_password", Value: "pw1"}})
	body := "[Container]\nImage=nginx\n"
	c := cadre.Cadre{
		Name:     "web",
		SopsPath: sopsPath,
		Manifest: manifest.Manifest{Secrets: manifest.Secrets{Create: []string{"db_password"}}},
	}
	c.Files = []cadre.File{{Name: "web.container", Content: []byte(body), Hash: fileset.Hash([]byte(body)), IsUnit: true}}

	// Prior state is fully converged but under a STALE uid: the user was recreated, so its home
	// (podman secrets under it, unit files) is gone. Everything must re-apply to the new uid.
	if err := state.Save(statePath("web"), state.State{
		Name: "web", UID: 999,
		Files:        map[string]string{"web.container": fileset.Hash([]byte(body))},
		SecretHashes: map[string]string{"db_password": fileset.Hash([]byte("pw1"))},
		Units:        []string{"web.container"},
	}); err != nil {
		t.Fatal(err)
	}

	f := &node.Fake{Responses: map[string]node.Result{"root:id -u rucher-web": {Stdout: "1234"}}}
	if _, err := Apply(f, c); err != nil {
		t.Fatal(err)
	}

	var sawSecretCreate, sawUnitTee bool
	for _, call := range f.Calls {
		switch strings.Join(call.Argv, " ") {
		case "podman secret create -- db_password -":
			sawSecretCreate = true
		case "tee " + systemdDir("web") + "/web.container":
			sawUnitTee = true
		}
	}
	if !sawSecretCreate {
		t.Error("a uid change must re-create the cadre's podman secrets for the fresh home")
	}
	if !sawUnitTee {
		t.Error("a uid change must re-write the cadre's unit files to the new home")
	}
}

func TestApplyRemovesDroppedUnitOnUidChange(t *testing.T) {
	t.Setenv("RUCHER_STATE_DIR", t.TempDir())
	body := "[Container]\nImage=nginx\n"
	// Prior (stale uid 999) has two units; desired drops old.container. The uid change forces a
	// re-apply against a zeroed baseline, but old.container must still be stopped and removed.
	if err := state.Save(statePath("web"), state.State{
		Name: "web", UID: 999,
		Files: map[string]string{
			"web.container": fileset.Hash([]byte(body)),
			"old.container": "oldhash",
		},
		Units:        []string{"web.container", "old.container"},
		SecretHashes: map[string]string{},
	}); err != nil {
		t.Fatal(err)
	}
	c := cadre.Cadre{Name: "web"}
	c.Files = []cadre.File{{Name: "web.container", Content: []byte(body), Hash: fileset.Hash([]byte(body)), IsUnit: true}}
	f := &node.Fake{Responses: map[string]node.Result{"root:id -u rucher-web": {Stdout: "1234"}}}
	p, err := Apply(f, c)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(p.StopUnits, "old.container") {
		t.Fatalf("StopUnits = %v, want old.container", p.StopUnits)
	}
	if !slices.Contains(p.RemoveFiles, "old.container") {
		t.Fatalf("RemoveFiles = %v, want old.container", p.RemoveFiles)
	}
	if !slices.Contains(p.StartUnits, "web.container") {
		t.Fatalf("StartUnits = %v, want web.container re-applied", p.StartUnits)
	}
}

func TestApplyKeepsUnitWhenStopFails(t *testing.T) {
	t.Setenv("RUCHER_STATE_DIR", t.TempDir())
	body := "[Container]\nImage=nginx\n"
	// Prior has two units; desired drops old.container, so it is stopped and removed.
	if err := state.Save(statePath("web"), state.State{
		Name: "web", UID: 1234,
		Files: map[string]string{
			"web.container": fileset.Hash([]byte(body)),
			"old.container": "oldhash",
		},
		Units:        []string{"web.container", "old.container"},
		SecretHashes: map[string]string{},
	}); err != nil {
		t.Fatal(err)
	}
	c := cadre.Cadre{Name: "web"}
	c.Files = []cadre.File{{Name: "web.container", Content: []byte(body), Hash: fileset.Hash([]byte(body)), IsUnit: true}}

	// Stopping old.container's generated service fails, while daemon-reload succeeds (live
	// manager). The failed stop must not delete the file nor drop the unit from state.
	f := &node.Fake{Responses: map[string]node.Result{
		"root:id -u rucher-web":                       {Stdout: "1234"},
		"user:1234:systemctl --user stop old.service": {Code: 1, Stderr: "job failed"},
	}}
	if _, err := Apply(f, c); err != nil {
		t.Fatal(err)
	}

	wantRm := "rm -f " + systemdDir("web") + "/old.container"
	for _, call := range f.Calls {
		if strings.Join(call.Argv, " ") == wantRm {
			t.Error("old.container must not be removed while its stop failed")
		}
	}
	st, err := state.Load(statePath("web"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(st.Units, "old.container") {
		t.Fatalf("state Units = %v, want old.container retained after a failed stop", st.Units)
	}
	if _, ok := st.Files["old.container"]; !ok {
		t.Fatalf("state Files = %v, want old.container retained after a failed stop", st.Files)
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

func TestApplySynthesizesPruneUnits(t *testing.T) {
	t.Setenv("RUCHER_STATE_DIR", t.TempDir())

	f := &node.Fake{Responses: map[string]node.Result{
		"root:id -u rucher-web": {Stdout: "1234", Code: 0},
	}}
	if _, err := Apply(f, cadre.Cadre{Name: "web"}); err != nil {
		t.Fatal(err)
	}

	var sawServiceTee, sawTimerTee, sawTimerEnable, sawServiceEnable bool
	for _, call := range f.Calls {
		switch strings.Join(call.Argv, " ") {
		case "tee " + userUnitDir("web") + "/" + fileset.PruneService:
			sawServiceTee = true
		case "tee " + userUnitDir("web") + "/" + fileset.PruneTimer:
			sawTimerTee = true
		case "systemctl --user enable --now " + fileset.PruneTimer:
			sawTimerEnable = true
		case "systemctl --user enable --now " + fileset.PruneService:
			sawServiceEnable = true
		}
	}
	if !sawServiceTee || !sawTimerTee {
		t.Errorf("both prune units must be written to the user unit dir (service=%v timer=%v)", sawServiceTee, sawTimerTee)
	}
	if !sawTimerEnable {
		t.Error("the prune timer must be enabled with `systemctl --user enable --now`")
	}
	if sawServiceEnable {
		t.Error("the [Install]-less prune service must never be enabled")
	}

	// Only the timer has a lifecycle; the service stays hash-tracked in Files.
	st, err := state.Load(statePath("web"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(st.SystemdUnits, []string{fileset.PruneTimer}) {
		t.Fatalf("state SystemdUnits = %v, want only the timer", st.SystemdUnits)
	}
	if _, ok := st.Files[fileset.PruneService]; !ok {
		t.Fatalf("state Files = %v, want the prune service hash-tracked", st.Files)
	}
}

func TestApplyPruneDisableRemovesUnits(t *testing.T) {
	t.Setenv("RUCHER_STATE_DIR", t.TempDir())
	responses := map[string]node.Result{
		"root:id -u rucher-web": {Stdout: "1234", Code: 0},
	}

	if _, err := Apply(&node.Fake{Responses: responses}, cadre.Cadre{Name: "web"}); err != nil {
		t.Fatal(err)
	}

	off := false
	c := cadre.Cadre{Name: "web", Manifest: manifest.Manifest{Prune: manifest.Prune{Enabled: &off}}}
	f := &node.Fake{Responses: responses}
	if _, err := Apply(f, c); err != nil {
		t.Fatal(err)
	}

	var sawDisable, sawServiceRm, sawTimerRm bool
	for _, call := range f.Calls {
		switch strings.Join(call.Argv, " ") {
		case "systemctl --user disable --now " + fileset.PruneTimer:
			sawDisable = true
		// both removals must target the user unit dir, not the Quadlet dir
		case "rm -f " + userUnitDir("web") + "/" + fileset.PruneService:
			sawServiceRm = true
		case "rm -f " + userUnitDir("web") + "/" + fileset.PruneTimer:
			sawTimerRm = true
		}
	}
	if !sawDisable {
		t.Error("disabling prune must `systemctl --user disable --now` the timer")
	}
	if !sawServiceRm || !sawTimerRm {
		t.Errorf("both prune units must be removed from the user unit dir (service=%v timer=%v)", sawServiceRm, sawTimerRm)
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
		// The private identity is written atomically at 0600 (install), the public
		// recipient (not secret) with a plain tee.
		if strings.Join(c.Argv, " ") == "install -m 600 /dev/stdin "+idp {
			idCall = c
		}
		if len(c.Argv) == 2 && c.Argv[0] == "tee" && c.Argv[1] == recp {
			teed = c
		}
	}
	// The identity written to disk must back-derive to the returned recipient.
	if idCall == nil {
		t.Fatalf("no `install -m 600 /dev/stdin %s` call recorded", idp)
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

func TestNewSelfHealsMissingRecipient(t *testing.T) {
	// Identity exists but recipient.txt does not (a New interrupted mid-way). New must
	// derive the recipient from the identity instead of failing forever.
	id, wantRec, err := age.GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	idp := IdentityPath("web")
	f := &node.Fake{Responses: map[string]node.Result{
		"root:id -u rucher-web":    {Stdout: "1500"},
		"user:1500:test -f " + idp: {Code: 0}, // identity present
		"root:cat /etc/subuid":     {},
		"root:cat /etc/subgid":     {},
		"root:cat " + idp:          {Stdout: id + "\n"},
	}}
	got, err := New(f, "web")
	if err != nil {
		t.Fatal(err)
	}
	if got != wantRec {
		t.Fatalf("recipient = %q, want %q derived from the identity", got, wantRec)
	}
	// recipient.txt must be rewritten to restore consistency.
	var rewrote bool
	for _, c := range f.Calls {
		if len(c.Argv) == 2 && c.Argv[0] == "tee" && c.Argv[1] == recipientPath("web") && string(c.Stdin) == wantRec+"\n" {
			rewrote = true
		}
	}
	if !rewrote {
		t.Fatal("recipient.txt must be rewritten from the derived recipient")
	}
}

func TestNewSurfacesRecipientWriteFailure(t *testing.T) {
	// The recipient.txt write must fail loudly on a non-zero exit (the runner reports it via
	// Code, not err), not be silently swallowed — otherwise Recipient() later can't read it.
	idp := IdentityPath("web")
	f := &node.Fake{Responses: map[string]node.Result{
		"root:id -u rucher-web":                 {Stdout: "1500"},
		"root:cat /etc/subuid":                  {},
		"root:cat /etc/subgid":                  {},
		"user:1500:test -f " + idp:              {Code: 1}, // force generation
		"user:1500:tee " + recipientPath("web"): {Code: 1, Stderr: "No space left on device"},
	}}
	if _, err := New(f, "web"); err == nil {
		t.Fatal("New must fail when the recipient.txt write exits non-zero")
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

func TestRemoveRefusesOnCorruptState(t *testing.T) {
	t.Setenv("RUCHER_STATE_DIR", t.TempDir())
	// A corrupted state file must abort Remove rather than silently skip teardown and
	// delete the file (which would orphan running workloads).
	sp := statePath("web")
	if err := os.MkdirAll(filepath.Dir(sp), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sp, []byte("{ not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := &node.Fake{Responses: map[string]node.Result{}}
	if err := Remove(f, "web", false); err == nil {
		t.Fatal("Remove must fail on a corrupted state file")
	}
	if _, err := os.Stat(sp); err != nil {
		t.Fatal("corrupted state file must be left in place, not deleted")
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

func TestApplySkipsRegistryLoginWhenConverged(t *testing.T) {
	t.Setenv("RUCHER_CADRES_DIR", t.TempDir())
	t.Setenv("RUCHER_STATE_DIR", t.TempDir())

	sopsPath := writeCadreSecrets(t, "web", []sopsage.KV{{Key: "ghcr_token", Value: "tok"}})
	c := cadre.Cadre{
		Name:     "web",
		SopsPath: sopsPath,
		Manifest: manifest.Manifest{Registries: manifest.Registries{Login: []manifest.Login{
			{Registry: "ghcr.io", Username: "u", PasswordKey: "ghcr_token"},
		}}},
	}
	sawLogin := func(f *node.Fake) bool {
		for _, call := range f.Calls {
			if len(call.Argv) >= 2 && call.Argv[0] == "podman" && call.Argv[1] == "login" {
				return true
			}
		}
		return false
	}

	// First apply: fresh cadre, plan is non-empty, so the registry login runs.
	f1 := &node.Fake{Responses: map[string]node.Result{"root:id -u rucher-web": {Stdout: "1234"}}}
	if _, err := Apply(f1, c); err != nil {
		t.Fatal(err)
	}
	if !sawLogin(f1) {
		t.Fatal("first apply must log in to the registry")
	}

	// Second apply: converged (empty plan), so login must be skipped to avoid a
	// per-pass registry round-trip / rate-limit.
	f2 := &node.Fake{Responses: map[string]node.Result{"root:id -u rucher-web": {Stdout: "1234"}}}
	if _, err := Apply(f2, c); err != nil {
		t.Fatal(err)
	}
	if sawLogin(f2) {
		t.Fatal("a converged apply must not re-run podman login")
	}
}

func TestApplyRelogsInWhenOnlyLoginBlockChanges(t *testing.T) {
	t.Setenv("RUCHER_CADRES_DIR", t.TempDir())
	t.Setenv("RUCHER_STATE_DIR", t.TempDir())

	sopsPath := writeCadreSecrets(t, "web", []sopsage.KV{{Key: "ghcr_token", Value: "tok"}})
	base := cadre.Cadre{
		Name:     "web",
		SopsPath: sopsPath,
		Manifest: manifest.Manifest{Registries: manifest.Registries{Login: []manifest.Login{
			{Registry: "ghcr.io", Username: "u", PasswordKey: "ghcr_token"},
		}}},
	}
	sawLogin := func(f *node.Fake) bool {
		for _, call := range f.Calls {
			if len(call.Argv) >= 2 && call.Argv[0] == "podman" && call.Argv[1] == "login" {
				return true
			}
		}
		return false
	}

	// Converge first so the plan is empty on the next apply.
	f1 := &node.Fake{Responses: map[string]node.Result{"root:id -u rucher-web": {Stdout: "1234"}}}
	if _, err := Apply(f1, base); err != nil {
		t.Fatal(err)
	}

	// Change only the username: no file or secret changed, so the plan is empty — but the
	// login block differs, so login must still re-run (a plain !p.Empty() gate would skip it).
	changed := base
	changed.Manifest.Registries.Login = []manifest.Login{
		{Registry: "ghcr.io", Username: "u2", PasswordKey: "ghcr_token"},
	}
	f2 := &node.Fake{Responses: map[string]node.Result{"root:id -u rucher-web": {Stdout: "1234"}}}
	if _, err := Apply(f2, changed); err != nil {
		t.Fatal(err)
	}
	if !sawLogin(f2) {
		t.Fatal("changing only the registry login block must re-run podman login")
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
		case "podman secret create -- db_password -":
			sawDBCreate = true
		case "podman secret create -- ghcr_token -":
			sawGhcrCreate = true
		}
	}
	if !sawDBCreate {
		t.Errorf("expected a `podman secret create -- db_password -` user call")
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
