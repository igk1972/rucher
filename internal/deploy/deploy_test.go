package deploy

import (
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
	// agent.yml written and agent installed.
	if callWith(f, "sudo install -D -m0644 /dev/stdin "+agentCfg) == nil {
		t.Fatal("agent.yml not written")
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

func TestPodmanURL(t *testing.T) {
	// Empty version resolves to the newest release via the latest/download redirect.
	if got, want := podmanURL(""),
		"https://github.com/mgoltzsche/podman-static/releases/latest/download/podman-linux-${arch}.tar.gz"; got != want {
		t.Errorf("podmanURL(\"\") = %q, want %q", got, want)
	}
	// A pinned version maps to that exact release tag.
	if got, want := podmanURL("5.8.4"),
		"https://github.com/mgoltzsche/podman-static/releases/download/v5.8.4/podman-linux-${arch}.tar.gz"; got != want {
		t.Errorf("podmanURL(pinned) = %q, want %q", got, want)
	}
}

// TestDeployPodmanVersionThreaded proves the deploy-time podman override reaches the
// provision script that runs on the node (streamed as the `sudo sh -s` stdin).
func TestDeployPodmanVersionThreaded(t *testing.T) {
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
	if s := provisionStdin(Options{}); !strings.Contains(s, "/releases/latest/download/podman-linux-${arch}.tar.gz") {
		t.Errorf("default deploy should provision the latest podman:\n%s", s)
	}
	if s := provisionStdin(Options{PodmanVersion: "5.9.0"}); !strings.Contains(s, "/download/v5.9.0/podman-linux-${arch}.tar.gz") {
		t.Errorf("pinned deploy should provision the pinned podman:\n%s", s)
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
