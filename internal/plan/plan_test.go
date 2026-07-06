package plan

import (
	"slices"
	"testing"

	"rucher/internal/cadre"
	"rucher/internal/fileset"
	"rucher/internal/manifest"
	"rucher/internal/state"
)

func comp(files map[string]string) cadre.Cadre {
	c := cadre.Cadre{Name: "web", Manifest: manifest.Manifest{Name: "web"}}
	for name, body := range files {
		c.Files = append(c.Files, cadre.File{
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

func TestCoarseFallbackRestartsUnreferencedUnits(t *testing.T) {
	unit := "[Container]\nImage=nginx\n" // references no support file
	c := comp(map[string]string{"web.container": unit, "orphan.conf": "new\n"})
	prior := state.State{
		Files: map[string]string{
			"web.container": fileset.Hash([]byte(unit)),
			"orphan.conf":   fileset.Hash([]byte("old\n")), // changed, referenced by nobody
		},
		Units:        []string{"web.container"},
		SecretHashes: map[string]string{},
	}
	p := Compute(c, nil, prior)
	if !slices.Contains(p.RestartUnits, "web.container") {
		t.Fatalf("RestartUnits = %v, want [web.container] via coarse fallback", p.RestartUnits)
	}
}

func TestSecretCreateRotateAndRemove(t *testing.T) {
	c := comp(map[string]string{"web.container": "[Container]\nImage=nginx\n"})
	secretHashes := map[string]string{
		"new_key": "h-new",     // absent in prior -> create
		"rot_key": "h-current", // changed vs prior -> re-create
	}
	prior := state.State{
		Files: map[string]string{"web.container": c.Files[0].Hash},
		Units: []string{"web.container"},
		SecretHashes: map[string]string{
			"rot_key":  "h-old", // different hash -> rotate
			"gone_key": "h-x",   // absent now -> remove
		},
	}
	p := Compute(c, secretHashes, prior)
	if !slices.Contains(p.CreateSecrets, "new_key") || !slices.Contains(p.CreateSecrets, "rot_key") {
		t.Fatalf("CreateSecrets = %v, want new_key and rot_key", p.CreateSecrets)
	}
	if !slices.Contains(p.RemoveSecrets, "gone_key") {
		t.Fatalf("RemoveSecrets = %v, want gone_key", p.RemoveSecrets)
	}
}

func TestResourceLimitsChange(t *testing.T) {
	c := comp(map[string]string{"web.container": "[Container]\nImage=nginx\n"})
	c.Manifest.Resources = manifest.Resources{MemoryMax: "512M"}
	prior := state.State{
		Files:        map[string]string{"web.container": c.Files[0].Hash},
		Units:        []string{"web.container"},
		SecretHashes: map[string]string{},
		Resources:    manifest.Resources{MemoryMax: "256M"},
	}
	p := Compute(c, nil, prior)
	if p.Resources == nil || *p.Resources != c.Manifest.Resources {
		t.Fatalf("Resources = %v, want %v", p.Resources, c.Manifest.Resources)
	}
	prior.Resources = c.Manifest.Resources // now equal
	if p2 := Compute(c, nil, prior); p2.Resources != nil {
		t.Fatalf("Resources = %v, want nil when unchanged", p2.Resources)
	}
}

func TestStopUnitsWhenUnitRemoved(t *testing.T) {
	c := comp(map[string]string{"web.container": "[Container]\nImage=nginx\n"})
	prior := state.State{
		Files: map[string]string{
			"web.container": c.Files[0].Hash,
			"old.container": fileset.Hash([]byte("[Container]\nImage=redis\n")),
		},
		Units:        []string{"web.container", "old.container"},
		SecretHashes: map[string]string{},
	}
	p := Compute(c, nil, prior)
	if !slices.Contains(p.StopUnits, "old.container") {
		t.Fatalf("StopUnits = %v, want old.container", p.StopUnits)
	}
	if !slices.Contains(p.RemoveFiles, "old.container") {
		t.Fatalf("RemoveFiles = %v, want old.container", p.RemoveFiles)
	}
	if !p.DaemonReload {
		t.Fatal("removing a unit file must trigger daemon-reload")
	}
}
