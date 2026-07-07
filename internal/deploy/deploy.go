// Package deploy installs/updates the rucher binary on nodes over SSH and, when
// a store is configured, bootstraps the GitOps agent — all from the operator.
// It mirrors internal/nodestatus.Collect: iterate nodes, resolve each to an
// sshx.Target, and run a short sequence of remote commands.
package deploy

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"rucher/internal/agentcfg"
	"rucher/internal/nodecfg"
	"rucher/internal/sshresolve"
	"rucher/internal/sshx"
)

const (
	installPath = "/usr/local/bin/rucher"
	stagePath   = "/usr/local/bin/.rucher.new" // same dir as installPath so mv is atomic
	agentCfg    = "/etc/rucher/agent.yml"
	// DefaultRepo is the GitHub owner/repo whose Release assets nodes download.
	DefaultRepo = "igk1972/rucher"
	// podmanVersion is the mgoltzsche/podman-static release installed when a node
	// has no podman yet.
	podmanVersion = "5.8.4"
)

// provisionScript ensures the base platform: a static podman (only when absent),
// the uidmap helpers, /etc/subuid+subgid, and /dev/net/tun for overlays. It is
// idempotent and Debian-oriented (apt-get/dpkg), matching node-requirements.md.
// Run via `sudo sh -s` with the script on stdin so sshx passes it intact.
var provisionScript = `set -e
arch=$(dpkg --print-architecture)
if ! command -v podman >/dev/null 2>&1; then
  curl -fsSL "https://github.com/mgoltzsche/podman-static/releases/download/v` + podmanVersion + `/podman-linux-${arch}.tar.gz" -o /tmp/podman-static.tar.gz
  cd /tmp && tar -xzf podman-static.tar.gz && cp -r podman-linux-*/usr podman-linux-*/etc / && rm -rf podman-linux-* podman-static.tar.gz
fi
command -v newuidmap >/dev/null 2>&1 || { apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq uidmap; }
touch /etc/subuid /etc/subgid
modprobe tun 2>/dev/null || true
echo tun > /etc/modules-load.d/tun.conf
printf 'KERNEL=="tun", SUBSYSTEM=="misc", MODE="0666"\n' > /etc/udev/rules.d/99-rucher-tun.rules
udevadm control --reload-rules 2>/dev/null || true
[ -e /dev/net/tun ] && chmod 0666 /dev/net/tun || true
`

// Options controls a deploy run.
type Options struct {
	// Binary source. If Binary is non-nil it is uploaded as-is; otherwise the
	// node downloads rucher_linux_<arch> from the GitHub Release.
	Binary  []byte
	Version string // release tag; empty => latest
	Repo    string // owner/repo; empty => DefaultRepo

	// Agent bootstrap. When Bootstrap is true, deploy writes /etc/rucher/agent.yml
	// from Store+Interval and runs `node agent install`.
	Bootstrap bool
	Store     agentcfg.StoreConfig
	Interval  string
}

// Row is the per-node outcome.
type Row struct {
	Node           string   `json:"node"`
	Address        string   `json:"address"`
	Arch           string   `json:"arch,omitempty"`
	Recipient      string   `json:"recipient,omitempty"`
	AgentInstalled bool     `json:"agentInstalled"`
	OK             bool     `json:"ok"`
	Errors         []string `json:"errors,omitempty"`
}

// Run deploys to each named node (all nodes under nodesDir when names is empty).
func Run(r sshx.Runner, nodesDir, limaDir string, names []string, opts Options) ([]Row, error) {
	if len(names) == 0 {
		listed, err := nodecfg.List(nodesDir)
		if err != nil {
			return nil, err
		}
		names = listed
	}
	rows := make([]Row, 0, len(names))
	for _, name := range names {
		rows = append(rows, deployOne(r, nodesDir, limaDir, name, opts))
	}
	return rows, nil
}

func deployOne(r sshx.Runner, nodesDir, limaDir, name string, opts Options) Row {
	row := Row{Node: name}
	cfg, err := nodecfg.LoadMerged(nodesDir, name)
	if err != nil {
		return fail(row, err.Error())
	}
	row.Address = cfg.Network.Address
	target, err := sshresolve.Resolve(name, cfg, limaDir)
	if err != nil {
		return fail(row, err.Error())
	}

	// 1. Architecture (dpkg names already match Go GOARCH).
	res, err := r.Run(target, []string{"dpkg", "--print-architecture"}, nil)
	if err != nil {
		return fail(row, err.Error())
	}
	if res.Code != 0 {
		return fail(row, "dpkg --print-architecture: "+firstLine(res.Stderr))
	}
	row.Arch = strings.TrimSpace(res.Stdout)

	// 2. Base platform (podman-static/uidmap/tun): idempotent — installs only what
	//    is missing. Run over stdin so the multi-line script survives sshx's join.
	if msg, ok := runStep(r, target, []string{"sudo", "sh", "-s"}, []byte(provisionScript)); !ok {
		return fail(row, "provision base platform: "+msg)
	}

	// 3. Deliver the binary to installPath (0755) via a staged file + atomic mv.
	//    Every step is a plain argv with no shell metacharacters: sshx joins argv
	//    with spaces and hands the line to the remote shell, so redirections and
	//    `sh -c` quoting would not survive — `install`/`curl`/`mv` need neither.
	if opts.Binary != nil {
		if msg, ok := runStep(r, target, []string{"sudo", "install", "-m0755", "/dev/stdin", stagePath}, opts.Binary); !ok {
			return fail(row, "stage binary: "+msg)
		}
	} else {
		if row.Arch != "amd64" && row.Arch != "arm64" {
			return fail(row, "unsupported architecture "+row.Arch+" (no release asset)")
		}
		url := assetURL(opts, row.Arch)
		if msg, ok := runStep(r, target, []string{"sudo", "curl", "-fsSL", url, "-o", stagePath}, nil); !ok {
			return fail(row, "download binary: "+msg)
		}
		if msg, ok := runStep(r, target, []string{"sudo", "chmod", "0755", stagePath}, nil); !ok {
			return fail(row, "chmod binary: "+msg)
		}
	}
	if msg, ok := runStep(r, target, []string{"sudo", "mv", "-f", stagePath, installPath}, nil); !ok {
		return fail(row, "install binary: "+msg)
	}

	// 4. Verify it is present and executable.
	if res, err := r.Run(target, []string{"test", "-x", installPath}, nil); err != nil || res.Code != 0 {
		return fail(row, "binary not executable at "+installPath)
	}

	// 5. Node key init — prints the node's age recipient (needed to seal cadres).
	res, err = r.Run(target, []string{"sudo", installPath, "node", "key", "init"}, nil)
	if err != nil {
		return fail(row, err.Error())
	}
	if res.Code != 0 {
		return fail(row, "node key init: "+firstLine(res.Stderr))
	}
	row.Recipient = strings.TrimSpace(res.Stdout)

	// 6. Agent bootstrap (only when a store was configured).
	if opts.Bootstrap {
		body, err := renderAgentConfig(name, opts)
		if err != nil {
			return fail(row, err.Error())
		}
		// install -D creates /etc/rucher and writes the file in one step.
		if msg, ok := runStep(r, target, []string{"sudo", "install", "-D", "-m0644", "/dev/stdin", agentCfg}, body); !ok {
			return fail(row, "write agent.yml: "+msg)
		}
		res, err := r.Run(target, []string{"sudo", installPath, "node", "agent", "install"}, nil)
		if err != nil {
			return fail(row, err.Error())
		}
		if res.Code != 0 {
			return fail(row, "node agent install: "+firstLine(res.Stderr))
		}
		row.AgentInstalled = true
	}

	row.OK = true
	return row
}

// assetURL is the GitHub Release download URL for the node's arch.
func assetURL(opts Options, arch string) string {
	repo := opts.Repo
	if repo == "" {
		repo = DefaultRepo
	}
	asset := "rucher_linux_" + arch
	if opts.Version != "" {
		return fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", repo, opts.Version, asset)
	}
	return fmt.Sprintf("https://github.com/%s/releases/latest/download/%s", repo, asset)
}

// renderAgentConfig marshals /etc/rucher/agent.yml. node: is the node's dir name
// so it matches placement.yml (the operator refers to nodes by that name).
func renderAgentConfig(node string, opts Options) ([]byte, error) {
	cfg := agentcfg.Config{Node: node, Store: opts.Store, Interval: opts.Interval}
	if cfg.Store.Kind == "" {
		cfg.Store.Kind = "git"
	}
	if cfg.Store.Branch == "" && cfg.Store.Kind == "git" {
		cfg.Store.Branch = "main"
	}
	if cfg.Interval == "" {
		cfg.Interval = "30s"
	}
	return yaml.Marshal(cfg)
}

// runStep runs one remote command (optionally feeding stdin) and returns a
// short error message + ok=false on any transport error or non-zero exit.
func runStep(r sshx.Runner, t sshx.Target, cmd []string, stdin []byte) (string, bool) {
	res, err := r.Run(t, cmd, stdin)
	if err != nil {
		return err.Error(), false
	}
	if res.Code != 0 {
		return firstLine(res.Stderr), false
	}
	return "", true
}

func fail(row Row, msg string) Row {
	row.Errors = append(row.Errors, msg)
	return row
}

func firstLine(s string) string {
	if first := strings.TrimSpace(strings.SplitN(s, "\n", 2)[0]); first != "" {
		return first
	}
	return "(no output)"
}
