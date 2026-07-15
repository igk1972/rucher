// SPDX-License-Identifier: AGPL-3.0-or-later

// Package quadletref extracts support-file and secret references from a unit.
package quadletref

import (
	"path/filepath"
	"strings"
	"unicode"
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
	// Fold systemd trailing-`\` continuations first: a Secret= or --secret on a
	// continuation line would otherwise be invisible, and a missed secret ref means
	// no restart on rotation (a stale credential, with no coarse fallback to save us).
	for _, line := range strings.Split(joinContinuations(unitContent), "\n") {
		line = strings.TrimSpace(line)
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

// mountSource pulls the source out of a "type=bind,source=/x,..." Mount value.
// podman accepts both source= and its src= alias.
func mountSource(v string) string {
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if s, ok := strings.CutPrefix(part, "source="); ok {
			return s
		}
		if s, ok := strings.CutPrefix(part, "src="); ok {
			return s
		}
	}
	return ""
}

// joinContinuations folds systemd line continuations the way podman's parser does:
// a `\` immediately followed by a newline becomes a single space, joining the two
// physical lines into one logical line.
func joinContinuations(content []byte) string {
	return strings.ReplaceAll(string(content), "\\\n", " ")
}

// podmanArgSecrets finds secret names behind `--secret name[,opts]` (and --secret=name),
// so a secret mounted via raw PodmanArgs still ties its unit to a rotation.
func podmanArgSecrets(v string) []string {
	toks := splitQuoted(v)
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

// fileArgPrefixes are the podman flags whose value is a support-file path, in
// the single-token `--flag=value` form.
var fileArgPrefixes = []string{"--volume=", "--mount=", "--env-file=", "-v="}

// podmanArgFiles finds file paths behind -v/--volume/--mount/--env-file, in both
// the space form (`--volume x`) and the equals form (`--volume=x`).
func podmanArgFiles(v string) []string {
	toks := splitQuoted(v)
	var out []string
	for i := 0; i < len(toks); i++ {
		switch toks[i] {
		case "-v", "--volume", "--mount", "--env-file":
			if i+1 < len(toks) {
				out = append(out, toks[i+1])
				i++
			}
			continue
		}
		for _, pfx := range fileArgPrefixes {
			if s, ok := strings.CutPrefix(toks[i], pfx); ok {
				out = append(out, s)
				break
			}
		}
	}
	return out
}

// splitQuoted splits a PodmanArgs value on whitespace the way systemd's quoted
// parsing does: single quotes are literal, double quotes and backslashes escape,
// so a quoted `--volume "/my data:/data"` yields one token with the real path.
func splitQuoted(v string) []string {
	var out []string
	var cur strings.Builder
	inWord := false
	escaped := false
	var quote rune // 0, '\'' or '"'
	flush := func() {
		if inWord {
			out = append(out, cur.String())
			cur.Reset()
			inWord = false
		}
	}
	for _, r := range v {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case quote == '\'': // single quotes are literal
			if r == '\'' {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case quote == '"':
			switch r {
			case '\\':
				escaped = true
			case '"':
				quote = 0
			default:
				cur.WriteRune(r)
			}
		case r == '\\':
			escaped = true
			inWord = true
		case r == '\'' || r == '"':
			quote = r
			inWord = true
		case unicode.IsSpace(r):
			flush()
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	flush()
	return out
}
