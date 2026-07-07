// Command setup-nodes creates the Lima node swarm and provisions it (podman +
// uidmap + /dev/net/tun) for the integration suite. Self-contained: the same recipe on
// a Mac and in CI.
//
//	go run ./test/integration/cmd/setup-nodes            # create + provision + verify
//	go run ./test/integration/cmd/setup-nodes create     # just create/start the VMs
//	go run ./test/integration/cmd/setup-nodes provision  # just install the toolchain
//	go run ./test/integration/cmd/setup-nodes verify      # just print per-node state
//
// Config via env: RUCHER_IT_PREFIX (lima-essaim), RUCHER_IT_COUNT (3),
// RUCHER_IT_PODMAN (5.8.4), RUCHER_IT_TEMPLATE (template:debian),
// RUCHER_IT_CPUS/MEMORY/DISK (2 / 0.5 / 4), RUCHER_IT_NODES_DIR (<module>/../nodes).
package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var (
	prefix    = env("RUCHER_IT_PREFIX", "lima-essaim")
	count     = envInt("RUCHER_IT_COUNT", 3)
	podmanVer = env("RUCHER_IT_PODMAN", "5.8.4") // exact mgoltzsche/podman-static release
	template  = env("RUCHER_IT_TEMPLATE", "template:debian")
	cpus      = env("RUCHER_IT_CPUS", "2")
	memory    = env("RUCHER_IT_MEMORY", "0.5")
	disk      = env("RUCHER_IT_DISK", "4")
	cacheDir  = filepath.Join(home(), ".cache", "rucher-it-setup")
	nodesDir  = env("RUCHER_IT_NODES_DIR", defaultNodesDir())
)

func main() {
	cmd := "all"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "create":
		create()
	case "provision":
		provision()
	case "verify":
		verify()
	case "all":
		create()
		provision()
		verify()
	default:
		fatal("unknown command: %s (create|provision|verify|all)", cmd)
	}
}

// --- node identity ----------------------------------------------------------

func nodeNames() []string {
	out := make([]string, count)
	for i := 0; i < count; i++ {
		out[i] = fmt.Sprintf("%s-%02d", prefix, i+1)
	}
	return out
}

// --- create -----------------------------------------------------------------

func create() {
	needCmd("limactl")
	existing := map[string]bool{}
	out, _, _ := run("limactl", []string{"list", "-q"}, nil)
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if l != "" {
			existing[l] = true
		}
	}
	for _, n := range nodeNames() {
		if existing[n] {
			logf("%s exists — ensuring started", n)
			run("limactl", []string{"start", n, "--tty=false"}, nil)
			continue
		}
		logf("creating %s from %s", n, template)
		if _, errb, code := run("limactl", []string{
			"create", "--name=" + n, "--cpus=" + cpus, "--memory=" + memory,
			"--disk=" + disk, "--tty=false", template,
		}, nil); code != 0 {
			fatal("create %s: %s", n, strings.TrimSpace(errb))
		}
		if _, errb, code := run("limactl", []string{"start", n, "--tty=false"}, nil); code != 0 {
			fatal("start %s: %s", n, strings.TrimSpace(errb))
		}
	}
	ensureNodesConfig()
}

// ensureNodesConfig writes a minimal <nodesDir>/<name>/configuration.yml if absent so
// the operator-plane tests have a node dir. It never clobbers an existing file.
func ensureNodesConfig() {
	for _, n := range nodeNames() {
		f := filepath.Join(nodesDir, n, "configuration.yml")
		if _, err := os.Stat(f); err == nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			fatal("mkdir %s: %v", filepath.Dir(f), err)
		}
		if err := os.WriteFile(f, []byte("hostname: "+n+"\n"), 0o644); err != nil {
			fatal("write %s: %v", f, err)
		}
		logf("wrote %s", f)
	}
}

// --- provision --------------------------------------------------------------

// provScript ensures rootless prereqs (uidmap + /etc/subuid,subgid) and the tun device.
const provScript = `set -e
u="${SUDO_USER:-$(id -un)}"
command -v newuidmap >/dev/null 2>&1 || { apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq uidmap; }
grep -q "^$u:" /etc/subuid 2>/dev/null || echo "$u:100000:65536" >> /etc/subuid
grep -q "^$u:" /etc/subgid 2>/dev/null || echo "$u:100000:65536" >> /etc/subgid
modprobe tun 2>/dev/null || true
echo tun > /etc/modules-load.d/tun.conf
echo 'KERNEL=="tun", SUBSYSTEM=="misc", MODE="0666"' > /etc/udev/rules.d/99-rucher-tun.rules
udevadm control --reload-rules 2>/dev/null || true
chmod 0666 /dev/net/tun 2>/dev/null || true
test -c /dev/net/tun
`

func provision() {
	needCmd("limactl")
	for _, n := range nodeNames() {
		logf("provisioning %s", n)
		if err := provisionOne(n); err != nil {
			fatal("provision %s: %v", n, err)
		}
	}
}

func provisionOne(node string) error {
	archOut, code := limaShell(node, "dpkg", "--print-architecture")
	arch := strings.TrimSpace(archOut)
	if code != 0 || (arch != "arm64" && arch != "amd64") {
		return fmt.Errorf("unexpected architecture %q", arch)
	}

	// 1. podman (static bundle; crun/conmon/netavark/pasta/… included). Skip if at target.
	if ver, _ := limaShell(node, "sh", "-c", "podman --version 2>/dev/null || true"); strings.Contains(ver, " "+podmanVer) {
		logf("%s: podman %s already present", node, podmanVer)
	} else {
		tar := filepath.Join(cacheDir, fmt.Sprintf("podman-linux-%s-v%s.tar.gz", arch, podmanVer))
		url := fmt.Sprintf("https://github.com/mgoltzsche/podman-static/releases/download/v%s/podman-linux-%s.tar.gz", podmanVer, arch)
		if err := download(url, tar); err != nil {
			return err
		}
		if err := limaCopy(tar, node+":/tmp/podman-static.tar.gz"); err != nil {
			return err
		}
		if err := limaSudo(node, `cd /tmp && rm -rf podman-linux-* && tar -xzf podman-static.tar.gz && cp -r podman-linux-*/usr podman-linux-*/etc / && rm -rf podman-linux-* podman-static.tar.gz`); err != nil {
			return fmt.Errorf("podman install: %w", err)
		}
		logf("%s: podman %s installed", node, podmanVer)
	}

	// 2. rootless prereqs + tun device.
	if err := limaSudo(node, provScript); err != nil {
		return fmt.Errorf("rootless/tun prereqs: %w", err)
	}
	return nil
}

// --- verify -----------------------------------------------------------------

func verify() {
	needCmd("limactl")
	fmt.Printf("%-18s %-10s %-9s %-5s\n", "NODE", "PODMAN", "ROOTLESS", "TUN")
	for _, n := range nodeNames() {
		pv, _ := limaShell(n, "sh", "-c", `podman --version 2>/dev/null | sed "s/.*version //" || echo none`)
		rl, _ := limaShell(n, "sh", "-c", "podman info >/dev/null 2>&1 && echo ok || echo FAIL")
		tun, _ := limaShell(n, "sh", "-c", "test -c /dev/net/tun && echo ok || echo FAIL")
		fmt.Printf("%-18s %-10s %-9s %-5s\n",
			n, dflt(strings.TrimSpace(pv), "none"), strings.TrimSpace(rl),
			strings.TrimSpace(tun))
	}
}

// --- lima / process helpers -------------------------------------------------

// run executes a command, returning stdout, stderr and the exit code. A non-zero exit
// is not a fatal error (it is returned in code); a failure to spawn is fatal.
func run(name string, args []string, stdin []byte) (string, string, int) {
	cmd := exec.Command(name, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if ee, ok := err.(*exec.ExitError); ok {
		return out.String(), errb.String(), ee.ExitCode()
	}
	if err != nil {
		fatal("run %s %v: %v", name, args, err)
	}
	return out.String(), errb.String(), 0
}

func limaShell(node string, argv ...string) (string, int) {
	out, _, code := run("limactl", append([]string{"shell", node, "--"}, argv...), nil)
	return out, code
}

func limaSudo(node, script string) error {
	_, errb, code := run("limactl", []string{"shell", node, "--", "sudo", "sh", "-s"}, []byte(script))
	if code != 0 {
		return fmt.Errorf("%s", strings.TrimSpace(errb))
	}
	return nil
}

func limaCopy(local, remote string) error {
	if _, errb, code := run("limactl", []string{"copy", local, remote}, nil); code != 0 {
		return fmt.Errorf("copy %s -> %s: %s", local, remote, strings.TrimSpace(errb))
	}
	return nil
}

// download fetches url to dest (cached: skips a non-empty existing file). Go's HTTP
// client follows GitHub's redirect to objects.githubusercontent.com cleanly.
func download(url, dest string) error {
	if fi, err := os.Stat(dest); err == nil && fi.Size() > 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	logf("download %s", url)
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	tmp := dest + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dest)
}

// --- small utilities --------------------------------------------------------

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		fatal("home dir: %v", err)
	}
	return h
}

// defaultNodesDir is <module>/../nodes — the operator node-config dir the suite reads,
// mirroring the harness. Falls back to ../nodes when the module root can't be found.
func defaultNodesDir() string {
	dir, err := os.Getwd()
	if err != nil {
		return "../nodes"
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, "..", "nodes")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "../nodes"
		}
		dir = parent
	}
}

func dflt(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func needCmd(name string) {
	if _, err := exec.LookPath(name); err != nil {
		fatal("%s not found on PATH", name)
	}
}

func logf(format string, a ...any) { fmt.Fprintf(os.Stderr, "[setup-nodes] "+format+"\n", a...) }

func fatal(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "[setup-nodes] error: "+format+"\n", a...)
	os.Exit(1)
}
