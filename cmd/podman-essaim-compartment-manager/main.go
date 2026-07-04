package main

import (
	"fmt"
	"io"
	"os"
	"slices"
)

func usage() string {
	return "podman-essaim-compartment-manager <command> [args]\n" +
		"commands: new plan apply status rm logs age\n"
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
		dir := "./compartments"
		rest := args[1:]
		if len(rest) >= 2 && rest[0] == "--dir" {
			dir = rest[1]
			rest = rest[2:]
		}
		return cmdPlan(dir, rest, stdout)
	case "apply":
		dir := "./compartments"
		rest := args[1:]
		if len(rest) >= 2 && rest[0] == "--dir" {
			dir = rest[1]
			rest = rest[2:]
		}
		return cmdApply(dir, rest, stdout)
	case "age":
		if len(args) >= 3 && args[1] == "recipient" {
			return cmdAgeRecipient(args[2], stdout)
		}
		fmt.Fprint(stdout, usage())
		return 2
	case "rm":
		if len(args) < 2 {
			fmt.Fprint(stdout, usage())
			return 2
		}
		purge := slices.Contains(args[2:], "--purge")
		return cmdRm(args[1], purge, stdout)
	default:
		fmt.Fprintf(stdout, "unknown or not-yet-implemented command: %s\n\n%s", args[0], usage())
		return 2
	}
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout))
}
