// SPDX-License-Identifier: AGPL-3.0-or-later

package ops

import (
	"strings"
	"testing"

	"rucher/internal/age"
	"rucher/internal/node"
)

func TestUnitService(t *testing.T) {
	cases := map[string]string{
		"web.container": "web.service",
		"app.kube":      "app.service", // .kube, like .container, maps to the bare stem
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
	f := &node.Fake{Responses: map[string]node.Result{}}
	o := Ops{R: f, User: "rucher-web", UID: 1234}

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

func TestSystemdUnitLifecycleUsesRawUnitName(t *testing.T) {
	// Native systemd units are managed by their own name (no Quadlet .service mapping),
	// and — unlike Quadlet units — a .timer/.socket/.path is enabled/disabled.
	f := &node.Fake{Responses: map[string]node.Result{}}
	o := Ops{R: f, User: "rucher-web", UID: 1234}

	if err := o.EnableNow("backup.timer"); err != nil {
		t.Fatal(err)
	}
	if err := o.RestartUnit("backup.timer"); err != nil {
		t.Fatal(err)
	}
	if err := o.DisableNow("backup.timer"); err != nil {
		t.Fatal(err)
	}

	want := [][]string{
		{"systemctl", "--user", "enable", "--now", "backup.timer"},
		{"systemctl", "--user", "restart", "backup.timer"},
		{"systemctl", "--user", "disable", "--now", "backup.timer"},
	}
	if len(f.Calls) != len(want) {
		t.Fatalf("calls = %d, want %d: %+v", len(f.Calls), len(want), f.Calls)
	}
	for i := range want {
		if !equalArgv(f.Calls[i].Argv, want[i]) {
			t.Fatalf("call %d argv = %v, want %v", i, f.Calls[i].Argv, want[i])
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

func TestPositionalsGuardedByDoubleDash(t *testing.T) {
	// A registry/secret name beginning with '-' must reach podman as a positional, not be
	// parsed as a flag: argv must carry a `--` separator immediately before it.
	assertDashBeforeLast := func(t *testing.T, argv []string) {
		t.Helper()
		if len(argv) < 2 || argv[len(argv)-2] != "--" {
			t.Fatalf("expected `--` before the trailing positional in %v", argv)
		}
	}

	f := &node.Fake{Responses: map[string]node.Result{}}
	o := Ops{R: f, User: "rucher-web", UID: 1234}

	if err := o.Login("-registry.example.com", "u", []byte("pw"), false); err != nil {
		t.Fatal(err)
	}
	if err := o.SecretRemove("-name"); err != nil {
		t.Fatal(err)
	}

	if len(f.Calls) != 2 {
		t.Fatalf("expected 2 calls, got %d: %+v", len(f.Calls), f.Calls)
	}
	assertDashBeforeLast(t, f.Calls[0].Argv) // podman login ... -- -registry.example.com
	assertDashBeforeLast(t, f.Calls[1].Argv) // podman secret rm -- -name

	// SecretCreate: `-- NAME -`, so the name is guarded and the stdin marker stays last.
	f = &node.Fake{Responses: map[string]node.Result{}}
	o = Ops{R: f, User: "rucher-web", UID: 1234}
	if err := o.SecretCreate("-name", []byte("v")); err != nil {
		t.Fatal(err)
	}
	want := []string{"podman", "secret", "create", "--", "-name", "-"}
	if !equalArgv(f.Calls[len(f.Calls)-1].Argv, want) {
		t.Fatalf("SecretCreate argv = %v, want %v", f.Calls[len(f.Calls)-1].Argv, want)
	}
}

func TestGenerateAgeKey(t *testing.T) {
	f := &node.Fake{Responses: map[string]node.Result{}}
	o := Ops{R: f, User: "rucher-web", UID: 1500}

	recipient, err := o.GenerateAgeKey("/id/identity.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(recipient, "age1") {
		t.Fatalf("recipient = %q, want an age1 prefix", recipient)
	}

	// The key is random, so we prove correctness by capturing the identity written to
	// disk and back-deriving its recipient: it must equal the returned one. The write
	// must land at 0600 from creation (cat under umask 077), never a tee that leaves a
	// 0644 window.
	var installed *node.Call
	for i := range f.Calls {
		c := &f.Calls[i]
		if c.Argv[0] == "sh" && len(c.Argv) == 5 && strings.Contains(c.Argv[2], "umask 077") &&
			strings.Contains(c.Argv[2], "cat") && c.Argv[4] == "/id/identity.txt" {
			installed = c
		}
		if c.Argv[0] == "tee" {
			t.Fatalf("identity must not be written with tee (0644 window): %v", c.Argv)
		}
	}
	if installed == nil {
		t.Fatal("no identity-write (`sh -c 'umask 077 && cat > ...'`) call recorded")
	}
	id := strings.TrimSpace(string(installed.Stdin))
	if !strings.HasPrefix(id, "AGE-SECRET-KEY-1") {
		t.Fatalf("install stdin = %q, want an AGE-SECRET-KEY-1 identity", id)
	}
	back, err := age.RecipientFor(id)
	if err != nil {
		t.Fatal(err)
	}
	if back != recipient {
		t.Fatalf("recipient from written identity = %q, want returned %q", back, recipient)
	}
}

func TestSecretRemoveDistinguishesRealFailure(t *testing.T) {
	o := func(f *node.Fake) Ops { return Ops{R: f, User: "rucher-web", UID: 1234} }

	// Absent secret -> nil (idempotent).
	f := &node.Fake{Responses: map[string]node.Result{
		"user:1234:podman secret rm -- gone": {Code: 125, Stderr: "Error: no such secret gone"},
	}}
	if err := o(f).SecretRemove("gone"); err != nil {
		t.Fatalf("removing an absent secret should be a no-op, got %v", err)
	}

	// Any other failure -> error (not swallowed).
	f = &node.Fake{Responses: map[string]node.Result{
		"user:1234:podman secret rm -- db": {Code: 125, Stderr: "Error: database is locked"},
	}}
	if err := o(f).SecretRemove("db"); err == nil {
		t.Fatal("a real secret-rm failure must be returned")
	}
}

func TestSecretCreatePassesValueViaStdin(t *testing.T) {
	f := &node.Fake{Responses: map[string]node.Result{}}
	o := Ops{R: f, User: "rucher-web", UID: 1234}
	if err := o.SecretCreate("db_password", []byte("s3cr3t")); err != nil {
		t.Fatal(err)
	}
	var createCall *node.Call
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
