//go:build integration

// Package integration drives the real Lima nodes end-to-end. It is gated behind
// the `integration` build tag so `go test ./...` never touches a node; run it
// explicitly:
//
//	go test -tags integration ./test/integration/ -v
//
// The suite builds a linux/arm64 rucher, installs it on each node it uses
// (/usr/local/bin/rucher, the path the agent unit hard-codes), and drives the
// nodes over `limactl shell`. Operator-side commands run from a host-built
// binary, so `ops nodes status` exercises the project's own sshx client against
// the Lima ssh.config. When the nodes are not running the whole suite skips.
package integration

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"net/http/cgi"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Node names — these must match both the Lima instance names and the
// ../../../nodes/<name> config directories.
const (
	node1 = "lima-essaim-01"
	node2 = "lima-essaim-02"
	node3 = "lima-essaim-03"
)

// binDir holds the built binaries for the whole test binary run; TestMain removes it.
var (
	binDir    string
	hostBin   string // host arch, runs the operator-side commands locally
	buildOnce sync.Once
	buildErr  error

	linuxMu   sync.Mutex
	linuxBins = map[string]string{} // GOARCH -> a linux rucher built for the nodes

	installMu sync.Mutex
	installed = map[string]bool{}
)

// The store is served to the guests over smart HTTP (git-http-backend behind an
// in-process Go server) so the nodes need no git binary — go-git is pure Go over
// http, matching how a real deployment pulls from https://. The server lives inside
// the test process (no external daemon to be reaped mid-run) and the guests reach it
// over the Lima gateway at host.lima.internal. git runs only on the host, as CGI.
const storePort = "9418"

var (
	storeSrv  *http.Server
	storeErr  error
	serveErr  error  // set if the store server's Serve returns (i.e. it stopped)
	storeBase string // GIT_PROJECT_ROOT: every store repo is a child of it
	reachOnce sync.Once
	reachErr  error
)

// TestMain builds the binaries and starts the store server once, cleaning both up.
func TestMain(m *testing.M) {
	storeErr = startStoreServer()
	code := m.Run()
	stopStoreServer()
	if binDir != "" {
		os.RemoveAll(binDir)
	}
	os.Exit(code)
}

// startStoreServer serves $HOME/.cache/rucher-integration over smart HTTP via
// git-http-backend as CGI, so guests can clone any store repo created under it.
func startStoreServer() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	storeBase = filepath.Join(home, ".cache", "rucher-integration")
	if err := os.MkdirAll(storeBase, 0o755); err != nil {
		return err
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return fmt.Errorf("git not found on host (needed to serve the store): %w", err)
	}
	handler := &cgi.Handler{
		Path: gitPath,
		Args: []string{"http-backend"},
		Env: []string{
			"GIT_PROJECT_ROOT=" + storeBase,
			"GIT_HTTP_EXPORT_ALL=1", // serve every repo under the root without an export marker
		},
	}
	ln, err := net.Listen("tcp", "0.0.0.0:"+storePort)
	if err != nil {
		return fmt.Errorf("listen on :%s: %w", storePort, err)
	}
	storeSrv = &http.Server{Handler: handler}
	go func() { serveErr = storeSrv.Serve(ln) }() // lives until stopStoreServer
	return nil
}

func stopStoreServer() {
	if storeSrv != nil {
		storeSrv.Close()
	}
}

// gitURL is the http URL a guest uses to clone a store from the host server. The
// store is a non-bare repo, so the git dir served by http-backend is its .git.
func gitURL(store string) string {
	return "http://host.lima.internal:" + storePort + "/" + filepath.Base(store) + "/.git"
}

// result is the outcome of a command (local or on a node).
type result struct {
	stdout string
	stderr string
	code   int
}

// out returns trimmed stdout, the common case for a command that prints one value.
func (r result) out() string { return strings.TrimSpace(r.stdout) }

// moduleRoot is the rucher module root (two levels up from test/integration).
func moduleRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}

// nodesDir is the operator's node-config directory (../nodes, a sibling of the module).
func nodesDir(t *testing.T) string {
	return filepath.Clean(filepath.Join(moduleRoot(t), "..", "nodes"))
}

// build compiles the host binary (operator-side commands) once and creates the shared
// bin dir. The node binary is built per node architecture by linuxBinary.
func build(t *testing.T) {
	t.Helper()
	buildOnce.Do(func() {
		binDir, buildErr = os.MkdirTemp("", "rucher-it-")
		if buildErr != nil {
			return
		}
		hostBin = filepath.Join(binDir, "rucher-host")
		if out, err := goBuild(moduleRoot(t), hostBin, "", ""); err != nil {
			buildErr = fmt.Errorf("build host: %w\n%s", err, out)
		}
	})
	if buildErr != nil {
		t.Fatalf("build host binary: %v", buildErr)
	}
}

// linuxBinary builds (once per GOARCH) a linux rucher for a node's architecture, so the
// suite works on arm64 (Mac / macOS runners) and amd64 (Linux CI) nodes alike.
func linuxBinary(t *testing.T, goarch string) string {
	t.Helper()
	build(t) // ensure binDir + host binary
	linuxMu.Lock()
	defer linuxMu.Unlock()
	if p, ok := linuxBins[goarch]; ok {
		return p
	}
	p := filepath.Join(binDir, "rucher-linux-"+goarch)
	if out, err := goBuild(moduleRoot(t), p, "linux", goarch); err != nil {
		t.Fatalf("build linux/%s: %v\n%s", goarch, err, out)
	}
	linuxBins[goarch] = p
	return p
}

// nodeGoarch reports a node's architecture as a Go GOARCH (arm64|amd64); dpkg's
// architecture names already match GOARCH.
func nodeGoarch(t *testing.T, node string) string {
	t.Helper()
	a := strings.TrimSpace(nodeShell(t, node, "dpkg", "--print-architecture").stdout)
	switch a {
	case "arm64", "amd64":
		return a
	default:
		t.Fatalf("node %s: unsupported architecture %q", node, a)
		return ""
	}
}

func goBuild(root, out, goos, goarch string) (string, error) {
	cmd := exec.Command("go", "build", "-trimpath", "-o", out, "./cmd/rucher")
	cmd.Dir = root
	cmd.Env = os.Environ()
	if goos != "" {
		cmd.Env = append(cmd.Env, "GOOS="+goos, "GOARCH="+goarch)
	}
	b, err := cmd.CombinedOutput()
	return string(b), err
}

// requireNodes skips the test unless limactl is present and every named node is Running.
func requireNodes(t *testing.T, names ...string) {
	t.Helper()
	if _, err := exec.LookPath("limactl"); err != nil {
		t.Skip("limactl not found; skipping integration test")
	}
	out, err := exec.Command("limactl", "list", "--format", "{{.Name}}={{.Status}}").CombinedOutput()
	if err != nil {
		t.Skipf("limactl list failed (%v); skipping", err)
	}
	status := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			status[k] = v
		}
	}
	for _, n := range names {
		if status[n] != "Running" {
			t.Skipf("node %s not Running (status=%q); skipping", n, status[n])
		}
	}
	build(t)
	for _, n := range names {
		installRucher(t, n)
	}
}

// installRucher builds the node's-arch binary and copies it to /usr/local/bin/rucher.
// It runs once per node per test-binary invocation.
func installRucher(t *testing.T, node string) {
	t.Helper()
	bin := linuxBinary(t, nodeGoarch(t, node))
	installMu.Lock()
	defer installMu.Unlock()
	if installed[node] {
		return
	}
	staged := "/tmp/rucher-it"
	if out, err := exec.Command("limactl", "copy", bin, node+":"+staged).CombinedOutput(); err != nil {
		t.Fatalf("copy rucher to %s: %v\n%s", node, err, out)
	}
	if r := nodeSudo(t, node, "install", "-m", "0755", staged, "/usr/local/bin/rucher"); r.code != 0 {
		t.Fatalf("install rucher on %s: %s", node, r.stderr)
	}
	installed[node] = true
}

// runCmd executes a prepared command and captures stdout/stderr/exit code.
// A non-zero exit is returned in result.code (not as a Go error), matching how the
// rest of the codebase treats process exit codes.
func runCmd(t *testing.T, cmd *exec.Cmd, stdin []byte) result {
	t.Helper()
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	res := result{stdout: out.String(), stderr: errb.String()}
	if ee, ok := err.(*exec.ExitError); ok {
		res.code = ee.ExitCode()
		return res
	}
	if err != nil {
		t.Fatalf("run %v: %v\n%s", cmd.Args, err, errb.String())
	}
	return res
}

// nodeShell runs argv on the node as the login user (via `limactl shell node -- argv`).
func nodeShell(t *testing.T, name string, argv ...string) result {
	t.Helper()
	full := append([]string{"shell", name, "--"}, argv...)
	return runCmd(t, exec.Command("limactl", full...), nil)
}

// nodeSudo runs argv on the node as root.
func nodeSudo(t *testing.T, name string, argv ...string) result {
	t.Helper()
	return nodeShell(t, name, append([]string{"sudo"}, argv...)...)
}

// nodeSudoStdin runs argv on the node as root, feeding stdin (e.g. `sudo tee <path>`).
func nodeSudoStdin(t *testing.T, name string, stdin []byte, argv ...string) result {
	t.Helper()
	full := append([]string{"shell", name, "--", "sudo"}, argv...)
	return runCmd(t, exec.Command("limactl", full...), stdin)
}

// host runs the host-built rucher with the given working directory.
func host(t *testing.T, dir string, argv ...string) result {
	t.Helper()
	build(t)
	cmd := exec.Command(hostBin, argv...)
	cmd.Dir = dir
	return runCmd(t, cmd, nil)
}

// rucherNode runs `sudo rucher <argv>` on the node.
func rucherNode(t *testing.T, name string, argv ...string) result {
	t.Helper()
	return nodeSudo(t, name, append([]string{"rucher"}, argv...)...)
}

// --- cadre cleanup -------------------------------------------------------

// cleanupCadre purges a cadre on a node; safe to call for a cadre that never existed.
func cleanupCadre(t *testing.T, name string, nodes ...string) {
	t.Helper()
	for _, n := range nodes {
		rucherNode(t, n, "node", "cadre", "rm", name, "--purge")
	}
}

// newCadre lays out a cadre on the host under a fresh temp parent (with the cadre in a
// `<name>/` subdir) and returns that parent. It is only host-side staging; nodeApply
// copies it onto the node before applying.
func newCadre(t *testing.T, name string, files map[string]string) string {
	t.Helper()
	parent := homeTemp(t, "cadre-")
	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir cadre: %v", err)
	}
	for fn, body := range files {
		if err := os.WriteFile(filepath.Join(dir, fn), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", fn, err)
		}
	}
	return parent
}

// stageCadre copies a host-built cadre parent dir onto the node and returns the
// node-local path. Files reach the node the way they do in a real deployment (a real
// local directory), so the suite does not read cadre files over the Lima home mount.
func stageCadre(t *testing.T, node, hostParent string) string {
	t.Helper()
	// A flat dst directly under /tmp (created fresh): copy -r then makes it a copy of
	// hostParent's contents (dst/<name>/…).
	dst := "/tmp/rucher-it-" + filepath.Base(hostParent)
	nodeShell(t, node, "rm", "-rf", dst)
	if out, err := exec.Command("limactl", "copy", "--recursive", hostParent, node+":"+dst).CombinedOutput(); err != nil {
		t.Fatalf("stage cadre on %s: %v\n%s", node, err, out)
	}
	return dst
}

// nodeApply stages a host-built cadre onto the node and applies it from there.
func nodeApply(t *testing.T, node, hostParent, name string) result {
	t.Helper()
	return rucherNode(t, node, "node", "cadre", "apply", "--dir", stageCadre(t, node, hostParent), name)
}

// --- shared git store (visible to every node over the read-only virtiofs mount) ---

// homeTemp makes a temp dir under $HOME so the guests can read it at the same
// absolute path they see the host home mounted at (virtiofs, read-only). Removed
// when the test ends.
func homeTemp(t *testing.T, prefix string) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home: %v", err)
	}
	base := filepath.Join(home, ".cache", "rucher-integration")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatalf("mkdir temp base: %v", err)
	}
	dir, err := os.MkdirTemp(base, prefix)
	if err != nil {
		t.Fatalf("mktemp %s: %v", prefix, err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// newStore creates a git repo the guests can clone from a local path. Returns the store path.
func newStore(t *testing.T) string {
	t.Helper()
	dir := homeTemp(t, "store-")
	git(t, dir, "init", "-b", "main")
	git(t, dir, "config", "user.email", "it@rucher.test")
	git(t, dir, "config", "user.name", "rucher-it")
	return dir
}

// git runs a git command in dir and fails the test on error.
func git(t *testing.T, dir string, argv ...string) {
	t.Helper()
	cmd := exec.Command("git", argv...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", argv, err, out)
	}
}

// commitStore stages and commits everything in the store.
func commitStore(t *testing.T, store, msg string) {
	t.Helper()
	git(t, store, "add", "-A")
	git(t, store, "commit", "-q", "-m", msg)
}

// writeStoreFile writes a file (creating parent dirs) inside the store.
func writeStoreFile(t *testing.T, store, rel, body string) {
	t.Helper()
	p := filepath.Join(store, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(p), err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

// writeAgentConfig installs /etc/rucher/agent.yml on a node for a git store.
func writeAgentConfig(t *testing.T, node, nodeID, storeURL string) {
	t.Helper()
	body := fmt.Sprintf("node: %s\nstore:\n  kind: git\n  url: %s\n  branch: main\n", nodeID, storeURL)
	nodeSudo(t, node, "mkdir", "-p", "/etc/rucher")
	if r := nodeSudoStdin(t, node, []byte(body), "tee", "/etc/rucher/agent.yml"); r.code != 0 {
		t.Fatalf("write agent.yml on %s: %s", node, r.stderr)
	}
}

// systemdPath is where a cadre's units/support files land on the node.
func systemdPath(name string) string {
	return "/var/lib/rucher/cadres/" + name + "/.config/containers/systemd"
}

// nodeKeyInit ensures the node's age key exists and returns its recipient. The
// agent refuses to run without it, even for cadres that ship no secrets.
func nodeKeyInit(t *testing.T, node string) string {
	t.Helper()
	r := rucherNode(t, node, "node", "key", "init")
	if r.code != 0 {
		t.Fatalf("node key init on %s: %s", node, r.stderr)
	}
	return r.out()
}

// agentRun performs one GitOps pass on the node. The caller inspects the result:
// a non-zero code means one or more cadres failed to apply.
func agentRun(t *testing.T, node string) result {
	t.Helper()
	return rucherNode(t, node, "node", "agent", "run")
}

// resetAgentCache clears the node's store checkout. The agent caches the checkout at
// a fixed path and pulls from the cached repo's origin, so pointing it at a different
// store URL without clearing the cache would keep reconciling the old store. Tests
// use a fresh store each, so they must start from a clean cache.
func resetAgentCache(t *testing.T, node string) {
	t.Helper()
	nodeSudo(t, node, "rm", "-rf", "/var/lib/rucher/store")
}

// prepareGitOps clears the store cache, ensures the node key, and writes the agent
// config pointing at the store's git:// URL for each node.
func prepareGitOps(t *testing.T, store string, nodes ...string) {
	t.Helper()
	if storeErr != nil {
		t.Skipf("store server unavailable: %v", storeErr)
	}
	verifyStoreReachable(t, nodes[0])
	for _, n := range nodes {
		resetAgentCache(t, n)
		nodeKeyInit(t, n)
		writeAgentConfig(t, n, n, gitURL(store))
	}
}

// verifyStoreReachable confirms (once) that a guest can reach the host git daemon
// over the Lima gateway. If not, it fails with the daemon's own log so the cause
// (bind failure, firewall) is visible instead of an opaque per-node dial error.
func verifyStoreReachable(t *testing.T, node string) {
	t.Helper()
	reachOnce.Do(func() {
		r := nodeShell(t, node, "nc", "-z", "-w", "3", "host.lima.internal", storePort)
		if r.code != 0 {
			self := "?"
			if resp, err := http.Get("http://127.0.0.1:" + storePort + "/"); err != nil {
				self = "err: " + err.Error()
			} else {
				resp.Body.Close()
				self = fmt.Sprintf("HTTP %d", resp.StatusCode)
			}
			reachErr = fmt.Errorf("guest %s cannot reach host store server at host.lima.internal:%s\n"+
				"host self-GET 127.0.0.1:%s = %s (serveErr=%v)", node, storePort, storePort, self, serveErr)
		}
	})
	if reachErr != nil {
		t.Fatal(reachErr)
	}
}
