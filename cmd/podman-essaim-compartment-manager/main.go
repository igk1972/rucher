package main

import (
	"fmt"
	"io"
	"os"
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
	case "plan":
		dir := "./compartments"
		rest := args[1:]
		if len(rest) >= 2 && rest[0] == "--dir" {
			dir = rest[1]
			rest = rest[2:]
		}
		return cmdPlan(dir, rest, stdout)
	default:
		fmt.Fprintf(stdout, "unknown or not-yet-implemented command: %s\n\n%s", args[0], usage())
		return 2
	}
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout))
}
