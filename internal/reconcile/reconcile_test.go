package reconcile

import (
	"strings"
	"testing"

	"podman-essaim-compartment-manager/internal/compartment"
	"podman-essaim-compartment-manager/internal/fileset"
	"podman-essaim-compartment-manager/internal/host"
	"podman-essaim-compartment-manager/internal/manifest"
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
