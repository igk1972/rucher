// SPDX-License-Identifier: AGPL-3.0-or-later

package cadre

import (
	"bufio"
	"bytes"
	"fmt"
	"strings"
)

// Warnings reports advisory problems that must not fail validation: publishing
// a port on all interfaces is legitimate for a public service, but it is the
// main cross-cadre visibility vector (a neighbour reaches it through the host
// IP), so the operator should opt in consciously.
func (c Cadre) Warnings() []string {
	var out []string
	for _, f := range c.Files {
		if !f.IsUnit {
			continue
		}
		sc := bufio.NewScanner(bytes.NewReader(f.Content))
		sc.Buffer(nil, maxUnitLine) // don't let a long line truncate the scan (default 64KB cap)
		for sc.Scan() {
			key, val, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
			if !ok || strings.TrimSpace(key) != "PublishPort" {
				continue
			}
			val = strings.TrimSpace(val)
			if publishBindsAllInterfaces(val) {
				out = append(out, fmt.Sprintf(
					"unit %s: PublishPort=%s publishes on all interfaces; use PublishPort=127.0.0.1:<host>:<ctr> unless the service is meant to be public",
					f.Name, val))
			}
		}
	}
	return out
}

// publishBindsAllInterfaces parses podman's [[ip:][hostPort]:]containerPort[/proto]
// publish syntax and reports whether the host binding covers all interfaces.
// The /proto suffix sticks to containerPort and never affects the ip part.
func publishBindsAllInterfaces(val string) bool {
	if val == "" {
		return false
	}
	if strings.HasPrefix(val, "[") { // bracketed IPv6 host address
		ip, _, ok := strings.Cut(val[1:], "]")
		return ok && ip == "::"
	}
	switch parts := strings.Split(val, ":"); len(parts) {
	case 1, 2: // containerPort / hostPort:containerPort -> podman binds 0.0.0.0
		return true
	case 3:
		return parts[0] == "" || parts[0] == "0.0.0.0"
	default: // unbracketed IPv6 or malformed: cannot judge, stay silent
		return false
	}
}
