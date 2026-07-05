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

// parseKeygen collects the compartment name and every repeatable --to recipient, so
// the identity can be sealed to all target nodes at once.
func parseKeygen(args []string) (name string, recipients []string, err error) {
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--to":
			if i+1 >= len(args) {
				return "", nil, fmt.Errorf("--to needs a recipient")
			}
			// A recipient repeated across --to flags should be sealed to once.
			if r := args[i+1]; !seen[r] {
				seen[r] = true
				recipients = append(recipients, r)
			}
			i++
		default:
			if name != "" {
				return "", nil, fmt.Errorf("unexpected argument: %q", args[i])
			}
			name = args[i]
		}
	}
	if name == "" || len(recipients) == 0 {
		return "", nil, fmt.Errorf("usage: keygen <name> --to <node-recipient> [--to <node-recipient> ...]")
	}
	return name, recipients, nil
}

// cmdKeygen generates a compartment keypair, seals its identity to every node recipient,
// writes identity.age to ./compartments/<name>/, and prints the compartment recipient.
func cmdKeygen(args []string, out io.Writer) int {
	name, recipients, err := parseKeygen(args)
	if err != nil {
		fmt.Fprintln(out, "error:", err)
		return 2
	}
	id, rcpt, err := age.GenerateIdentity()
	if err != nil {
		fmt.Fprintln(out, "error:", err)
		return 1
	}
	sealed, err := age.SealTo(recipients, []byte(id))
	if err != nil {
		fmt.Fprintln(out, "error:", err)
		return 1
	}
	dir := "compartments/" + name
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintln(out, "error:", err)
		return 1
	}
	if err := os.WriteFile(dir+"/identity.age", sealed, 0o600); err != nil {
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
	var st agent.Status
	var runErr error
	switch cfg.Store.Kind {
	case "git":
		st, runErr = agent.Run(context.Background(), host.NewExec(), store.Git{
			URL: cfg.Store.URL, Branch: cfg.Store.Branch, CachePath: storeCachePath,
			SSHKey: cfg.Store.SSHKey, Token: cfg.Store.Token,
			User: cfg.Store.User, InsecureHostKey: cfg.Store.InsecureHostKey,
		}, nodeID, nodeIdentity)
	case "s3":
		st, runErr = agent.Run(context.Background(), host.NewExec(), store.S3{
			Endpoint: cfg.Store.Endpoint, Bucket: cfg.Store.Bucket, Prefix: cfg.Store.Prefix,
			AccessKey: cfg.Store.AccessKey, SecretKey: cfg.Store.SecretKey,
			UseSSL: cfg.Store.UseSSL, Region: cfg.Store.Region, CachePath: storeCachePath,
		}, nodeID, nodeIdentity)
	default:
		fmt.Fprintln(out, "error: unsupported store kind:", cfg.Store.Kind)
		return 1
	}
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

// agentTimerUnit returns the systemd timer unit body, firing the agent every interval
// (default 30s when interval is empty).
func agentTimerUnit(interval string) string {
	if interval == "" {
		interval = "30s"
	}
	return "[Unit]\nDescription=run the podman-essaim GitOps agent periodically\n\n" +
		"[Timer]\nOnBootSec=30s\nOnUnitActiveSec=" + interval + "\n\n[Install]\nWantedBy=timers.target\n"
}

// cmdAgentInstall writes the systemd oneshot service + timer that run `agent run`.
func cmdAgentInstall(configPath string, out io.Writer) int {
	cfg, err := agentcfg.Load(configPath)
	if err != nil {
		fmt.Fprintln(out, "error:", err)
		return 1
	}
	r := host.NewExec()
	service := "[Unit]\nDescription=podman-essaim GitOps agent (one pass)\n\n" +
		"[Service]\nType=oneshot\nExecStart=/usr/local/bin/pecm agent run --config " + configPath + "\n"
	timer := agentTimerUnit(cfg.Interval)
	for path, body := range map[string]string{
		"/etc/systemd/system/podman-essaim-agent.service": service,
		"/etc/systemd/system/podman-essaim-agent.timer":   timer,
	} {
		if _, err := r.Root([]string{"tee", path}, []byte(body)); err != nil {
			fmt.Fprintln(out, "error:", err)
			return 1
		}
	}
	if res, err := r.Root([]string{"systemctl", "daemon-reload"}, nil); err != nil || res.Code != 0 {
		fmt.Fprintln(out, "error: systemctl daemon-reload:", err, res.Stderr)
		return 1
	}
	res, err := r.Root([]string{"systemctl", "enable", "--now", "podman-essaim-agent.timer"}, nil)
	if err != nil || res.Code != 0 {
		fmt.Fprintln(out, "error: enable podman-essaim-agent.timer:", err, res.Stderr)
		return 1
	}
	fmt.Fprintln(out, "installed podman-essaim-agent.timer")
	return 0
}
