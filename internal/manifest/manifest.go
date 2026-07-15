// SPDX-License-Identifier: AGPL-3.0-or-later

// Package manifest parses and validates a rucher.yml manifest.
package manifest

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Manifest struct {
	Secrets    Secrets    `yaml:"secrets"`
	Registries Registries `yaml:"registries"`
	Resources  Resources  `yaml:"resources"`
	Prune      Prune      `yaml:"prune"`
}

type Secrets struct {
	From   string   `yaml:"from"`
	Create []string `yaml:"create"`
}

type Registries struct {
	Login []Login `yaml:"login"`
}

type Login struct {
	Registry    string `yaml:"registry"`
	Username    string `yaml:"username"`
	PasswordKey string `yaml:"passwordKey"`
	Insecure    bool   `yaml:"insecure"`
}

type Resources struct {
	MemoryMax string `yaml:"memoryMax"`
	CPUQuota  string `yaml:"cpuQuota"`
}

// Prune configures the synthesized per-cadre GC of unused podman images.
// An absent block means enabled with the defaults below.
type Prune struct {
	Enabled  *bool  `yaml:"enabled"`  // nil = default true
	Schedule string `yaml:"schedule"` // systemd OnCalendar expression
	Until    string `yaml:"until"`    // prune unused images created earlier than this Go duration ago
}

// On reports whether pruning is enabled (the default when enabled is absent).
func (p Prune) On() bool { return p.Enabled == nil || *p.Enabled }

// Prune defaults, shared with internal/prune which interpolates them into unit bodies.
const (
	DefaultPruneSchedule = "daily"
	DefaultPruneUntil    = "168h"
)

const defaultSecretsFile = "secrets.sops.yaml"

func Load(data []byte) (Manifest, error) {
	var m Manifest
	// strict decode: reject unknown keys so a typo'd field (e.g. memmoryMax) is a
	// hard error rather than being silently dropped.
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	// An empty rucher.yml is a valid nameless manifest (every field defaults); yaml.v3
	// reports an empty/comment-only document as io.EOF, which is not a parse error here.
	if err := dec.Decode(&m); err != nil && !errors.Is(err, io.EOF) {
		return Manifest{}, fmt.Errorf("parse rucher.yml: %w", err)
	}
	if m.Secrets.From == "" {
		m.Secrets.From = defaultSecretsFile
	}
	if m.Prune.Schedule == "" {
		m.Prune.Schedule = DefaultPruneSchedule
	}
	if m.Prune.Until == "" {
		m.Prune.Until = DefaultPruneUntil
	}
	return m, nil
}

func (m Manifest) Validate() error {
	for i, l := range m.Registries.Login {
		if l.Registry == "" || l.Username == "" || l.PasswordKey == "" {
			return fmt.Errorf("manifest: login[%d] needs registry, username and passwordKey", i)
		}
	}
	// Resources are interpolated verbatim into a root-owned slice drop-in
	// (provision.ApplyResources); a newline would inject extra [Slice]/[Unit] directives.
	// The anchored regexes accept only the shapes systemd itself takes, so any newline is
	// rejected too.
	if v := m.Resources.MemoryMax; v != "" && !memoryMaxRe.MatchString(v) {
		return fmt.Errorf("manifest: resources.memoryMax %q is not a valid systemd memory size (like 512M, 1G or infinity)", v)
	}
	if v := m.Resources.CPUQuota; v != "" && !cpuQuotaRe.MatchString(v) {
		return fmt.Errorf("manifest: resources.cpuQuota %q is not a valid systemd percentage (like 50%% or 200%%)", v)
	}
	if m.Prune.Until != "" {
		if _, err := time.ParseDuration(m.Prune.Until); err != nil {
			return fmt.Errorf("manifest: prune.until %q is not a duration (like 168h)", m.Prune.Until)
		}
	}
	// The schedule is interpolated into a unit body; a newline would inject directives.
	if strings.Contains(m.Prune.Schedule, "\n") {
		return fmt.Errorf("manifest: prune.schedule must be a single line")
	}
	// Catch the common typo (e.g. "dialy"): a bare alphabetic word must be a known
	// OnCalendar shortcut or weekday. Anything with digits/punctuation is a full
	// calendar expression we don't parse here (no systemd on the operator machine) —
	// an invalid one would otherwise only surface when the timer fails to enable on the
	// node, aborting an otherwise-healthy reconcile.
	if s := m.Prune.Schedule; s != "" && isAlphaWord(s) && !calendarWords[strings.ToLower(s)] {
		return fmt.Errorf("manifest: prune.schedule %q is not a known OnCalendar shortcut", s)
	}
	return nil
}

// memoryMaxRe matches a systemd MemoryMax value: a byte size (optional B/K/M/G/T/P/E
// suffix, base 1024), a percentage, or "infinity". cpuQuotaRe matches a CPUQuota
// percentage. Anchored, so any embedded newline fails to match.
var (
	memoryMaxRe = regexp.MustCompile(`^(infinity|[0-9]+(\.[0-9]+)?([BKMGTPE])?|[0-9]+(\.[0-9]+)?%)$`)
	cpuQuotaRe  = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?%$`)
)

// calendarWords are the single-word OnCalendar values (systemd shortcuts + weekday names)
// that are valid on their own.
var calendarWords = map[string]bool{
	"minutely": true, "hourly": true, "daily": true, "weekly": true, "monthly": true,
	"yearly": true, "quarterly": true, "semiannually": true, "annually": true,
	"mon": true, "tue": true, "wed": true, "thu": true, "fri": true, "sat": true, "sun": true,
	"monday": true, "tuesday": true, "wednesday": true, "thursday": true,
	"friday": true, "saturday": true, "sunday": true,
}

func isAlphaWord(s string) bool {
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
			return false
		}
	}
	return s != ""
}
