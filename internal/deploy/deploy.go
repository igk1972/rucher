// SPDX-License-Identifier: AGPL-3.0-or-later

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
	"rucher/internal/parallel"
	"rucher/internal/sshresolve"
	"rucher/internal/sshx"
)

const (
	installPath = "/usr/local/bin/rucher"
	stagePath   = "/usr/local/bin/.rucher.new" // same dir as installPath so mv is atomic
	agentCfg    = "/etc/rucher/agent.yml"
	// DefaultRepo is the GitHub owner/repo whose Release assets nodes download.
	DefaultRepo = "igk1972/rucher"
	// podmanDebRepo is the GitHub owner/repo whose Release carries prebuilt podman .deb
	// (per-arch tarballs) a node installs when Podman.Source is "prebuilt".
	podmanDebRepo = "igk1972/podman-6-deb"
)

// podmanTarballURL is the prebuilt per-arch .deb tarball a node downloads; ${arch} is
// left for the shell to resolve on the node. A pinned version maps to that release tag;
// empty resolves to the newest one via GitHub's /releases/latest/download/ redirect.
func podmanTarballURL(version string) string {
	const asset = "podman6-trixie-${arch}.tar.gz"
	if version != "" {
		return fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", podmanDebRepo, version, asset)
	}
	return fmt.Sprintf("https://github.com/%s/releases/latest/download/%s", podmanDebRepo, asset)
}

// aptPodman installs the distro podman package (journald-capable) when absent.
const aptPodman = `if ! command -v podman >/dev/null 2>&1; then
  apt-get update -qq
  DEBIAN_FRONTEND=noninteractive apt-get install -y -qq -o Dpkg::Options::=--force-confold podman
fi
`

// prebuiltPodman installs podman from the per-arch .deb tarball in podmanDebRepo's
// Release (version pins a tag, empty = latest). apt resolves conmon/crun from the .deb
// deps; passt is added explicitly because the AlviStack .deb don't depend on it yet
// rootless networking needs pasta. The rootful storage.conf the .deb ship is corrected
// per-user in provision.EnsureUser.
func prebuiltPodman(version string) string {
	return `if ! command -v podman >/dev/null 2>&1; then
  arch=$(dpkg --print-architecture)
  curl -fsSL "` + podmanTarballURL(version) + `" -o /tmp/p6.tgz
  mkdir -p /tmp/p6 && tar -xzf /tmp/p6.tgz -C /tmp/p6
  apt-get update -qq
  DEBIAN_FRONTEND=noninteractive apt-get install -y -qq -o Dpkg::Options::=--force-confold /tmp/p6/*.deb passt
  rm -rf /tmp/p6 /tmp/p6.tgz
fi
`
}

// provisionScript ensures the base platform: podman (only when absent), the uidmap
// helpers, /etc/subuid+subgid, and /dev/net/tun for overlays. It is idempotent and
// Debian-oriented (apt-get/dpkg), matching node-requirements.md. source "prebuilt"
// installs the .deb tarball (version pins a release tag, empty = latest); anything else
// installs the distro apt package. Run via `sudo sh -s` with the script on stdin.
func provisionScript(source, version string) string {
	install := aptPodman
	if source == "prebuilt" {
		install = prebuiltPodman(version)
	}
	return `set -e
` + install + `command -v newuidmap >/dev/null 2>&1 || { apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq uidmap; }
touch /etc/subuid /etc/subgid
modprobe tun 2>/dev/null || true
echo tun > /etc/modules-load.d/tun.conf
printf 'KERNEL=="tun", SUBSYSTEM=="misc", MODE="0666"\n' > /etc/udev/rules.d/99-rucher-tun.rules
udevadm control --reload-rules 2>/dev/null || true
[ -e /dev/net/tun ] && chmod 0666 /dev/net/tun || true
`
}

// Options controls a deploy run.
type Options struct {
	// Binary source. If Binary is non-nil it is uploaded as-is; otherwise the
	// node downloads rucher_linux_<arch> from the GitHub Release.
	Binary  []byte
	Version string // release tag; empty => latest
	Repo    string // owner/repo; empty => DefaultRepo

	// PodmanSource selects where a node without podman gets it: "prebuilt" installs the
	// .deb tarball from podmanDebRepo; anything else (default) uses the distro apt package.
	// Overrides configuration.yml's podman.source.
	PodmanSource string
	// PodmanVersion pins the prebuilt release tag (only meaningful with PodmanSource
	// "prebuilt"); empty installs the latest. Overrides configuration.yml's podman.version.
	PodmanVersion string

	// Agent bootstrap. When Bootstrap is true, deploy writes /etc/rucher/agent.yml
	// from Store+Interval and runs `node agent install`.
	Bootstrap bool
	Store     agentcfg.StoreConfig
	Interval  string

	// Concurrency bounds how many nodes deploy in parallel; <= 0 means one worker
	// per node (see parallel.Map).
	Concurrency int
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
	// Rows come back in the order of names regardless of opts.Concurrency.
	rows := parallel.Map(names, opts.Concurrency, func(name string) Row {
		return deployOne(r, nodesDir, limaDir, name, opts)
	})
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

	// 2. Base platform (podman/uidmap/tun): idempotent — installs only what is missing.
	//    Run over stdin so the multi-line script survives sshx's join. CLI opts override
	//    the node's configuration.yml podman.{source,version}.
	source, version := cfg.Podman.Source, cfg.Podman.Version
	if opts.PodmanSource != "" {
		source = opts.PodmanSource
	}
	if opts.PodmanVersion != "" {
		version = opts.PodmanVersion
	}
	if msg, ok := runStep(r, target, []string{"sudo", "sh", "-s"}, []byte(provisionScript(source, version))); !ok {
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
