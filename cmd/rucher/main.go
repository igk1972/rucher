package main

import (
	"fmt"
	"io"
	"os"
)

func usage() string {
	return `rucher <node|ops> ...

node — on the Linux node (runuser/systemctl/podman):
  node apply [--dir DIR]                        reconcile all compartments under --dir
  node cadre new <name>
  node cadre apply [--dir DIR] <name...>        reconcile the named compartment(s)
  node cadre status [name...]
  node cadre logs <name> <unit>
  node cadre rm <name> [--purge]
  node cadre recipient <name>
  node key init | show
  node agent run | install [--config PATH]

ops — from the operator machine:
  ops plan [--dir DIR] [name...]
  ops ruches [--nodes DIR] status [--live] [--json] [node...]
  ops ruches [--nodes DIR] join <node> --address <addr> [--json]
  ops key seal <name> --to <recipient> [--to <recipient> ...]
`
}

// parseDir pulls an optional `--dir <value>` out of args wherever it appears
// (default ./compartments); the remaining positional args are compartment names.
func parseDir(args []string) (dir string, names []string, err error) {
	dir = "./compartments"
	for i := 0; i < len(args); i++ {
		if args[i] == "--dir" {
			if i+1 >= len(args) {
				return "", nil, fmt.Errorf("--dir requires a value")
			}
			dir = args[i+1]
			i++
			continue
		}
		names = append(names, args[i])
	}
	return dir, names, nil
}

// parseRm extracts a `--purge` flag from anywhere in args; the single remaining
// non-flag argument is the compartment name.
func parseRm(args []string) (name string, purge bool, err error) {
	for _, a := range args {
		if a == "--purge" {
			purge = true
			continue
		}
		if name != "" {
			return "", false, fmt.Errorf("rm takes a single compartment name")
		}
		name = a
	}
	if name == "" {
		return "", false, fmt.Errorf("rm requires a compartment name")
	}
	return name, purge, nil
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
		// `node apply` reconciles the whole node (all compartments); a specific
		// compartment is `node cadre apply <name>`, so positional names are rejected here.
		dir, names, err := parseDir(args[1:])
		if err != nil {
			fmt.Fprintln(stdout, "error:", err)
			return 2
		}
		if len(names) > 0 {
			fmt.Fprintln(stdout, "error: `node apply` reconciles all compartments; use `node cadre apply <name>` for one")
			return 2
		}
		return cmdApply(dir, nil, stdout)
	case "cadre":
		return runNodeCadre(args[1:], stdout)
	case "key":
		if len(args) < 2 {
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
		configPath := "/etc/rucher/agent.yml"
		rest := args[2:]
		if len(rest) >= 2 && rest[0] == "--config" {
			configPath = rest[1]
		}
		switch args[1] {
		case "run":
			return cmdAgentRun(configPath, stdout)
		case "install":
			return cmdAgentInstall(configPath, stdout)
		default:
			fmt.Fprintf(stdout, "unknown node agent subcommand: %s\n", args[1])
			return 2
		}
	default:
		fmt.Fprintf(stdout, "unknown node subcommand: %s\n\n%s", args[0], usage())
		return 2
	}
}

// runNodeCadre dispatches per-compartment operations (`node cadre <verb> ...`).
func runNodeCadre(args []string, stdout io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stdout, "usage: node cadre <new|apply|status|logs|rm|recipient> ...")
		return 2
	}
	switch args[0] {
	case "new":
		if len(args) < 2 {
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
			fmt.Fprintln(stdout, "error: `node cadre apply` needs a compartment name; use `node apply` for all")
			return 2
		}
		return cmdApply(dir, names, stdout)
	case "status":
		return cmdStatus(args[1:], stdout)
	case "logs":
		if len(args) < 3 {
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
		if len(args) < 2 {
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
// and the SSH fleet plane).
func runOps(args []string, stdout io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stdout, usage())
		return 2
	}
	switch args[0] {
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
	case "ruches":
		return runOpsRuches(args[1:], stdout)
	default:
		fmt.Fprintf(stdout, "unknown ops subcommand: %s\n\n%s", args[0], usage())
		return 2
	}
}

// runOpsRuches dispatches the fleet plane (`ops ruches [--nodes DIR] <status|join>`).
// --nodes is accepted only before the subcommand, matching the other flag-first commands.
func runOpsRuches(args []string, stdout io.Writer) int {
	nodesDir := "./nodes"
	rest := args
	if len(rest) >= 2 && rest[0] == "--nodes" {
		nodesDir, rest = rest[1], rest[2:]
	}
	if len(rest) == 0 {
		fmt.Fprintln(stdout, "usage: ops ruches [--nodes DIR] status|join ...")
		return 2
	}
	switch rest[0] {
	case "status":
		live, jsonOut := false, false
		var names []string
		for _, a := range rest[1:] {
			switch a {
			case "--live":
				live = true
			case "--json":
				jsonOut = true
			default:
				names = append(names, a)
			}
		}
		return cmdNodesStatus(nodesDir, names, live, jsonOut, stdout)
	case "join":
		return cmdNetJoin(nodesDir, rest[1:], stdout)
	default:
		fmt.Fprintf(stdout, "unknown ops ruches subcommand: %s\n", rest[0])
		return 2
	}
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout))
}
