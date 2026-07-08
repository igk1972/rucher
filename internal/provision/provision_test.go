// SPDX-License-Identifier: AGPL-3.0-or-later

package provision

import (
	"strings"
	"testing"

	"rucher/internal/manifest"
	"rucher/internal/node"
)

func TestUserNameAndHome(t *testing.T) {
	if UserName("web") != "rucher-web" {
		t.Fatalf("UserName = %q", UserName("web"))
	}
	if HomeDir("web") != BaseDir()+"/web" {
		t.Fatalf("HomeDir = %q", HomeDir("web"))
	}
}

func TestEnsureUserCreatesWhenMissing(t *testing.T) {
	f := &node.Fake{Responses: map[string]node.Result{
		// id -u for a missing user: non-zero exit
		"root:id -u rucher-web": {Code: 1},
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
	for _, want := range []string{"useradd", "loginctl enable-linger rucher-web"} {
		if !strings.Contains(all, want) {
			t.Fatalf("expected command %q in:\n%s", want, all)
		}
	}
}

func TestNextSubidStart(t *testing.T) {
	if got := nextSubidStart("", ""); got != 100000 {
		t.Fatalf("empty inputs = %d, want 100000", got)
	}
	if got := nextSubidStart("a:100000:65536\n", ""); got != 165536 {
		t.Fatalf("single subuid line = %d, want 165536", got)
	}
	if got := nextSubidStart("a:100000:65536", "b:200000:65536"); got != 265536 {
		t.Fatalf("subgid dominates = %d, want 265536", got)
	}
}

func TestHasSubid(t *testing.T) {
	if !hasSubid("rucher-web:100000:65536\n", "rucher-web") {
		t.Fatal("expected hasSubid to find rucher-web")
	}
	if hasSubid("rucher-web:100000:65536\n", "rucher-api") {
		t.Fatal("expected hasSubid to not find rucher-api")
	}
}

func TestEnsureUserAllocatesFreeBlock(t *testing.T) {
	f := &node.Fake{Responses: map[string]node.Result{
		"root:id -u rucher-web": {Stdout: "1500"},
		"root:cat /etc/subuid":  {Stdout: "existing:100000:65536\n"},
		"root:cat /etc/subgid":  {Stdout: "existing:100000:65536\n"},
	}}
	uid, err := EnsureUser(f, "web")
	if err != nil {
		t.Fatal(err)
	}
	if uid != 1500 {
		t.Fatalf("uid = %d, want 1500", uid)
	}
	want := []string{"usermod", "--add-subuids", "165536-231071", "--add-subgids", "165536-231071", "rucher-web"}
	found := false
	for _, c := range f.Calls {
		if strings.Join(c.Argv, " ") == strings.Join(want, " ") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected usermod call %v in calls %v", want, f.Calls)
	}
}

func TestEnsureUserSkipsSubidWhenPresent(t *testing.T) {
	f := &node.Fake{Responses: map[string]node.Result{
		"root:id -u rucher-web": {Stdout: "1500"},
		"root:cat /etc/subuid":  {Stdout: "rucher-web:300000:65536\n"},
	}}
	if _, err := EnsureUser(f, "web"); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.Calls {
		for _, a := range c.Argv {
			if a == "--add-subuids" {
				t.Fatalf("did not expect --add-subuids, got calls %v", f.Calls)
			}
		}
	}
}

func TestApplyResourcesWritesDropInAndReloads(t *testing.T) {
	f := &node.Fake{Responses: map[string]node.Result{}}
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
