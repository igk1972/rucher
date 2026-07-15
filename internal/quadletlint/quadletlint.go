// SPDX-License-Identifier: AGPL-3.0-or-later

// Package quadletlint validates a cadre's Quadlet unit files with Podman's own
// parser/converter, so a bad Image=, an invalid PublishPort or an unknown key is
// caught by `ops validate` on the operator machine instead of only failing the
// Quadlet generator on the node. It is used by the operator-side validate command,
// never on the apply/agent hot path.
package quadletlint

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"go.podman.io/podman/v6/pkg/systemd/parser"
	"go.podman.io/podman/v6/pkg/systemd/quadlet"
)

// isUser mirrors rucher's model: every cadre runs rootless under `systemctl --user`.
const isUser = true

// Check validates one cadre's Quadlet units in-memory (no disk, no systemd). files
// maps a unit filename ("web.container") to its contents; pass only Quadlet unit
// files (.container/.volume/.network/.pod/.kube/.image/.build), not .timer/.socket/
// .path or support files. It returns non-fatal warnings and fatal errors separately,
// each prefixed with the offending filename.
//
// Cross-unit references (Volume=x.volume, Network=x.network, Pod=x.pod) resolve only
// when every referenced unit is in files; a plain external volume/network name
// (pgdata:/data, Network=host) is tolerated and not checked.
func Check(files map[string]string) (warnings, fatal []string) {
	var units []*parser.UnitFile
	for name, content := range files {
		u := parser.NewUnitFile()
		if err := u.Parse(content); err != nil {
			fatal = append(fatal, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		u.Filename = name // Parse() does not set it; service/resource names + map key derive from it
		units = append(units, u)
	}

	// Convert in dependency order (image < volume/network < build <
	// container/kube < pod) so a unit's referenced units are resolved first.
	sort.SliceStable(units, func(i, j int) bool {
		return quadlet.SupportedExtensions[filepath.Ext(units[i].Filename)] <
			quadlet.SupportedExtensions[filepath.Ext(units[j].Filename)]
	})

	// Prefill an info entry for every unit before converting any, so cross-references
	// (and the unit's own lookup) succeed.
	info := make(map[string]*quadlet.UnitInfo)
	for _, u := range units {
		svc, _ := quadlet.GetUnitServiceName(u)
		var resource string
		var containers []string
		switch filepath.Ext(u.Filename) {
		case ".container":
			resource = quadlet.GetContainerResourceName(u)
		case ".build":
			resource = quadlet.GetBuiltImageName(u)
		case ".pod":
			containers = make([]string, 0)
			resource = quadlet.GetPodResourceName(u)
		}
		info[u.Filename] = &quadlet.UnitInfo{ServiceName: svc, ResourceName: resource, ContainersToStart: containers}
	}

	for _, u := range units {
		var warn, err error
		switch filepath.Ext(u.Filename) {
		case ".container":
			_, warn, err = quadlet.ConvertContainer(u, info, isUser)
		case ".volume":
			_, warn, err = quadlet.ConvertVolume(u, info, isUser)
		case ".network":
			_, warn, err = quadlet.ConvertNetwork(u, info, isUser)
		case ".pod":
			_, warn, err = quadlet.ConvertPod(u, info, isUser)
		case ".build":
			_, warn, err = quadlet.ConvertBuild(u, info, isUser)
		case ".kube":
			_, err = quadlet.ConvertKube(u, info, isUser)
		case ".image":
			_, err = quadlet.ConvertImage(u, info, isUser)
		default:
			continue // not a Quadlet unit type; caller should not have passed it
		}
		if warn != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %s", u.Filename, strings.TrimSpace(warn.Error())))
		}
		if err != nil {
			fatal = append(fatal, fmt.Sprintf("%s: %s", u.Filename, strings.TrimSpace(err.Error())))
		}
	}
	return warnings, fatal
}
