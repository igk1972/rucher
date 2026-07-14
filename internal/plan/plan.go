// SPDX-License-Identifier: AGPL-3.0-or-later

// Package plan computes an idempotent reconcile plan from desired state vs prior state.
package plan

import (
	"slices"
	"strings"

	"rucher/internal/cadre"
	"rucher/internal/fileset"
	"rucher/internal/manifest"
	"rucher/internal/quadletref"
	"rucher/internal/state"
)

type Plan struct {
	Name          string
	WriteFiles    []cadre.File
	RemoveFiles   []string
	CreateSecrets []string
	RemoveSecrets []string
	Resources     *manifest.Resources // non-nil when slice limits must be (re)applied
	DaemonReload  bool
	StartUnits    []string // Quadlet units to start (new)
	RestartUnits  []string // Quadlet units to restart (changed / referencing changed input)
	StopUnits     []string // Quadlet units to stop (removed)
	// Native systemd units (.timer/.socket/.path), managed by their own unit name.
	EnableUnits         []string // new -> `enable --now`
	RestartSystemdUnits []string // changed -> `restart`
	DisableUnits        []string // removed -> `disable --now`
}

func (p Plan) Empty() bool {
	return len(p.WriteFiles) == 0 && len(p.RemoveFiles) == 0 &&
		len(p.CreateSecrets) == 0 && len(p.RemoveSecrets) == 0 &&
		p.Resources == nil && !p.DaemonReload &&
		len(p.StartUnits) == 0 && len(p.RestartUnits) == 0 && len(p.StopUnits) == 0 &&
		len(p.EnableUnits) == 0 && len(p.RestartSystemdUnits) == 0 && len(p.DisableUnits) == 0
}

func Compute(c cadre.Cadre, secretHashes map[string]string, prior state.State) Plan {
	p := Plan{Name: c.Name}

	desiredFiles := map[string]cadre.File{}
	for _, f := range c.Files {
		desiredFiles[f.Name] = f
	}

	// Files: write changed/new, remember which support files changed.
	changedSupport := map[string]bool{}
	unitFileChanged := map[string]bool{}
	systemdUnitChanged := map[string]bool{} // new or changed native systemd unit files
	for name, f := range desiredFiles {
		if prior.Files[name] != f.Hash {
			p.WriteFiles = append(p.WriteFiles, f)
			switch {
			case f.IsUnit:
				unitFileChanged[name] = true
			case f.IsSystemdUnit:
				systemdUnitChanged[name] = true
			default:
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

	// Native systemd units: enable new ones, restart changed ones, disable removed ones.
	priorSystemd := map[string]bool{}
	for _, u := range prior.SystemdUnits {
		priorSystemd[u] = true
	}
	for _, f := range c.Files {
		// IsSystemdUnit routes a file to the user unit dir; lifecycle applies only to
		// real .timer/.socket/.path units. The synthesized prune .service carries the
		// flag but is [Install]-less and fired by its timer, so it must never be
		// enabled or restarted — a change takes effect at the next fire, after the
		// daemon-reload this plan already schedules.
		if !f.IsSystemdUnit || !fileset.IsSystemdUnit(f.Name) {
			continue
		}
		if !priorSystemd[f.Name] {
			p.EnableUnits = append(p.EnableUnits, f.Name)
		} else if systemdUnitChanged[f.Name] {
			p.RestartSystemdUnits = append(p.RestartSystemdUnits, f.Name)
		}
	}
	for u := range priorSystemd {
		if _, ok := desiredFiles[u]; !ok {
			p.DisableUnits = append(p.DisableUnits, u)
		}
	}

	// Reload if any unit file (Quadlet or systemd) was written or removed.
	if len(unitFileChanged) > 0 || len(systemdUnitChanged) > 0 ||
		removedAnyUnit(p.RemoveFiles) || len(p.DisableUnits) > 0 {
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

	slices.SortFunc(p.WriteFiles, func(a, b cadre.File) int { return strings.Compare(a.Name, b.Name) })
	slices.Sort(p.StartUnits)
	slices.Sort(p.RestartUnits)
	slices.Sort(p.StopUnits)
	slices.Sort(p.EnableUnits)
	slices.Sort(p.RestartSystemdUnits)
	slices.Sort(p.DisableUnits)
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
func orphanChanged(changedSupport map[string]bool, files []cadre.File) bool {
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
