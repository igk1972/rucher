// SPDX-License-Identifier: AGPL-3.0-or-later

package quadletlint

import (
	"strings"
	"testing"
)

func hasSubstr(ss []string, sub string) bool {
	for _, s := range ss {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func TestCheckValidContainer(t *testing.T) {
	_, fatal := Check(map[string]string{
		"web.container": "[Container]\nImage=docker.io/library/nginx:alpine\nPublishPort=127.0.0.1:8080:80\n",
	})
	if len(fatal) != 0 {
		t.Fatalf("valid container produced fatal errors: %v", fatal)
	}
}

func TestCheckMissingImage(t *testing.T) {
	_, fatal := Check(map[string]string{
		"web.container": "[Container]\nPublishPort=127.0.0.1:8080:80\n",
	})
	if !hasSubstr(fatal, "web.container") {
		t.Fatalf("a container with no Image must be fatal, got %v", fatal)
	}
}

func TestCheckBadPort(t *testing.T) {
	// ExposeHostPort format is validated during conversion (PublishPort is parsed
	// later by podman's specgen, so it is not caught here — a documented limit).
	_, fatal := Check(map[string]string{
		"web.container": "[Container]\nImage=nginx\nExposeHostPort=not-a-port\n",
	})
	if len(fatal) == 0 {
		t.Fatal("an invalid ExposeHostPort must be fatal")
	}
}

func TestCheckUnknownKey(t *testing.T) {
	_, fatal := Check(map[string]string{
		"web.container": "[Container]\nImage=nginx\nFrobnicate=yes\n",
	})
	if len(fatal) == 0 {
		t.Fatal("an unknown key must be fatal")
	}
}

func TestCheckValidNonContainerTypes(t *testing.T) {
	// Every unit type must dispatch to the right Convert* (incl. the single-return
	// Kube/Image) and a minimal valid unit of each must not produce a false fatal —
	// otherwise operators with these unit types could never pass validate.
	_, fatal := Check(map[string]string{
		"d.volume":  "[Volume]\n",
		"n.network": "[Network]\n",
		"p.pod":     "[Pod]\n",
		"i.image":   "[Image]\nImage=docker.io/library/nginx:alpine\n",
		"k.kube":    "[Kube]\nYaml=app.yml\n",
	})
	if len(fatal) != 0 {
		t.Fatalf("minimal valid units of each type must not be fatal, got %v", fatal)
	}
}

func TestCheckIsolatesErrorsPerUnit(t *testing.T) {
	// A bad unit must not taint the good ones: exactly one fatal, naming the culprit.
	_, fatal := Check(map[string]string{
		"good.container": "[Container]\nImage=nginx\n",
		"bad.container":  "[Container]\nExec=sleep 1\n", // no Image
	})
	if len(fatal) != 1 || !strings.Contains(fatal[0], "bad.container") {
		t.Fatalf("want a single fatal naming bad.container, got %v", fatal)
	}
	if hasSubstr(fatal, "good.container") {
		t.Fatalf("the valid unit must not appear in fatal: %v", fatal)
	}
}

func TestCheckCrossReferenceResolves(t *testing.T) {
	// A container referencing a .volume in the same cadre must validate; the same
	// reference without the .volume present must fail.
	ok := map[string]string{
		"data.volume":   "[Volume]\n",
		"web.container": "[Container]\nImage=nginx\nVolume=data.volume:/data\n",
	}
	if _, fatal := Check(ok); len(fatal) != 0 {
		t.Fatalf("cross-reference to a present .volume must resolve, got %v", fatal)
	}
	missing := map[string]string{
		"web.container": "[Container]\nImage=nginx\nVolume=data.volume:/data\n",
	}
	if _, fatal := Check(missing); len(fatal) == 0 {
		t.Fatal("reference to an absent .volume must be fatal")
	}
}
