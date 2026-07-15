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
