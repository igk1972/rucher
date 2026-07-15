// SPDX-License-Identifier: AGPL-3.0-or-later

package quadletref

import (
	"slices"
	"testing"
)

func TestExtract(t *testing.T) {
	unit := []byte(`
[Container]
Image=nginx
EnvironmentFile=%h/.config/containers/systemd/app.env
Volume=%h/.config/containers/systemd/nginx.conf:/etc/nginx/nginx.conf:ro
Secret=db_password,type=env,target=DB
PodmanArgs=--env-file %h/.config/containers/systemd/extra.env
`)
	r := Extract(unit)
	wantFiles := []string{"app.env", "extra.env", "nginx.conf"}
	got := append([]string(nil), r.Files...)
	slices.Sort(got)
	if !slices.Equal(got, wantFiles) {
		t.Fatalf("Files = %v, want %v", got, wantFiles)
	}
	if !slices.Equal(r.Secrets, []string{"db_password"}) {
		t.Fatalf("Secrets = %v", r.Secrets)
	}
}

func TestExtractPodmanArgsSecret(t *testing.T) {
	// A secret mounted via raw PodmanArgs (both --secret name and --secret=name forms)
	// must be tracked so its unit restarts on rotation.
	for _, unit := range []string{
		"[Container]\nImage=nginx\nPodmanArgs=--secret api_token,type=env,target=TOK\n",
		"[Container]\nImage=nginx\nPodmanArgs=--secret=api_token\n",
	} {
		r := Extract([]byte(unit))
		if !slices.Contains(r.Secrets, "api_token") {
			t.Fatalf("Secrets = %v, want api_token for unit %q", r.Secrets, unit)
		}
	}
}
