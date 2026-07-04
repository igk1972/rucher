package plan

import (
	"slices"
	"testing"

	"podman-essaim-compartment-manager/internal/compartment"
	"podman-essaim-compartment-manager/internal/fileset"
	"podman-essaim-compartment-manager/internal/manifest"
	"podman-essaim-compartment-manager/internal/state"
)

func comp(files map[string]string) compartment.Compartment {
	c := compartment.Compartment{Name: "web", Manifest: manifest.Manifest{Name: "web"}}
	for name, body := range files {
		c.Files = append(c.Files, compartment.File{
			Name: name, Content: []byte(body), Hash: fileset.Hash([]byte(body)),
			IsUnit: fileset.IsUnitFile(name),
		})
	}
	return c
}

func TestFreshInstallStartsUnits(t *testing.T) {
	c := comp(map[string]string{
		"web.container": "[Container]\nImage=nginx\nEnvironmentFile=%h/.config/containers/systemd/app.env\n",
		"app.env":       "A=1\n",
	})
	p := Compute(c, nil, state.State{})
	if !p.DaemonReload {
		t.Fatal("want DaemonReload on fresh install")
	}
	if !slices.Contains(p.StartUnits, "web.container") {
		t.Fatalf("StartUnits = %v", p.StartUnits)
	}
	if len(p.WriteFiles) != 2 {
		t.Fatalf("WriteFiles = %d", len(p.WriteFiles))
	}
}

func TestNoOpWhenUnchanged(t *testing.T) {
	c := comp(map[string]string{"web.container": "[Container]\nImage=nginx\n"})
	prior := state.State{
		Files:        map[string]string{"web.container": c.Files[0].Hash},
		Units:        []string{"web.container"},
		SecretHashes: map[string]string{},
	}
	p := Compute(c, nil, prior)
	if !p.Empty() {
		t.Fatalf("expected empty plan, got %+v", p)
	}
}

func TestSupportFileChangeRestartsOnlyReferencingUnit(t *testing.T) {
	unit := "[Container]\nImage=nginx\nEnvironmentFile=%h/.config/containers/systemd/app.env\n"
	c := comp(map[string]string{"web.container": unit, "app.env": "A=2\n", "other.container": "[Container]\nImage=redis\n"})
	prior := state.State{
		Files: map[string]string{
			"web.container":   fileset.Hash([]byte(unit)),
			"app.env":         fileset.Hash([]byte("A=1\n")), // changed
			"other.container": fileset.Hash([]byte("[Container]\nImage=redis\n")),
		},
		Units:        []string{"web.container", "other.container"},
		SecretHashes: map[string]string{},
	}
	p := Compute(c, nil, prior)
	if p.DaemonReload {
		t.Fatal("no unit file changed; daemon-reload not expected")
	}
	if !slices.Equal(p.RestartUnits, []string{"web.container"}) {
		t.Fatalf("RestartUnits = %v, want [web.container]", p.RestartUnits)
	}
}

func TestRemovedFileIsDeleted(t *testing.T) {
	c := comp(map[string]string{"web.container": "[Container]\nImage=nginx\n"})
	prior := state.State{
		Files:        map[string]string{"web.container": c.Files[0].Hash, "old.env": "h"},
		Units:        []string{"web.container"},
		SecretHashes: map[string]string{},
	}
	p := Compute(c, nil, prior)
	if !slices.Contains(p.RemoveFiles, "old.env") {
		t.Fatalf("RemoveFiles = %v", p.RemoveFiles)
	}
}
