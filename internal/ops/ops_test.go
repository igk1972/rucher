package ops

import (
	"strings"
	"testing"

	"podman-essaim-compartment-manager/internal/host"
)

func TestUnitService(t *testing.T) {
	cases := map[string]string{
		"web.container": "web.service",
		"data.volume":   "data-volume.service",
		"net.network":   "net-network.service",
		"app.pod":       "app-pod.service",
		"web":           "web", // no extension: returned unchanged, must not panic
	}
	for in, want := range cases {
		if got := UnitService(in); got != want {
			t.Fatalf("UnitService(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStartStopUseStartStopNotEnableDisable(t *testing.T) {
	// systemd refuses to enable/disable generator-produced (Quadlet) units, so
	// Start/Stop must issue plain start/stop; boot-persistence comes from the
	// unit's [Install] section + linger.
	f := &host.Fake{Responses: map[string]host.Result{}}
	o := Ops{R: f, User: "pecm-web", UID: 1234}

	if err := o.Start("web.container"); err != nil {
		t.Fatal(err)
	}
	if err := o.Stop("web.container"); err != nil {
		t.Fatal(err)
	}

	if len(f.Calls) != 2 {
		t.Fatalf("expected 2 calls, got %d: %+v", len(f.Calls), f.Calls)
	}
	wantStart := []string{"systemctl", "--user", "start", "web.service"}
	if !equalArgv(f.Calls[0].Argv, wantStart) {
		t.Fatalf("Start argv = %v, want %v", f.Calls[0].Argv, wantStart)
	}
	wantStop := []string{"systemctl", "--user", "stop", "web.service"}
	if !equalArgv(f.Calls[1].Argv, wantStop) {
		t.Fatalf("Stop argv = %v, want %v", f.Calls[1].Argv, wantStop)
	}
	for _, c := range f.Calls {
		for _, a := range c.Argv {
			if a == "enable" || a == "disable" {
				t.Fatalf("argv must not contain enable/disable: %v", c.Argv)
			}
		}
	}
}

func equalArgv(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSecretCreatePassesValueViaStdin(t *testing.T) {
	f := &host.Fake{Responses: map[string]host.Result{}}
	o := Ops{R: f, User: "pecm-web", UID: 1234}
	if err := o.SecretCreate("db_password", []byte("s3cr3t")); err != nil {
		t.Fatal(err)
	}
	var createCall *host.Call
	for i := range f.Calls {
		if strings.Contains(strings.Join(f.Calls[i].Argv, " "), "secret create") {
			createCall = &f.Calls[i]
		}
	}
	if createCall == nil {
		t.Fatal("no secret create call")
	}
	if string(createCall.Stdin) != "s3cr3t" {
		t.Fatalf("value not via stdin: %q", createCall.Stdin)
	}
	for _, a := range createCall.Argv {
		if a == "s3cr3t" {
			t.Fatal("secret leaked into argv")
		}
	}
}
