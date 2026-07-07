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
	c := cadre.Cadre{Name: "web"}
	for name, body := range files {
		c.Files = append(c.Files, cadre.File{
			Name: name, Content: []byte(body), Hash: fileset.Hash([]byte(body)),
			IsUnit:        fileset.IsUnitFile(name),
			IsSystemdUnit: fileset.IsSystemdUnit(name),
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

func TestSystemdTimerEnabledOnFreshInstall(t *testing.T) {
	c := comp(map[string]string{
		"backup.container": "[Container]\nImage=busybox\n",
		"backup.timer":     "[Timer]\nOnCalendar=daily\n[Install]\nWantedBy=timers.target\n",
	})
	p := Compute(c, nil, state.State{})
	if !slices.Contains(p.EnableUnits, "backup.timer") {
		t.Fatalf("EnableUnits = %v, want backup.timer", p.EnableUnits)
	}
	if len(p.RestartSystemdUnits) != 0 {
		t.Fatalf("RestartSystemdUnits = %v, want none on fresh install", p.RestartSystemdUnits)
	}
	if !slices.Contains(p.StartUnits, "backup.container") {
		t.Fatalf("StartUnits = %v, want backup.container", p.StartUnits)
	}
	if !p.DaemonReload {
		t.Fatal("a new .timer must trigger daemon-reload")
	}
}

func TestSystemdTimerRestartOnChangeAndDisableOnRemoval(t *testing.T) {
	timer := "[Timer]\nOnCalendar=hourly\n[Install]\nWantedBy=timers.target\n"
	c := comp(map[string]string{"backup.timer": timer})
	prior := state.State{
		Files: map[string]string{
			// backup.timer present but with a different body -> changed -> restart
			"backup.timer": fileset.Hash([]byte("[Timer]\nOnCalendar=daily\n[Install]\nWantedBy=timers.target\n")),
			// old.timer no longer desired -> disable + remove
			"old.timer": fileset.Hash([]byte("[Timer]\nOnCalendar=weekly\n")),
		},
		SystemdUnits: []string{"backup.timer", "old.timer"},
		SecretHashes: map[string]string{},
	}
	p := Compute(c, nil, prior)
	if !slices.Equal(p.RestartSystemdUnits, []string{"backup.timer"}) {
		t.Fatalf("RestartSystemdUnits = %v, want [backup.timer]", p.RestartSystemdUnits)
	}
	if len(p.EnableUnits) != 0 {
		t.Fatalf("EnableUnits = %v, want none (already present)", p.EnableUnits)
	}
	if !slices.Contains(p.DisableUnits, "old.timer") {
		t.Fatalf("DisableUnits = %v, want old.timer", p.DisableUnits)
	}
	if !slices.Contains(p.RemoveFiles, "old.timer") {
		t.Fatalf("RemoveFiles = %v, want old.timer", p.RemoveFiles)
	}
	if !p.DaemonReload {
		t.Fatal("a changed/removed systemd unit must trigger daemon-reload")
	}
}

func TestSystemdTimerNoOpWhenUnchanged(t *testing.T) {
	timer := "[Timer]\nOnCalendar=daily\n[Install]\nWantedBy=timers.target\n"
	c := comp(map[string]string{"backup.timer": timer})
	prior := state.State{
		Files:        map[string]string{"backup.timer": fileset.Hash([]byte(timer))},
		SystemdUnits: []string{"backup.timer"},
		SecretHashes: map[string]string{},
	}
	if p := Compute(c, nil, prior); !p.Empty() {
		t.Fatalf("expected an empty plan for an unchanged timer, got %+v", p)
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
