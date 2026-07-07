package main

import "testing"

func TestParseDeploy(t *testing.T) {
	df, err := parseDeploy([]string{
		"--version", "v0.1.0", "--store-url", "git@example.com:store.git",
		"--store-branch", "prod", "--interval", "1m", "--json", "web", "db",
	})
	if err != nil {
		t.Fatal(err)
	}
	if df.version != "v0.1.0" || df.store.URL != "git@example.com:store.git" ||
		df.store.Branch != "prod" || df.interval != "1m" || !df.jsonOut {
		t.Fatalf("flags = %+v", df)
	}
	if len(df.names) != 2 || df.names[0] != "web" || df.names[1] != "db" {
		t.Fatalf("names = %v", df.names)
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
