package reconcile

import (
	"slices"
	"strings"
	"testing"

	"podman-essaim-compartment-manager/internal/compartment"
	"podman-essaim-compartment-manager/internal/fileset"
	"podman-essaim-compartment-manager/internal/host"
	"podman-essaim-compartment-manager/internal/manifest"
	"podman-essaim-compartment-manager/internal/provision"
	"podman-essaim-compartment-manager/internal/state"
)

func TestApplyFreshWritesFilesAndStarts(t *testing.T) {
	c := compartment.Compartment{Name: "web", Manifest: manifest.Manifest{Name: "web"}}
	body := "[Container]\nImage=nginx\n"
	c.Files = []compartment.File{{Name: "web.container", Content: []byte(body), Hash: fileset.Hash([]byte(body)), IsUnit: true}}

	f := &host.Fake{Responses: map[string]host.Result{
		"root:id -u pecm-web": {Stdout: "1234", Code: 0},
	}}
	t.Setenv("PECM_STATE_DIR", t.TempDir())

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

func TestNewGeneratesIdentityAndReturnsRecipient(t *testing.T) {
	idp := provision.HomeDir("web") + "/.config/podman-essaim-compartment-manager/age/identity.txt"
	recp := provision.HomeDir("web") + "/.config/podman-essaim-compartment-manager/age/recipient.txt"
	f := &host.Fake{Responses: map[string]host.Result{
		"root:id -u pecm-web":            {Stdout: "1500"},
		"root:cat /etc/subuid":           {},
		"root:cat /etc/subgid":           {},
		"user:1500:test -f " + idp:       {Code: 1}, // force generation
		"user:1500:age-keygen -o " + idp: {Stderr: "Public key: age1testrecipient\n"},
	}}

	rec, err := New(f, "web")
	if err != nil {
		t.Fatal(err)
	}
	if rec != "age1testrecipient" {
		t.Fatalf("recipient = %q, want age1testrecipient", rec)
	}

	var teed *host.Call
	for i := range f.Calls {
		c := &f.Calls[i]
		if len(c.Argv) == 2 && c.Argv[0] == "tee" && c.Argv[1] == recp {
			teed = c
		}
	}
	if teed == nil {
		t.Fatalf("no tee %s call recorded", recp)
	}
	if string(teed.Stdin) != "age1testrecipient\n" {
		t.Fatalf("tee stdin = %q, want %q", teed.Stdin, "age1testrecipient\n")
	}
}

func TestStatusReportsUnitStates(t *testing.T) {
	t.Setenv("PECM_STATE_DIR", t.TempDir())
	if err := state.Save(statePath("web"), state.State{Name: "web", UID: 1234, Units: []string{"web.container"}}); err != nil {
		t.Fatal(err)
	}

	f := &host.Fake{Responses: map[string]host.Result{
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

func TestListReturnsCompartmentsWithState(t *testing.T) {
	t.Setenv("PECM_STATE_DIR", t.TempDir())
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
	t.Setenv("PECM_STATE_DIR", t.TempDir())
	got, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("List = %v, want empty", got)
	}
}

func TestStatusEmptyWhenNoState(t *testing.T) {
	t.Setenv("PECM_STATE_DIR", t.TempDir())
	f := &host.Fake{Responses: map[string]host.Result{}}
	got, err := Status(f, "web")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("Status = %+v, want empty", got)
	}
}

func TestRemoveStopsUnitsAndFilesWithoutPurge(t *testing.T) {
	t.Setenv("PECM_STATE_DIR", t.TempDir())
	if err := state.Save(statePath("web"), state.State{Name: "web", UID: 1234, Units: []string{"web.container"}}); err != nil {
		t.Fatal(err)
	}

	f := &host.Fake{Responses: map[string]host.Result{}}
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
	t.Setenv("PECM_STATE_DIR", t.TempDir())
	if err := state.Save(statePath("web"), state.State{Name: "web", UID: 1234, Units: []string{"web.container"}}); err != nil {
		t.Fatal(err)
	}

	f := &host.Fake{Responses: map[string]host.Result{}}
	if err := Remove(f, "web", true); err != nil {
		t.Fatal(err)
	}

	var sawUserdel, sawRmSlice bool
	wantRmSlice := "rm -rf /etc/systemd/system/user-1234.slice.d"
	for _, c := range f.Calls {
		joined := strings.Join(c.Argv, " ")
		if c.Root && joined == "userdel -r pecm-web" {
			sawUserdel = true
		}
		if c.Root && joined == wantRmSlice {
			sawRmSlice = true
		}
	}
	if !sawUserdel {
		t.Errorf("expected a root `userdel -r pecm-web` call")
	}
	if !sawRmSlice {
		t.Errorf("expected a root `%s` call", wantRmSlice)
	}
}

func TestApplyHonorsSecretsCreateAllowlist(t *testing.T) {
	t.Setenv("PECM_STATE_DIR", t.TempDir())

	sopsPath := "/etc/pecm/web/secrets.sops.yaml"
	idp := IdentityPath("web")
	decrypted := `{"db_password":"pw1","ghcr_token":"tok"}`
	f := &host.Fake{Responses: map[string]host.Result{
		"root:id -u pecm-web": {Stdout: "1234", Code: 0},
		"root:env SOPS_AGE_KEY_FILE=" + idp + " sops -d --output-type json " + sopsPath: {Stdout: decrypted},
	}}

	c := compartment.Compartment{
		Name:     "web",
		SopsPath: sopsPath,
		Manifest: manifest.Manifest{
			Name:    "web",
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
	t.Setenv("PECM_STATE_DIR", t.TempDir())

	sopsPath := "/etc/pecm/web/secrets.sops.yaml"
	idp := IdentityPath("web")
	decrypted := `{"db_password":"pw1"}`
	f := &host.Fake{Responses: map[string]host.Result{
		"root:id -u pecm-web": {Stdout: "1234", Code: 0},
		"root:env SOPS_AGE_KEY_FILE=" + idp + " sops -d --output-type json " + sopsPath: {Stdout: decrypted},
	}}

	c := compartment.Compartment{
		Name:     "web",
		SopsPath: sopsPath,
		Manifest: manifest.Manifest{
			Name: "web",
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
	recp := provision.HomeDir("web") + "/.config/podman-essaim-compartment-manager/age/recipient.txt"
	f := &host.Fake{Responses: map[string]host.Result{
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
