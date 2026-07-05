package main

import (
	"fmt"
	"io"
	"os"
)

func usage() string {
	return "podman-essaim-compartment-manager <command> [args]\n" +
		"commands: new plan apply status rm logs age node agent keygen net hosts\n"
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

// run is the testable entry point; it returns a process exit code.
func run(args []string, stdout io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stdout, usage())
		return 2
	}
	switch args[0] {
	case "new":
		if len(args) < 2 {
			fmt.Fprint(stdout, usage())
			return 2
		}
		return cmdNew(args[1], stdout)
	case "plan":
		dir, names, err := parseDir(args[1:])
		if err != nil {
			fmt.Fprintln(stdout, "error:", err)
			return 2
		}
		return cmdPlan(dir, names, stdout)
	case "apply":
		dir, names, err := parseDir(args[1:])
		if err != nil {
			fmt.Fprintln(stdout, "error:", err)
			return 2
		}
		return cmdApply(dir, names, stdout)
	case "status":
		return cmdStatus(args[1:], stdout)
	case "logs":
		if len(args) < 3 {
			fmt.Fprint(stdout, usage())
			return 2
		}
		return cmdLogs(args[1], args[2], stdout)
	case "age":
		if len(args) >= 3 && args[1] == "recipient" {
			return cmdAgeRecipient(args[2], stdout)
		}
		fmt.Fprint(stdout, usage())
		return 2
	case "rm":
		name, purge, err := parseRm(args[1:])
		if err != nil {
			fmt.Fprintf(stdout, "error: %v\n\n%s", err, usage())
			return 2
		}
		return cmdRm(name, purge, stdout)
	case "node":
		if len(args) < 2 {
			fmt.Fprintln(stdout, "usage: node init|recipient")
			return 2
		}
		switch args[1] {
		case "init":
			return cmdNodeInit(stdout)
		case "recipient":
			return cmdNodeRecipient(stdout)
		default:
			fmt.Fprintf(stdout, "unknown node subcommand: %s\n", args[1])
			return 2
		}
	case "agent":
		if len(args) < 2 {
			fmt.Fprintln(stdout, "usage: agent run|install")
			return 2
		}
		configPath := "/etc/podman-essaim/agent.yml"
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
			fmt.Fprintf(stdout, "unknown agent subcommand: %s\n", args[1])
			return 2
		}
	case "keygen":
		return cmdKeygen(args[1:], stdout)
	case "net":
		hostsDir := "./hosts"
		rest := args[1:]
		if len(rest) >= 2 && rest[0] == "--hosts" {
			hostsDir, rest = rest[1], rest[2:]
		}
		if len(rest) >= 1 && rest[0] == "join" {
			return cmdNetJoin(hostsDir, rest[1:], stdout)
		}
		fmt.Fprintln(stdout, "usage: net [--hosts DIR] join <host> ...")
		return 2
	case "hosts":
		hostsDir := "./hosts"
		rest := args[1:]
		if len(rest) >= 2 && rest[0] == "--hosts" {
			hostsDir, rest = rest[1], rest[2:]
		}
		if len(rest) >= 1 && rest[0] == "status" {
			live := false
			var names []string
			for _, a := range rest[1:] {
				if a == "--live" {
					live = true
				} else {
					names = append(names, a)
				}
			}
			return cmdHostsStatus(hostsDir, names, live, stdout)
		}
		fmt.Fprintln(stdout, "usage: hosts [--hosts DIR] status [--live] [host...]")
		return 2
	default:
		fmt.Fprintf(stdout, "unknown or not-yet-implemented command: %s\n\n%s", args[0], usage())
		return 2
	}
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout))
}
