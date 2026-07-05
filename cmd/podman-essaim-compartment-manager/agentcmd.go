package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"podman-essaim-compartment-manager/internal/age"
	"podman-essaim-compartment-manager/internal/agent"
	"podman-essaim-compartment-manager/internal/agentcfg"
	"podman-essaim-compartment-manager/internal/host"
	"podman-essaim-compartment-manager/internal/node"
	"podman-essaim-compartment-manager/internal/store"
)

const agentStatusPath = "/var/lib/podman-essaim/agent-status.json"
const storeCachePath = "/var/lib/podman-essaim/store"

func parseKeygen(args []string) (name, recipient string, err error) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--to":
			if i+1 >= len(args) {
				return "", "", fmt.Errorf("--to needs a recipient")
			}
			recipient = args[i+1]
			i++
		default:
			if name == "" {
				name = args[i]
			}
		}
	}
	if name == "" || recipient == "" {
		return "", "", fmt.Errorf("usage: keygen <name> --to <node-recipient>")
	}
	return name, recipient, nil
}

// cmdKeygen generates a compartment keypair, seals its identity to the node recipient,
// writes identity.age to ./compartments/<name>/, and prints the compartment recipient.
func cmdKeygen(args []string, out io.Writer) int {
	name, to, err := parseKeygen(args)
	if err != nil {
		fmt.Fprintln(out, "error:", err)
		return 2
	}
	id, rcpt, err := age.GenerateIdentity()
	if err != nil {
		fmt.Fprintln(out, "error:", err)
		return 1
	}
	sealed, err := age.Seal(to, []byte(id))
	if err != nil {
		fmt.Fprintln(out, "error:", err)
		return 1
	}
	dir := "compartments/" + name
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintln(out, "error:", err)
		return 1
	}
	if err := os.WriteFile(dir+"/identity.age", sealed, 0o644); err != nil {
		fmt.Fprintln(out, "error:", err)
		return 1
	}
	fmt.Fprintln(out, rcpt)
	return 0
}

func cmdNodeInit(out io.Writer) int {
	rcpt, err := node.EnsureIdentity(node.IdentityPath)
	if err != nil {
		fmt.Fprintln(out, "error:", err)
		return 1
	}
	fmt.Fprintln(out, rcpt)
	return 0
}

func cmdNodeRecipient(out io.Writer) int {
	rcpt, err := node.Recipient(node.IdentityPath)
	if err != nil {
		fmt.Fprintln(out, "error:", err)
		return 1
	}
	fmt.Fprintln(out, rcpt)
	return 0
}

func cmdAgentRun(configPath string, out io.Writer) int {
	cfg, err := agentcfg.Load(configPath)
	if err != nil {
		fmt.Fprintln(out, "error:", err)
		return 1
	}
	nodeID, err := cfg.NodeID()
	if err != nil {
		fmt.Fprintln(out, "error:", err)
		return 1
	}
	nodeIdentity, err := node.Identity(node.IdentityPath)
	if err != nil {
		fmt.Fprintln(out, "error: node key missing (run `pecm node init`):", err)
		return 1
	}
	st, runErr := agent.Run(context.Background(), host.NewExec(), store.Git{
		URL: cfg.Store.URL, Branch: cfg.Store.Branch, CachePath: storeCachePath,
		SSHKey: cfg.Store.SSHKey, Token: cfg.Store.Token,
	}, nodeID, nodeIdentity)
	if werr := agent.WriteStatus(agentStatusPath, st); werr != nil {
		fmt.Fprintln(out, "warning: write status:", werr)
	}
	fmt.Fprintf(out, "revision %s: applied=%d removed=%d\n", st.Revision, len(st.Applied), len(st.Removed))
	if runErr != nil {
		fmt.Fprintln(out, "error:", runErr)
		return 1
	}
	return 0
}

// cmdAgentInstall writes the systemd oneshot service + timer that run `agent run`.
func cmdAgentInstall(configPath string, out io.Writer) int {
	r := host.NewExec()
	service := "[Unit]\nDescription=podman-essaim GitOps agent (one pass)\n\n" +
		"[Service]\nType=oneshot\nExecStart=/usr/local/bin/pecm agent run --config " + configPath + "\n"
	timer := "[Unit]\nDescription=run the podman-essaim GitOps agent periodically\n\n" +
		"[Timer]\nOnBootSec=30s\nOnUnitActiveSec=30s\n\n[Install]\nWantedBy=timers.target\n"
	for path, body := range map[string]string{
		"/etc/systemd/system/podman-essaim-agent.service": service,
		"/etc/systemd/system/podman-essaim-agent.timer":   timer,
	} {
		if _, err := r.Root([]string{"tee", path}, []byte(body)); err != nil {
			fmt.Fprintln(out, "error:", err)
			return 1
		}
	}
	r.Root([]string{"systemctl", "daemon-reload"}, nil)
	r.Root([]string{"systemctl", "enable", "--now", "podman-essaim-agent.timer"}, nil)
	fmt.Fprintln(out, "installed podman-essaim-agent.timer")
	return 0
}
