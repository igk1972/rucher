// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"rucher/internal/cadre"
)

// cadreName matches names that stay useradd-compatible once prefixed with
// "rucher-"; maxCadreName keeps the user name within useradd's 32-char limit.
var cadreName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

const maxCadreName = 25

func initManifest(name string) string {
	return "# " + name + " — cadre manifest. The cadre's name is this directory's name; every key\n" +
		"# is optional and an empty manifest is valid. Uncomment what you need.\n" +
		"#\n" +
		"# secrets:\n" +
		"#   from: secrets.sops.yaml    # SOPS+age file; its keys become podman secrets\n" +
		"# registries:\n" +
		"#   login:\n" +
		"#     - registry: ghcr.io\n" +
		"#       username: deploy\n" +
		"#       passwordKey: ghcr_token\n" +
		"# resources:                   # systemd slice limits for the whole cadre\n" +
		"#   memoryMax: 512M\n" +
		"#   cpuQuota: \"50%\"\n" +
		"# prune:                       # image GC (default: enabled, daily, older than 168h)\n" +
		"#   enabled: true\n"
}

func initUnit(name string) string {
	return "[Unit]\nDescription=" + name + " web\n\n" +
		"[Container]\nImage=docker.io/library/nginx:alpine\nPublishPort=127.0.0.1:8080:80\n\n" +
		"[Install]\nWantedBy=default.target\n"
}

// cmdInit scaffolds a new cadre directory: a commented manifest plus a minimal
// working Quadlet unit, ready for validate/plan/apply.
func cmdInit(dir, name string, out io.Writer) int {
	if !cadreName.MatchString(name) || len(name) > maxCadreName {
		fmt.Fprintf(out, "error: cadre name %q must match [a-z0-9][a-z0-9-]* and be at most %d characters\n", name, maxCadreName)
		return 1
	}
	target := filepath.Join(dir, name)
	if _, err := os.Stat(target); err == nil {
		fmt.Fprintf(out, "error: %s already exists\n", target)
		return 1
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		fmt.Fprintln(out, "error:", err)
		return 1
	}
	for fn, body := range map[string]string{
		"rucher.yml":    initManifest(name),
		"web.container": initUnit(name),
	} {
		if err := os.WriteFile(filepath.Join(target, fn), []byte(body), 0o644); err != nil {
			fmt.Fprintln(out, "error:", err)
			return 1
		}
	}
	// The scaffold must always load as a valid cadre; failing here is a rucher bug.
	if _, err := cadre.Load(target); err != nil {
		fmt.Fprintf(out, "error: scaffold failed self-validation: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "created %s/{rucher.yml,web.container}\n", target)
	fmt.Fprintf(out, "next: rucher ops validate --dir %[1]s %[2]s, rucher ops plan --dir %[1]s %[2]s,\n"+
		"then on the node: sudo rucher node cadre apply --dir <dir> %[2]s\n", dir, name)
	return 0
}
