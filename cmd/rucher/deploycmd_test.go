// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import "testing"

func TestParseDeploy(t *testing.T) {
	df, err := parseDeploy([]string{
		"--version", "v0.1.0", "--podman-prebuilt", "--podman-version", "v6.0.1",
		"--store-url", "git@example.com:store.git",
		"--store-branch", "prod", "--interval", "1m", "--json", "web", "db",
	})
	if err != nil {
		t.Fatal(err)
	}
	if df.version != "v0.1.0" || !df.podmanPrebuilt || df.podmanVersion != "v6.0.1" || df.store.URL != "git@example.com:store.git" ||
		df.store.Branch != "prod" || df.interval != "1m" || !df.jsonOut {
		t.Fatalf("flags = %+v", df)
	}
	if len(df.names) != 2 || df.names[0] != "web" || df.names[1] != "db" {
		t.Fatalf("names = %v", df.names)
	}
}

func TestParseDeployPodmanVersionNeedsPrebuilt(t *testing.T) {
	if _, err := parseDeploy([]string{"--podman-version", "v6.0.1"}); err == nil {
		t.Fatal("expected --podman-version to require --podman-prebuilt")
	}
}

func TestParseDeployBinaryVersionExclusive(t *testing.T) {
	if _, err := parseDeploy([]string{"--binary", "/tmp/rucher", "--version", "v1"}); err == nil {
		t.Fatal("expected --binary and --version to be mutually exclusive")
	}
}

func TestParseDeployMissingValue(t *testing.T) {
	if _, err := parseDeploy([]string{"--store-url"}); err == nil {
		t.Fatal("expected an error when a flag value is missing")
	}
}

// TestParseDeployRejectsUnknownFlag covers M10: a typo'd --store-* flag must error
// rather than be swallowed (flag + value) as node names, silently dropping the credential.
func TestParseDeployRejectsUnknownFlag(t *testing.T) {
	if _, err := parseDeploy([]string{"--store-xyz", "secret", "web"}); err == nil {
		t.Fatal("expected an unknown flag to error, not become a node name")
	}
	// A known no-value flag must still parse cleanly alongside a positional.
	df, err := parseDeploy([]string{"--store-ssl", "web"})
	if err != nil {
		t.Fatalf("--store-ssl should still parse: %v", err)
	}
	if df.store.UseSSL == nil || !*df.store.UseSSL || len(df.names) != 1 || df.names[0] != "web" {
		t.Fatalf("flags = %+v", df)
	}

	// --store-no-ssl must set an explicit false so a plaintext endpoint can be deployed.
	df, err = parseDeploy([]string{"--store-no-ssl", "web"})
	if err != nil {
		t.Fatalf("--store-no-ssl should parse: %v", err)
	}
	if df.store.UseSSL == nil || *df.store.UseSSL {
		t.Fatalf("--store-no-ssl must set UseSSL=false, got %+v", df.store.UseSSL)
	}
}

func TestParseDeployStoreUser(t *testing.T) {
	df, err := parseDeploy([]string{"--store-url", "git@x:r.git", "--store-user", "deploy", "web"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if df.store.User != "deploy" {
		t.Fatalf("store.User = %q, want deploy", df.store.User)
	}
}
