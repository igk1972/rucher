// SPDX-License-Identifier: AGPL-3.0-or-later

// Package quadletref extracts support-file and secret references from a unit.
package quadletref

import (
	"bufio"
	"bytes"
	"path/filepath"
	"strings"
)

type Refs struct {
	Files   []string
	Secrets []string
}

// fileKeys reference a single path as their value (possibly with :opts suffix).
var fileKeys = map[string]bool{
	"EnvironmentFile": true, "Volume": true, "Mount": true,
	"AddDevice": true, "Rootfs": true, "ContainersConfModule": true,
	"Yaml": true, "File": true, "SetWorkingDirectory": true,
}

func Extract(unitContent []byte) Refs {
	var r Refs
	seenF := map[string]bool{}
	addFile := func(raw string) {
		// keep only cadre-local basenames; drop container-side/opts parts
		raw = strings.TrimSpace(raw)
		raw = strings.SplitN(raw, ":", 2)[0] // "host:container:opts" -> host for Volume/Mount source
		if src := mountSource(raw); src != "" {
			raw = src
		}
		if raw == "" {
			return
		}
		base := filepath.Base(raw)
		if base != "." && base != "/" && !seenF[base] {
			seenF[base] = true
			r.Files = append(r.Files, base)
		}
	}
	sc := bufio.NewScanner(bytes.NewReader(unitContent))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		switch {
		case fileKeys[key]:
			addFile(val)
		case key == "Secret":
			name := strings.SplitN(val, ",", 2)[0]
			r.Secrets = append(r.Secrets, strings.TrimSpace(name))
		case key == "PodmanArgs":
			for _, p := range podmanArgFiles(val) {
				addFile(p)
			}
			for _, s := range podmanArgSecrets(val) {
				r.Secrets = append(r.Secrets, s)
			}
		}
	}
	return r
}

// mountSource pulls source= out of a "type=bind,source=/x,..." Mount value.
func mountSource(v string) string {
	for _, part := range strings.Split(v, ",") {
		if s, ok := strings.CutPrefix(strings.TrimSpace(part), "source="); ok {
			return s
		}
	}
	return ""
}

// podmanArgSecrets finds secret names behind `--secret name[,opts]` (and --secret=name),
// so a secret mounted via raw PodmanArgs still ties its unit to a rotation.
func podmanArgSecrets(v string) []string {
	toks := strings.Fields(v)
	var out []string
	for i := 0; i < len(toks); i++ {
		var arg string
		switch {
		case toks[i] == "--secret" && i+1 < len(toks):
			arg = toks[i+1]
			i++
		default:
			s, ok := strings.CutPrefix(toks[i], "--secret=")
			if !ok {
				continue
			}
			arg = s
		}
		if name := strings.SplitN(arg, ",", 2)[0]; name != "" {
			out = append(out, name)
		}
	}
	return out
}

// podmanArgFiles finds file paths behind -v/--volume/--mount/--env-file.
func podmanArgFiles(v string) []string {
	toks := strings.Fields(v)
	var out []string
	for i := 0; i < len(toks); i++ {
		switch toks[i] {
		case "-v", "--volume", "--mount", "--env-file":
			if i+1 < len(toks) {
				out = append(out, toks[i+1])
				i++
			}
		}
	}
	return out
}
