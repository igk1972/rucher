// SPDX-License-Identifier: AGPL-3.0-or-later

// Package prune synthesizes the per-cadre systemd units that garbage-collect
// unused podman images.
package prune

import (
	"rucher/internal/cadre"
	"rucher/internal/fileset"
	"rucher/internal/manifest"
)

// Files returns the synthesized GC units, nil when pruning is disabled.
// Both carry IsSystemdUnit so they are routed to the user unit dir, but only
// the timer gets a lifecycle: plan gates enable/restart/disable by extension,
// so the [Install]-less oneshot service is written and daemon-reloaded, never
// enabled (a changed ExecStart takes effect at the next timer fire).
func Files(p manifest.Prune) []cadre.File {
	if !p.On() {
		return nil
	}
	// Defaults are applied by manifest.Load; repeated here so a zero-value
	// Prune still yields valid units.
	schedule, until := p.Schedule, p.Until
	if schedule == "" {
		schedule = manifest.DefaultPruneSchedule
	}
	if until == "" {
		until = manifest.DefaultPruneUntil
	}
	service := "[Unit]\nDescription=rucher: prune unused podman images\n\n" +
		"[Service]\nType=oneshot\n" +
		"ExecStart=/usr/bin/podman image prune --all --force --filter until=" + until + "\n"
	// RandomizedDelaySec de-synchronizes the cadres of one node; Persistent
	// catches windows missed while the node was down.
	timer := "[Unit]\nDescription=rucher: schedule podman image pruning\n\n" +
		"[Timer]\nOnCalendar=" + schedule + "\nRandomizedDelaySec=15m\nPersistent=true\n\n" +
		"[Install]\nWantedBy=timers.target\n"
	return []cadre.File{unit(fileset.PruneService, service), unit(fileset.PruneTimer, timer)}
}

func unit(name, body string) cadre.File {
	return cadre.File{Name: name, Content: []byte(body), Hash: fileset.Hash([]byte(body)), IsSystemdUnit: true}
}
