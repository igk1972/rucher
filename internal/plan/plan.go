// Package plan computes an idempotent reconcile plan from desired state vs prior state.
package plan

import (
	"slices"
	"strings"

	"podman-essaim-compartment-manager/internal/compartment"
	"podman-essaim-compartment-manager/internal/manifest"
	"podman-essaim-compartment-manager/internal/quadletref"
	"podman-essaim-compartment-manager/internal/state"
)

type Plan struct {
	Name          string
	WriteFiles    []compartment.File
	RemoveFiles   []string
	CreateSecrets []string
	RemoveSecrets []string
	Logins        []manifest.Login
	Resources     *manifest.Resources // non-nil when slice limits must be (re)applied
	DaemonReload  bool
	StartUnits    []string
	RestartUnits  []string
	StopUnits     []string
}

func (p Plan) Empty() bool {
	return len(p.WriteFiles) == 0 && len(p.RemoveFiles) == 0 &&
		len(p.CreateSecrets) == 0 && len(p.RemoveSecrets) == 0 && len(p.Logins) == 0 &&
		p.Resources == nil && !p.DaemonReload &&
		len(p.StartUnits) == 0 && len(p.RestartUnits) == 0 && len(p.StopUnits) == 0
}

func Compute(c compartment.Compartment, secretHashes map[string]string, prior state.State) Plan {
	p := Plan{Name: c.Name}

	desiredFiles := map[string]compartment.File{}
	for _, f := range c.Files {
		desiredFiles[f.Name] = f
	}

	// Files: write changed/new, remember which support files changed.
	changedSupport := map[string]bool{}
	unitFileChanged := map[string]bool{}
	for name, f := range desiredFiles {
		if prior.Files[name] != f.Hash {
			p.WriteFiles = append(p.WriteFiles, f)
			if f.IsUnit {
				unitFileChanged[name] = true
			} else {
				changedSupport[name] = true
			}
		}
	}
	for name := range prior.Files {
		if _, ok := desiredFiles[name]; !ok {
			p.RemoveFiles = append(p.RemoveFiles, name)
		}
	}

	// Secrets: (re)create on value-hash change; remove ones no longer present.
	changedSecret := map[string]bool{}
	for key, h := range secretHashes {
		if prior.SecretHashes[key] != h {
			p.CreateSecrets = append(p.CreateSecrets, key)
			changedSecret[key] = true
		}
	}
	for key := range prior.SecretHashes {
		if _, ok := secretHashes[key]; !ok {
			p.RemoveSecrets = append(p.RemoveSecrets, key)
		}
	}

	// Resource limits: re-apply when they differ.
	if c.Manifest.Resources != prior.Resources {
		r := c.Manifest.Resources
		p.Resources = &r
	}

	// Reload if any unit file was written or removed.
	if len(unitFileChanged) > 0 || removedAnyUnit(p.RemoveFiles) {
		p.DaemonReload = true
	}

	// Restart scope: per unit, compare state and references.
	priorUnits := map[string]bool{}
	for _, u := range prior.Units {
		priorUnits[u] = true
	}
	for _, f := range c.Files {
		if !f.IsUnit {
			continue
		}
		if !priorUnits[f.Name] {
			p.StartUnits = append(p.StartUnits, f.Name)
			continue
		}
		if unitFileChanged[f.Name] {
			p.RestartUnits = append(p.RestartUnits, f.Name)
			continue
		}
		refs := quadletref.Extract(f.Content)
		if anyRef(refs.Files, changedSupport) || anyRef(refs.Secrets, changedSecret) {
			p.RestartUnits = append(p.RestartUnits, f.Name)
		}
	}

	// Coarse fallback: a changed support file that no unit references -> restart all units.
	if orphanChanged(changedSupport, c.Files) {
		for _, f := range c.Files {
			if f.IsUnit && priorUnits[f.Name] && !slices.Contains(p.RestartUnits, f.Name) &&
				!slices.Contains(p.StartUnits, f.Name) {
				p.RestartUnits = append(p.RestartUnits, f.Name)
			}
		}
	}

	// Stop units that disappeared.
	for u := range priorUnits {
		if _, ok := desiredFiles[u]; !ok {
			p.StopUnits = append(p.StopUnits, u)
		}
	}

	slices.SortFunc(p.WriteFiles, func(a, b compartment.File) int { return strings.Compare(a.Name, b.Name) })
	slices.Sort(p.StartUnits)
	slices.Sort(p.RestartUnits)
	slices.Sort(p.StopUnits)
	slices.Sort(p.RemoveFiles)
	slices.Sort(p.CreateSecrets)
	slices.Sort(p.RemoveSecrets)
	return p
}

func anyRef(refs []string, changed map[string]bool) bool {
	for _, r := range refs {
		if changed[r] {
			return true
		}
	}
	return false
}

func removedAnyUnit(removed []string) bool {
	for _, name := range removed {
		if len(name) > 0 && isUnitName(name) {
			return true
		}
	}
	return false
}

func isUnitName(name string) bool {
	for _, ext := range []string{".container", ".volume", ".network", ".pod", ".kube", ".image", ".build"} {
		if len(name) >= len(ext) && name[len(name)-len(ext):] == ext {
			return true
		}
	}
	return false
}

// orphanChanged reports whether a changed support file is referenced by no unit.
func orphanChanged(changedSupport map[string]bool, files []compartment.File) bool {
	if len(changedSupport) == 0 {
		return false
	}
	referenced := map[string]bool{}
	for _, f := range files {
		if !f.IsUnit {
			continue
		}
		for _, r := range quadletref.Extract(f.Content).Files {
			referenced[r] = true
		}
	}
	for name := range changedSupport {
		if !referenced[name] {
			return true
		}
	}
	return false
}
