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
	}
	for in, want := range cases {
		if got := unitService(in); got != want {
			t.Fatalf("unitService(%q) = %q, want %q", in, got, want)
		}
	}
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
