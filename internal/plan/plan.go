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
	removedSupport := map[string]bool{}
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
			// A removed support file must restart the units that referenced it, but it is kept
			// out of changedSupport: unreferenced by definition, it would otherwise trip the
			// coarse orphan fallback below and restart every unit in the cadre.
			if !fileset.IsUnitFile(name) && !fileset.IsSystemdUnit(name) {
				removedSupport[name] = true
			}
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
		// ShouldEnable separates routing from lifecycle: a native unit is enabled only if
		// it's an activator (.timer/.socket/.path) or a .service carrying [Install]. An
		// [Install]-less .service (an operator oneshot, or the synthesized prune service) is
		// fired by its companion unit, so it must never be enabled or restarted — a change
		// takes effect at the next fire, after the daemon-reload this plan already schedules.
		if !fileset.ShouldEnable(f.Name, f.Content) {
			continue
		}
		if !priorSystemd[f.Name] {
			p.EnableUnits = append(p.EnableUnits, f.Name)
			// A unit enabled for the first time whose content is unchanged (so the file loop
			// above queued no write) may predate this version as a support-file .service in the
			// Quadlet dir. Re-place it in the user unit dir and reload, so `enable --now` can
			// resolve it instead of failing on a file systemd's user manager never reads.
			if prior.Files[f.Name] == f.Hash {
				p.WriteFiles = append(p.WriteFiles, f)
				p.DaemonReload = true
			}
		} else if systemdUnitChanged[f.Name] {
			p.RestartSystemdUnits = append(p.RestartSystemdUnits, f.Name)
		}
	}
	// Disable a unit that left the desired set, or that stayed but is no longer enable-worthy
	// (a .service edited to drop its [Install] section) — otherwise its wants-symlink lingers
	// and, worse, rucher forgets it was enabled and never disables it on a later removal.
	for u := range priorSystemd {
		if f, ok := desiredFiles[u]; !ok || !fileset.ShouldEnable(f.Name, f.Content) {
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
		if anyRef(refs.Files, changedSupport) || anyRef(refs.Files, removedSupport) ||
			anyRef(refs.Secrets, changedSecret) {
			p.RestartUnits = append(p.RestartUnits, f.Name)
		}
	}

	// Coarse fallback: a changed support file that no unit references -> restart every present
	// unit, Quadlet and native systemd alike. A systemd unit's dependency on a support file
	// isn't visible in its content, so a changed orphan file restarts it too, mirroring the
	// Quadlet handling. ShouldEnable keeps the install-only .service (an operator oneshot or the
	// synthesized prune service) out — it is fired by its companion unit, never restarted directly.
	if orphanChanged(changedSupport, c.Files) {
		for _, f := range c.Files {
			switch {
			case f.IsUnit && priorUnits[f.Name] &&
				!slices.Contains(p.RestartUnits, f.Name) && !slices.Contains(p.StartUnits, f.Name):
				p.RestartUnits = append(p.RestartUnits, f.Name)
			case fileset.ShouldEnable(f.Name, f.Content) && priorSystemd[f.Name] &&
				!slices.Contains(p.RestartSystemdUnits, f.Name) && !slices.Contains(p.EnableUnits, f.Name):
				p.RestartSystemdUnits = append(p.RestartSystemdUnits, f.Name)
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

// removedAnyUnit reports whether any removed file is a unit — Quadlet, or a native systemd
// unit including an install-only .service. Its removal must trigger a daemon-reload so systemd
// drops the vanished unit. A removed enabled unit already forces the reload via DisableUnits;
// this also covers a lone install-only .service, which is tracked only in Files (no disable).
func removedAnyUnit(removed []string) bool {
	for _, name := range removed {
		if fileset.IsUnitFile(name) || fileset.IsSystemdUnit(name) {
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
