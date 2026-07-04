package reconcile

import (
	"strings"
	"testing"

	"podman-essaim-compartment-manager/internal/compartment"
	"podman-essaim-compartment-manager/internal/fileset"
	"podman-essaim-compartment-manager/internal/host"
	"podman-essaim-compartment-manager/internal/manifest"
	"podman-essaim-compartment-manager/internal/provision"
)

func TestApplyFreshWritesFilesAndStarts(t *testing.T) {
	c := compartment.Compartment{Name: "web", Manifest: manifest.Manifest{Name: "web"}}
	body := "[Container]\nImage=nginx\n"
	c.Files = []compartment.File{{Name: "web.container", Content: []byte(body), Hash: fileset.Hash([]byte(body)), IsUnit: true}}

	f := &host.Fake{Responses: map[string]host.Result{
		"root:id -u pecm-web": {Stdout: "1234", Code: 0},
	}}
	// override statePath to a temp location for the test
	oldBase := baseDirForState
	baseDirForState = t.TempDir()
	defer func() { baseDirForState = oldBase }()

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
