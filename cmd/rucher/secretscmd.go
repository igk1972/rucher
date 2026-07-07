package main

import (
	"fmt"
	"io"
	"time"

	"gopkg.in/yaml.v3"

	"rucher/internal/sopsage"
)

// parseSecretsEncrypt collects the repeatable --to recipients (de-duplicated).
func parseSecretsEncrypt(args []string) ([]string, error) {
	var recipients []string
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--to":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--to needs a recipient")
			}
			if r := args[i+1]; !seen[r] {
				seen[r] = true
				recipients = append(recipients, r)
			}
			i++
		default:
			return nil, fmt.Errorf("unexpected argument: %q", args[i])
		}
	}
	if len(recipients) == 0 {
		return nil, fmt.Errorf("usage: ops secrets encrypt --to <recipient> [--to <recipient> ...]")
	}
	return recipients, nil
}

// cmdSecretsEncrypt reads a flat plaintext YAML map on in and writes the
// SOPS+age encrypted document to out, encrypted to every --to recipient. It is
// the in-process replacement for `sops --encrypt --age <recipient>`.
func cmdSecretsEncrypt(args []string, in io.Reader, out io.Writer) int {
	recipients, err := parseSecretsEncrypt(args)
	if err != nil {
		fmt.Fprintln(out, "error:", err)
		return 2
	}
	data, err := io.ReadAll(in)
	if err != nil {
		fmt.Fprintln(out, "error:", err)
		return 1
	}
	kvs, err := parsePlainYAML(data)
	if err != nil {
		fmt.Fprintln(out, "error:", err)
		return 1
	}
	enc, err := sopsage.Encrypt(recipients, kvs, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		fmt.Fprintln(out, "error:", err)
		return 1
	}
	out.Write(enc)
	return 0
}

// parsePlainYAML reads a flat `key: value` YAML map, preserving key order (which
// fixes the SOPS MAC order). Every value must be a scalar.
func parsePlainYAML(data []byte) ([]sopsage.KV, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("expected a YAML map of key: value")
	}
	root := doc.Content[0]
	var kvs []sopsage.KV
	for i := 0; i+1 < len(root.Content); i += 2 {
		k := root.Content[i].Value
		v := root.Content[i+1]
		if v.Kind != yaml.ScalarNode {
			return nil, fmt.Errorf("value for %q must be a scalar", k)
		}
		kvs = append(kvs, sopsage.KV{Key: k, Value: v.Value})
	}
	if len(kvs) == 0 {
		return nil, fmt.Errorf("no key/value pairs in input")
	}
	return kvs, nil
}
