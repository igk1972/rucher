// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"rucher/internal/agentcfg"
	"rucher/internal/deploy"
	"rucher/internal/sshx"
)

// deployFlags is the parsed `ops nodes deploy` command line.
type deployFlags struct {
	version        string
	repo           string
	binaryPath     string
	podmanPrebuilt bool
	podmanVersion  string
	interval       string
	store          agentcfg.StoreConfig
	jsonOut        bool
	concurrency    int
	names          []string
}

// parseDeploy parses the deploy flags; remaining positionals are node names.
func parseDeploy(args []string) (deployFlags, error) {
	df := deployFlags{concurrency: 4} // deploy is heavy (large uploads); default modest
	need := func(i int) (string, error) {
		if i+1 >= len(args) {
			return "", fmt.Errorf("%s needs a value", args[i])
		}
		return args[i+1], nil
	}
	for i := 0; i < len(args); i++ {
		var err error
		var v string
		switch a := args[i]; a {
		case "--version":
			v, err = need(i)
			df.version, i = v, i+1
		case "--repo":
			v, err = need(i)
			df.repo, i = v, i+1
		case "--binary":
			v, err = need(i)
			df.binaryPath, i = v, i+1
		case "--podman-prebuilt":
			df.podmanPrebuilt = true
		case "--podman-version":
			v, err = need(i)
			df.podmanVersion, i = v, i+1
		case "--interval":
			v, err = need(i)
			df.interval, i = v, i+1
		case "--store-url":
			v, err = need(i)
			df.store.URL, i = v, i+1
		case "--store-kind":
			v, err = need(i)
			df.store.Kind, i = v, i+1
		case "--store-branch":
			v, err = need(i)
			df.store.Branch, i = v, i+1
		case "--store-ssh-key":
			v, err = need(i)
			df.store.SSHKey, i = v, i+1
		case "--store-token":
			v, err = need(i)
			df.store.Token, i = v, i+1
		case "--store-endpoint":
			v, err = need(i)
			df.store.Endpoint, i = v, i+1
		case "--store-bucket":
			v, err = need(i)
			df.store.Bucket, i = v, i+1
		case "--store-prefix":
			v, err = need(i)
			df.store.Prefix, i = v, i+1
		case "--store-access-key":
			v, err = need(i)
			df.store.AccessKey, i = v, i+1
		case "--store-secret-key":
			v, err = need(i)
			df.store.SecretKey, i = v, i+1
		case "--store-region":
			v, err = need(i)
			df.store.Region, i = v, i+1
		case "--store-insecure-host-key":
			df.store.InsecureHostKey = true
		case "--store-ssl":
			df.store.UseSSL = true
		case "--concurrency":
			v, err = need(i)
			if err == nil {
				var n int
				if n, err = strconv.Atoi(v); err == nil && n < 1 {
					err = fmt.Errorf("--concurrency must be >= 1")
				}
				df.concurrency, i = n, i+1
			}
		case "--json":
			df.jsonOut = true
		default:
			// A real node name never starts with "-", so a flag-looking token is a
			// typo (e.g. a misspelled --store-* whose value would otherwise be
			// swallowed as a node name, silently dropping the credential).
			if strings.HasPrefix(a, "-") {
				return deployFlags{}, fmt.Errorf("unknown flag %q", a)
			}
			df.names = append(df.names, a)
		}
		if err != nil {
			return deployFlags{}, err
		}
	}
	if df.binaryPath != "" && df.version != "" {
		return deployFlags{}, fmt.Errorf("specify either --binary or --version, not both")
	}
	if df.podmanVersion != "" && !df.podmanPrebuilt {
		return deployFlags{}, fmt.Errorf("--podman-version requires --podman-prebuilt")
	}
	return df, nil
}

// cmdNodesDeploy installs/updates rucher on the named nodes (all under nodesDir
// if none named) and, when a store is configured, bootstraps the GitOps agent.
func cmdNodesDeploy(nodesDir string, args []string, out io.Writer) int {
	df, err := parseDeploy(args)
	if err != nil {
		fmt.Fprintln(out, "error:", err)
		return 2
	}
	source := ""
	if df.podmanPrebuilt {
		source = "prebuilt"
	}
	opts := deploy.Options{
		Version:       df.version,
		Repo:          df.repo,
		PodmanSource:  source,
		PodmanVersion: df.podmanVersion,
		Store:         df.store,
		Interval:      df.interval,
		Concurrency:   df.concurrency,
		// A store URL (git) or bucket (s3) turns on agent bootstrap.
		Bootstrap: df.store.URL != "" || df.store.Bucket != "",
	}
	if df.binaryPath != "" {
		opts.Binary, err = os.ReadFile(df.binaryPath)
		if err != nil {
			fmt.Fprintln(out, "error:", err)
			return 1
		}
	}

	// A multi-MB binary upload/download must not hit the 30s default exec timeout.
	client := sshx.NewClient(knownHostsPath(), 10*time.Second)
	client.ExecTimeout = 5 * time.Minute

	rows, err := deploy.Run(client, nodesDir, limaDir(), df.names, opts)
	if err != nil {
		fmt.Fprintln(out, "error:", err)
		return 1
	}
	if df.jsonOut {
		return renderDeployJSON(out, rows)
	}
	return renderDeployTable(out, rows)
}

func renderDeployTable(out io.Writer, rows []deploy.Row) int {
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NODE\tADDRESS\tARCH\tAGENT\tRECIPIENT\tOK")
	rc := 0
	for _, r := range rows {
		if !r.OK {
			rc = 1
		}
		agent := "-"
		if r.AgentInstalled {
			agent = "yes"
		}
		ok := "yes"
		if !r.OK {
			ok = "no"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", r.Node, r.Address, r.Arch, agent, r.Recipient, ok)
	}
	tw.Flush()
	// Print full error messages below the table so failures are legible.
	for _, r := range rows {
		for _, e := range r.Errors {
			fmt.Fprintf(out, "  %s: %s\n", r.Node, e)
		}
	}
	return rc
}

func renderDeployJSON(out io.Writer, rows []deploy.Row) int {
	rc := 0
	for _, r := range rows {
		if !r.OK {
			rc = 1
		}
	}
	if rows == nil {
		rows = []deploy.Row{}
	}
	b, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		fmt.Fprintln(out, "error:", err)
		return 1
	}
	fmt.Fprintf(out, "%s\n", b)
	return rc
}
