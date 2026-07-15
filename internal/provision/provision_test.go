// SPDX-License-Identifier: AGPL-3.0-or-later

package provision

import (
	"slices"
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

func TestValidName(t *testing.T) {
	for _, ok := range []string{"web", "web-1", "a", "0abc", "a-b-c"} {
		if !ValidName(ok) {
			t.Errorf("ValidName(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"", "Web", "../etc", "a/b", "a.b", "a b", "-lead", "a-very-long-name-exceeding-limit"} {
		if ValidName(bad) {
			t.Errorf("ValidName(%q) = true, want false", bad)
		}
	}
}

func TestEnsureUserRejectsInvalidName(t *testing.T) {
	f := &node.Fake{Responses: map[string]node.Result{}}
	if _, err := EnsureUser(f, "../escape"); err == nil {
		t.Fatal("EnsureUser must reject a traversal name")
	}
	for _, c := range f.Calls {
		if c.Argv[0] == "useradd" {
			t.Fatal("useradd must not run for an invalid name")
		}
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
		// Both maps must carry the user for the allocation to be skipped.
		"root:cat /etc/subuid": {Stdout: "rucher-web:300000:65536\n"},
		"root:cat /etc/subgid": {Stdout: "rucher-web:300000:65536\n"},
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

func TestEnsureUserAllocatesSubidWhenSubgidMissing(t *testing.T) {
	// subuid present but subgid absent (a usermod interrupted mid-write): the allocation must
	// still run so the missing subgid range is repaired.
	f := &node.Fake{Responses: map[string]node.Result{
		"root:id -u rucher-web": {Stdout: "1500"},
		"root:cat /etc/subuid":  {Stdout: "rucher-web:300000:65536\n"},
		"root:cat /etc/subgid":  {Stdout: ""},
	}}
	if _, err := EnsureUser(f, "web"); err != nil {
		t.Fatal(err)
	}
	var sawAdd bool
	for _, c := range f.Calls {
		if slices.Contains(c.Argv, "--add-subgids") {
			sawAdd = true
		}
	}
	if !sawAdd {
		t.Fatalf("expected a usermod --add-subgids to repair the missing subgid, got %v", f.Calls)
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

func TestApplyResourcesErrorsOnTeeFailure(t *testing.T) {
	// A non-zero exit (e.g. read-only FS) must surface as an error: plan.Compute only
	// re-applies Resources on change, so a silently-dropped write never retries.
	f := &node.Fake{Responses: map[string]node.Result{
		"root:tee /etc/systemd/system/user-1234.slice.d/50-rucher.conf": {Code: 1, Stderr: "read-only file system"},
	}}
	if err := ApplyResources(f, 1234, manifest.Resources{MemoryMax: "512M"}); err == nil {
		t.Fatal("ApplyResources must error when tee exits non-zero")
	}
}
