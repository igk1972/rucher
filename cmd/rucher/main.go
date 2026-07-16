// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

func usage() string {
	return `rucher <node|ops> ...

node — on the Linux node (runuser/systemctl/podman):
  node apply [--dir DIR]                        reconcile all cadres under --dir
  node cadre new <name>
  node cadre apply [--dir DIR] <name...>        reconcile the named cadre(s)
  node cadre status [name...]
  node cadre logs <name> <unit>
  node cadre rm <name> [--purge]
  node cadre recipient <name>
  node key init | show
  node agent run | install [--config PATH]

ops — from the operator machine:
  ops init [--dir DIR] <name>                  scaffold a cadre directory (manifest + example unit)
  ops validate [--dir DIR] [name...]           check cadre manifests + unit files (no node)
  ops plan [--dir DIR] [name...]
  ops nodes [--dir DIR] status [--live] [--json] [--concurrency N] [node...]
  ops nodes [--dir DIR] join <node> --address <addr> [--json]
  ops nodes [--dir DIR] deploy [--version TAG | --binary PATH] [--store-url URL ...] [--concurrency N] [node...]
  ops key seal <name> --to <recipient> [--to <recipient> ...]
  ops secrets encrypt [--to <rcpt>... | --cadre <name> --seal-to <node-rcpt>...] [--in F] [--out F]
`
}

// parseDir pulls an optional `--dir <value>` out of args wherever it appears
// (default ./cadres); the remaining positional args are cadre names.
func parseDir(args []string) (dir string, names []string, err error) {
	dir = "./cadres"
	for i := 0; i < len(args); i++ {
		if args[i] == "--dir" {
			if i+1 >= len(args) {
				return "", nil, fmt.Errorf("--dir requires a value")
			}
			dir = args[i+1]
			i++
			continue
		}
		// Reject an unknown flag rather than treating it as a cadre name — otherwise a
		// typo like `--drii` silently becomes a (non-existent) cadre selector.
		if strings.HasPrefix(args[i], "-") {
			return "", nil, fmt.Errorf("unknown flag %q", args[i])
		}
		names = append(names, args[i])
	}
	return dir, names, nil
}

// parseRm extracts a `--purge` flag from anywhere in args; the single remaining
// non-flag argument is the cadre name.
func parseRm(args []string) (name string, purge bool, err error) {
	for _, a := range args {
		if a == "--purge" {
			purge = true
			continue
		}
		// An unknown flag must be an error, not a cadre name: `rm --force web` should not
		// silently "remove" a cadre literally named --force and exit 0.
		if strings.HasPrefix(a, "-") {
			return "", false, fmt.Errorf("unknown flag %q", a)
		}
		if name != "" {
			return "", false, fmt.Errorf("rm takes a single cadre name")
		}
		name = a
	}
	if name == "" {
		return "", false, fmt.Errorf("rm requires a cadre name")
	}
	return name, purge, nil
}

// parseAgentConfig pulls the optional `--config <path>` that may follow run/install.
// Per docs/cli.md it must come first and be the only argument, so a bare `--config`
// (no value) or any stray token is a usage error rather than being silently ignored —
// which would fall back to the default config path and run against the wrong file.
func parseAgentConfig(args []string) (string, error) {
	configPath := "/etc/rucher/agent.yml"
	if len(args) == 0 {
		return configPath, nil
	}
	if args[0] != "--config" {
		if strings.HasPrefix(args[0], "-") {
			return "", fmt.Errorf("unknown flag %q", args[0])
		}
		return "", fmt.Errorf("unexpected argument %q", args[0])
	}
	if len(args) < 2 || args[1] == "" {
		return "", fmt.Errorf("--config needs a value")
	}
	if len(args) > 2 {
		return "", fmt.Errorf("unexpected argument %q", args[2])
	}
	return args[1], nil
}

// run is the testable entry point; it returns a process exit code. The command
// surface is split by execution side: `node` acts on the local Linux host,
// `ops` runs on the operator machine (cross-platform).
func run(args []string, stdout io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stdout, usage())
		return 2
	}
	switch args[0] {
	case "node":
		return runNode(args[1:], stdout)
	case "ops":
		return runOps(args[1:], stdout)
	default:
		fmt.Fprintf(stdout, "unknown command: %s\n\n%s", args[0], usage())
		return 2
	}
}

// runNode dispatches the node-side tree (everything that shells out to the local
// host's systemd/podman via runuser).
func runNode(args []string, stdout io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stdout, usage())
		return 2
	}
	switch args[0] {
	case "apply":
		// `node apply` reconciles the whole node (all cadres); a specific
		// cadre is `node cadre apply <name>`, so positional names are rejected here.
		dir, names, err := parseDir(args[1:])
		if err != nil {
			fmt.Fprintln(stdout, "error:", err)
			return 2
		}
		if len(names) > 0 {
			fmt.Fprintln(stdout, "error: `node apply` reconciles all cadres; use `node cadre apply <name>` for one")
			return 2
		}
		return cmdApply(dir, nil, stdout)
	case "cadre":
		return runNodeCadre(args[1:], stdout)
	case "key":
		if len(args) != 2 {
			fmt.Fprintln(stdout, "usage: node key init|show")
			return 2
		}
		switch args[1] {
		case "init":
			return cmdNodeInit(stdout)
		case "show":
			return cmdNodeRecipient(stdout)
		default:
			fmt.Fprintf(stdout, "unknown node key subcommand: %s\n", args[1])
			return 2
		}
	case "agent":
		if len(args) < 2 {
			fmt.Fprintln(stdout, "usage: node agent run|install [--config PATH]")
			return 2
		}
		// Validate the subcommand before parsing flags so `node agent foo --config`
		// reports the unknown subcommand rather than a --config complaint.
		if args[1] != "run" && args[1] != "install" {
			fmt.Fprintf(stdout, "unknown node agent subcommand: %s\n", args[1])
			return 2
		}
		configPath, err := parseAgentConfig(args[2:])
		if err != nil {
			fmt.Fprintf(stdout, "error: %v\n\nusage: node agent run|install [--config PATH]\n", err)
			return 2
		}
		if args[1] == "install" {
			return cmdAgentInstall(configPath, stdout)
		}
		return cmdAgentRun(configPath, stdout)
	default:
		fmt.Fprintf(stdout, "unknown node subcommand: %s\n\n%s", args[0], usage())
		return 2
	}
}

// runNodeCadre dispatches per-cadre operations (`node cadre <verb> ...`).
func runNodeCadre(args []string, stdout io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stdout, "usage: node cadre <new|apply|status|logs|rm|recipient> ...")
		return 2
	}
	switch args[0] {
	case "new":
		if len(args) != 2 {
			fmt.Fprintln(stdout, "usage: node cadre new <name>")
			return 2
		}
		return cmdNew(args[1], stdout)
	case "apply":
		dir, names, err := parseDir(args[1:])
		if err != nil {
			fmt.Fprintln(stdout, "error:", err)
			return 2
		}
		if len(names) == 0 {
			fmt.Fprintln(stdout, "error: `node cadre apply` needs a cadre name; use `node apply` for all")
			return 2
		}
		return cmdApply(dir, names, stdout)
	case "status":
		// `node cadre status` takes only cadre names; a flag-looking token is a typo
		// (names never start with "-"), so reject it rather than silently print nothing.
		for _, a := range args[1:] {
			if strings.HasPrefix(a, "-") {
				fmt.Fprintf(stdout, "error: unknown flag %q\n", a)
				return 2
			}
		}
		return cmdStatus(args[1:], stdout)
	case "logs":
		if len(args) != 3 {
			fmt.Fprintln(stdout, "usage: node cadre logs <name> <unit>")
			return 2
		}
		return cmdLogs(args[1], args[2], stdout)
	case "rm":
		name, purge, err := parseRm(args[1:])
		if err != nil {
			fmt.Fprintf(stdout, "error: %v\n\nusage: node cadre rm <name> [--purge]\n", err)
			return 2
		}
		return cmdRm(name, purge, stdout)
	case "recipient":
		if len(args) != 2 {
			fmt.Fprintln(stdout, "usage: node cadre recipient <name>")
			return 2
		}
		return cmdAgeRecipient(args[1], stdout)
	default:
		fmt.Fprintf(stdout, "unknown node cadre subcommand: %s\n", args[0])
		return 2
	}
}

// runOps dispatches the operator-side tree (cross-platform: local files, crypto,
// and the SSH nodes plane).
func runOps(args []string, stdout io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stdout, usage())
		return 2
	}
	switch args[0] {
	case "init":
		dir, names, err := parseDir(args[1:])
		if err != nil {
			fmt.Fprintln(stdout, "error:", err)
			return 2
		}
		if len(names) != 1 {
			fmt.Fprintln(stdout, "usage: ops init [--dir DIR] <name>")
			return 2
		}
		return cmdInit(dir, names[0], stdout)
	case "validate":
		dir, names, err := parseDir(args[1:])
		if err != nil {
			fmt.Fprintln(stdout, "error:", err)
			return 2
		}
		return cmdValidate(dir, names, stdout)
	case "plan":
		dir, names, err := parseDir(args[1:])
		if err != nil {
			fmt.Fprintln(stdout, "error:", err)
			return 2
		}
		return cmdPlan(dir, names, stdout)
	case "key":
		if len(args) < 2 || args[1] != "seal" {
			fmt.Fprintln(stdout, "usage: ops key seal <name> --to <recipient> [--to <recipient> ...]")
			return 2
		}
		return cmdKeygen(args[2:], stdout)
	case "nodes":
		return runOpsNodes(args[1:], stdout)
	case "secrets":
		return runOpsSecrets(args[1:], stdout)
	default:
		fmt.Fprintf(stdout, "unknown ops subcommand: %s\n\n%s", args[0], usage())
		return 2
	}
}

// runOpsSecrets dispatches `ops secrets encrypt` — in-process SOPS+age
// encryption of a plaintext YAML map read from stdin.
func runOpsSecrets(args []string, stdout io.Writer) int {
	if len(args) == 0 || args[0] != "encrypt" {
		fmt.Fprintln(stdout, "usage: ops secrets encrypt --to <recipient>... | --cadre <name> --seal-to <node-recipient>...")
		return 2
	}
	return cmdSecretsEncrypt(args[1:], os.Stdin, stdout)
}

// runOpsNodes dispatches the ops nodes plane (`ops nodes [--dir DIR] <status|join>`).
// --dir is accepted only before the subcommand, matching the other flag-first commands.
func runOpsNodes(args []string, stdout io.Writer) int {
	nodesDir := "./nodes"
	rest := args
	if len(rest) >= 2 && rest[0] == "--dir" {
		nodesDir, rest = rest[1], rest[2:]
	}
	if len(rest) == 0 {
		fmt.Fprintln(stdout, "usage: ops nodes [--dir DIR] status|join ...")
		return 2
	}
	switch rest[0] {
	case "status":
		live, jsonOut := false, false
		concurrency := 8 // status is light (1-2 cats per node); a higher default is fine
		var names []string
		sargs := rest[1:]
		for i := 0; i < len(sargs); i++ {
			switch a := sargs[i]; a {
			case "--live":
				live = true
			case "--json":
				jsonOut = true
			case "--concurrency":
				if i+1 >= len(sargs) {
					fmt.Fprintln(stdout, "error: --concurrency needs a value")
					return 2
				}
				n, err := strconv.Atoi(sargs[i+1])
				if err != nil || n < 1 {
					fmt.Fprintln(stdout, "error: --concurrency must be a positive integer")
					return 2
				}
				concurrency, i = n, i+1
			default:
				// A node name never starts with "-" (names match [a-z0-9][a-z0-9-]*),
				// so a flag-looking token is a typo (e.g. --llive for --live); reject it
				// instead of treating it as a phantom node to SSH into.
				if strings.HasPrefix(a, "-") {
					fmt.Fprintf(stdout, "error: unknown flag %q\n", a)
					return 2
				}
				names = append(names, a)
			}
		}
		return cmdNodesStatus(nodesDir, names, live, jsonOut, concurrency, stdout)
	case "join":
		return cmdNetJoin(nodesDir, rest[1:], stdout)
	case "deploy":
		return cmdNodesDeploy(nodesDir, rest[1:], stdout)
	default:
		fmt.Fprintf(stdout, "unknown ops nodes subcommand: %s\n", rest[0])
		return 2
	}
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout))
}
