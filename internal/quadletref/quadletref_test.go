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

func TestExtractSecretOnContinuationLine(t *testing.T) {
	// A `--secret` on a systemd `\`-continuation line must still be seen, or the
	// unit would not restart on rotation and would keep a stale credential.
	unit := []byte("[Container]\nImage=nginx\nPodmanArgs=--label a \\\n  --secret dbpass\n")
	r := Extract(unit)
	if !slices.Contains(r.Secrets, "dbpass") {
		t.Fatalf("Secrets = %v, want dbpass", r.Secrets)
	}
}

func TestExtractPodmanArgsEqualsForms(t *testing.T) {
	// The single-token equals form must be tracked like the space form.
	for _, tc := range []struct {
		unit string
		file string
	}{
		{"[Container]\nImage=nginx\nPodmanArgs=--env-file=app.env\n", "app.env"},
		{"[Container]\nImage=nginx\nPodmanArgs=--volume=cfg.conf:/etc/x\n", "cfg.conf"},
		{"[Container]\nImage=nginx\nPodmanArgs=-v=data.txt:/d\n", "data.txt"},
	} {
		r := Extract([]byte(tc.unit))
		if !slices.Contains(r.Files, tc.file) {
			t.Fatalf("Files = %v, want %s for unit %q", r.Files, tc.file, tc.unit)
		}
	}
}

func TestExtractPodmanArgsQuotedVolume(t *testing.T) {
	// A quoted source with an embedded space must not be mis-split on whitespace.
	unit := []byte("[Container]\nImage=nginx\nPodmanArgs=--volume \"/my data:/data\"\n")
	r := Extract(unit)
	if !slices.Contains(r.Files, "my data") {
		t.Fatalf("Files = %v, want 'my data'", r.Files)
	}
}

func TestExtractMountSrcAlias(t *testing.T) {
	// podman's src= alias for source= must resolve the same cadre-local ref.
	unit := []byte("[Container]\nImage=nginx\nMount=type=bind,src=/data/x,relabel=shared\n")
	r := Extract(unit)
	if !slices.Contains(r.Files, "x") {
		t.Fatalf("Files = %v, want x", r.Files)
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
