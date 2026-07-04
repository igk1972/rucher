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
	return 0
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout))
}
