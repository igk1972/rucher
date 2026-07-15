// SPDX-License-Identifier: AGPL-3.0-or-later

package deploy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"rucher/internal/agentcfg"
	"rucher/internal/sshx"
)

// nodesDirWith writes <dir>/<name>/configuration.yml with a network.address so
// sshresolve resolves a target, and returns the dir.
func nodesDirWith(t *testing.T, name, address string) string {
	t.Helper()
	dir := t.TempDir()
	nd := filepath.Join(dir, name)
	if err := os.MkdirAll(nd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nd, "configuration.yml"),
		[]byte("network:\n  address: "+address+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func target(address string) sshx.Target {
	return sshx.Target{Addr: address + ":22", User: "root"}
}

// callWith returns the first call whose joined argv equals `joined`.
func callWith(f *sshx.Fake, joined string) *sshx.Call {
	for i := range f.Calls {
		if strings.Join(f.Calls[i].Cmd, " ") == joined {
			return &f.Calls[i]
		}
	}
	return nil
}

func hasCmd(f *sshx.Fake, joined string) bool {
	return callWith(f, joined) != nil
}

func TestDeployBinaryBootstrap(t *testing.T) {
	dir := nodesDirWith(t, "web", "10.0.0.1")
	tg := target("10.0.0.1")
	f := &sshx.Fake{Responses: map[string]sshx.Result{
		sshx.Key(tg, []string{"dpkg", "--print-architecture"}):             {Stdout: "arm64\n"},
		sshx.Key(tg, []string{"sudo", installPath, "node", "key", "init"}): {Stdout: "age1rcpt\n"},
	}}
	opts := Options{
		Binary:    []byte("FAKEBINARY"),
		Bootstrap: true,
		Store:     agentcfg.StoreConfig{Kind: "git", URL: "git@example.com:store.git", Branch: "main"},
		Interval:  "30s",
	}
	rows, err := Run(f, dir, "", nil, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	r := rows[0]
	if !r.OK || r.Arch != "arm64" || r.Recipient != "age1rcpt" || !r.AgentInstalled {
		t.Fatalf("row = %+v", r)
	}
	// Base platform is provisioned (idempotently) via `sudo sh -s`.
	if !hasCmd(f, "sudo sh -s") {
		t.Fatal("base-platform provision not run")
	}
	// The binary is streamed via stdin into a staged file, then moved into place.
	up := callWith(f, "sudo install -m0755 /dev/stdin "+stagePath)
	if up == nil || string(up.Stdin) != "FAKEBINARY" {
		t.Fatalf("binary upload call missing or wrong stdin: %+v", up)
	}
	if !hasCmd(f, "sudo mv -f "+stagePath+" "+installPath) {
		t.Fatal("upload is not atomic (no mv into place)")
	}
	// agent.yml written 0600 (it holds store credentials) and agent installed.
	if callWith(f, "sudo install -D -m0600 /dev/stdin "+agentCfg) == nil {
		t.Fatal("agent.yml not written with mode 0600")
	}
	if !hasCmd(f, "sudo "+installPath+" node agent install") {
		t.Fatal("node agent install not run")
	}
}

func TestDeployDownloadNoBootstrap(t *testing.T) {
	dir := nodesDirWith(t, "web", "10.0.0.1")
	tg := target("10.0.0.1")
	f := &sshx.Fake{Responses: map[string]sshx.Result{
		sshx.Key(tg, []string{"dpkg", "--print-architecture"}):             {Stdout: "amd64\n"},
		sshx.Key(tg, []string{"sudo", installPath, "node", "key", "init"}): {Stdout: "age1x\n"},
	}}
	rows, err := Run(f, dir, "", nil, Options{Version: "v0.1.0"})
	if err != nil {
		t.Fatal(err)
	}
	r := rows[0]
	if !r.OK || r.AgentInstalled {
		t.Fatalf("row = %+v (agent should be off without a store)", r)
	}
	want := "https://github.com/igk1972/rucher/releases/download/v0.1.0/rucher_linux_amd64"
	if callWith(f, "sudo curl -fsSL "+want+" -o "+stagePath) == nil {
		t.Fatalf("download call with URL %q missing; calls: %v", want, f.Calls)
	}
	if hasCmd(f, "sudo "+installPath+" node agent install") {
		t.Fatal("agent install must not run without a store")
	}
}

func TestDeployNodeFailureRecorded(t *testing.T) {
	dir := nodesDirWith(t, "web", "10.0.0.1")
	tg := target("10.0.0.1")
	f := &sshx.Fake{Responses: map[string]sshx.Result{
		sshx.Key(tg, []string{"dpkg", "--print-architecture"}):             {Stdout: "arm64\n"},
		sshx.Key(tg, []string{"sudo", installPath, "node", "key", "init"}): {Code: 1, Stderr: "not root"},
	}}
	rows, err := Run(f, dir, "", nil, Options{Binary: []byte("x")})
	if err != nil {
		t.Fatal(err)
	}
	r := rows[0]
	if r.OK || len(r.Errors) == 0 || !strings.Contains(r.Errors[0], "node key init") {
		t.Fatalf("expected a recorded key-init failure, got %+v", r)
	}
}

// TestDeployPreservesOrderUnderConcurrency deploys several nodes with a bounded
// pool and asserts rows come back in the order of names. Also exercises the
// concurrent path for the race detector.
func TestDeployPreservesOrderUnderConcurrency(t *testing.T) {
	root := t.TempDir()
	names := []string{"n0", "n1", "n2", "n3"}
	resp := map[string]sshx.Result{}
	for i, name := range names {
		addr := fmt.Sprintf("10.0.0.%d", i)
		nd := filepath.Join(root, name)
		if err := os.MkdirAll(nd, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(nd, "configuration.yml"),
			[]byte("network:\n  address: "+addr+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		tg := target(addr)
		resp[sshx.Key(tg, []string{"dpkg", "--print-architecture"})] = sshx.Result{Stdout: "arm64\n"}
		resp[sshx.Key(tg, []string{"sudo", installPath, "node", "key", "init"})] = sshx.Result{Stdout: "age1" + name + "\n"}
	}
	f := &sshx.Fake{Responses: resp}

	rows, err := Run(f, root, "", names, Options{Binary: []byte("x"), Concurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(names) {
		t.Fatalf("rows = %d, want %d", len(rows), len(names))
	}
	for i, name := range names {
		if rows[i].Node != name {
			t.Fatalf("rows[%d].Node = %q, want %q (order not preserved)", i, rows[i].Node, name)
		}
		if !rows[i].OK {
			t.Fatalf("rows[%d] not OK: %+v", i, rows[i])
		}
	}
}

func TestDeployRejectsShellUnsafeRefs(t *testing.T) {
	dir := nodesDirWith(t, "web", "10.0.0.1")
	tg := target("10.0.0.1")
	arch := func() *sshx.Fake {
		return &sshx.Fake{Responses: map[string]sshx.Result{
			sshx.Key(tg, []string{"dpkg", "--print-architecture"}): {Stdout: "arm64\n"},
		}}
	}

	// A shell-injection payload in the prebuilt podman version must be rejected before
	// the provision script (which runs as root via `sudo sh -s`) is ever assembled.
	f := arch()
	rows, err := Run(f, dir, "", nil, Options{Binary: []byte("x"), PodmanSource: "prebuilt", PodmanVersion: `v6";reboot;"`})
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].OK || len(rows[0].Errors) == 0 || !strings.Contains(rows[0].Errors[0], "invalid podman version") {
		t.Fatalf("bad podman version must fail with an invalid-tag error: %+v", rows[0])
	}
	if hasCmd(f, "sudo sh -s") {
		t.Fatal("provision script must not run for a rejected version")
	}

	// Same for the release download tag (reaches the node inside a curl URL).
	rows, err = Run(arch(), dir, "", nil, Options{Version: "v1;rm -rf /"})
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].OK || len(rows[0].Errors) == 0 || !strings.Contains(rows[0].Errors[0], "invalid release version") {
		t.Fatalf("bad release tag must fail: %+v", rows[0])
	}
}

func TestPodmanTarballURL(t *testing.T) {
	// Empty version resolves to the newest release via the latest/download redirect.
	if got, want := podmanTarballURL(""),
		"https://github.com/igk1972/podman-6-deb/releases/latest/download/podman6-trixie-${arch}.tar.gz"; got != want {
		t.Errorf("podmanTarballURL(\"\") = %q, want %q", got, want)
	}
	// A pinned version maps to that release tag.
	if got, want := podmanTarballURL("v6.0.1"),
		"https://github.com/igk1972/podman-6-deb/releases/download/v6.0.1/podman6-trixie-${arch}.tar.gz"; got != want {
		t.Errorf("podmanTarballURL(pinned) = %q, want %q", got, want)
	}
}

// TestDeployPodmanSourceThreaded proves the deploy-time podman source/version reaches
// the provision script that runs on the node (streamed as the `sudo sh -s` stdin).
func TestDeployPodmanSourceThreaded(t *testing.T) {
	provisionStdin := func(opts Options) string {
		dir := nodesDirWith(t, "web", "10.0.0.1")
		tg := target("10.0.0.1")
		f := &sshx.Fake{Responses: map[string]sshx.Result{
			sshx.Key(tg, []string{"dpkg", "--print-architecture"}):             {Stdout: "arm64\n"},
			sshx.Key(tg, []string{"sudo", installPath, "node", "key", "init"}): {Stdout: "age1x\n"},
		}}
		opts.Binary = []byte("x")
		if _, err := Run(f, dir, "", nil, opts); err != nil {
			t.Fatal(err)
		}
		c := callWith(f, "sudo sh -s")
		if c == nil {
			t.Fatal("base-platform provision not run")
		}
		return string(c.Stdin)
	}
	// Default: the distro apt package.
	if s := provisionStdin(Options{}); !strings.Contains(s, "apt-get install -y -qq -o Dpkg::Options::=--force-confold podman") {
		t.Errorf("default deploy should apt-install podman:\n%s", s)
	}
	// Prebuilt, pinned tag: the per-arch .deb tarball for that release.
	if s := provisionStdin(Options{PodmanSource: "prebuilt", PodmanVersion: "v6.0.1"}); !strings.Contains(s, "/releases/download/v6.0.1/podman6-trixie-${arch}.tar.gz") {
		t.Errorf("prebuilt deploy should provision the pinned tarball:\n%s", s)
	}
	// Prebuilt, no version: the latest tarball.
	if s := provisionStdin(Options{PodmanSource: "prebuilt"}); !strings.Contains(s, "/releases/latest/download/podman6-trixie-${arch}.tar.gz") {
		t.Errorf("prebuilt deploy (latest) should provision the latest tarball:\n%s", s)
	}
}

func TestRenderAgentConfigRoundTrips(t *testing.T) {
	body, err := renderAgentConfig("web", Options{
		Store:    agentcfg.StoreConfig{Kind: "git", URL: "git@example.com:store.git"},
		Interval: "45s",
	})
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "agent.yml")
	if err := os.WriteFile(p, body, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := agentcfg.Load(p)
	if err != nil {
		t.Fatalf("generated agent.yml does not load: %v", err)
	}
	if cfg.Node != "web" || cfg.Store.URL != "git@example.com:store.git" || cfg.Interval != "45s" || cfg.Store.Branch != "main" {
		t.Fatalf("round-trip mismatch: %+v", cfg)
	}
}
