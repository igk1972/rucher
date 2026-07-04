package provision

import (
	"strings"
	"testing"

	"podman-essaim-compartment-manager/internal/host"
	"podman-essaim-compartment-manager/internal/manifest"
)

func TestUserNameAndHome(t *testing.T) {
	if UserName("web") != "pecm-web" {
		t.Fatalf("UserName = %q", UserName("web"))
	}
	if HomeDir("web") != BaseDir+"/web" {
		t.Fatalf("HomeDir = %q", HomeDir("web"))
	}
}

func TestEnsureUserCreatesWhenMissing(t *testing.T) {
	f := &host.Fake{Responses: map[string]host.Result{
		// id -u for a missing user: non-zero exit
		"root:id -u pecm-web": {Code: 1},
		// after creation, id -u returns the uid (call recorded again below)
	}}
	// Second id -u (after useradd) should return the uid; emulate by switching response.
	// We assert the sequence of commands instead of the returned uid here.
	_, _ = EnsureUser(f, "web")
	var joined []string
	for _, c := range f.Calls {
		joined = append(joined, strings.Join(c.Argv, " "))
	}
	all := strings.Join(joined, "\n")
	for _, want := range []string{"useradd", "loginctl enable-linger pecm-web"} {
		if !strings.Contains(all, want) {
			t.Fatalf("expected command %q in:\n%s", want, all)
		}
	}
}

func TestApplyResourcesWritesDropInAndReloads(t *testing.T) {
	f := &host.Fake{Responses: map[string]host.Result{}}
	if err := ApplyResources(f, 1234, manifest.Resources{MemoryMax: "512M", CPUQuota: "50%"}); err != nil {
		t.Fatal(err)
	}
	var all strings.Builder
	sawReload := false
	for _, c := range f.Calls {
		line := strings.Join(c.Argv, " ")
		all.WriteString(line + "\n")
		if strings.Contains(line, "daemon-reload") && c.Root {
			sawReload = true
		}
	}
	if !strings.Contains(all.String(), "user-1234.slice.d") {
		t.Fatalf("drop-in path missing:\n%s", all.String())
	}
	if !sawReload {
		t.Fatal("expected root daemon-reload")
	}
}
