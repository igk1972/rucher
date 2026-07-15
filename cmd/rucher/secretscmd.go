// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"rucher/internal/age"
	"rucher/internal/provision"
	"rucher/internal/sopsage"
)

// secretsEncryptFlags is the parsed `ops secrets encrypt` command line.
type secretsEncryptFlags struct {
	to      []string // direct recipients (stdin -> stdout mode)
	cadre   string   // seal mode: cadre name (paths under dir/<cadre>/)
	sealTo  []string // node recipients to seal the generated cadre identity to
	dir     string   // parent dir for cadres (default "cadres")
	inFile  string   // plaintext input (default: stdin)
	outFile string   // output (default: stdout, or dir/<cadre>/secrets.sops.yaml in seal mode)
}

// parseSecretsEncrypt parses the flags; --to and --seal-to are de-duplicated.
func parseSecretsEncrypt(args []string) (secretsEncryptFlags, error) {
	fl := secretsEncryptFlags{dir: "cadres"}
	seen, sealSeen := map[string]bool{}, map[string]bool{}
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
		case "--to":
			if v, err = need(i); err == nil {
				if !seen[v] {
					seen[v] = true
					fl.to = append(fl.to, v)
				}
				i++
			}
		case "--seal-to":
			if v, err = need(i); err == nil {
				if !sealSeen[v] {
					sealSeen[v] = true
					fl.sealTo = append(fl.sealTo, v)
				}
				i++
			}
		case "--cadre":
			if v, err = need(i); err == nil {
				fl.cadre, i = v, i+1
			}
		case "--dir":
			if v, err = need(i); err == nil {
				fl.dir, i = v, i+1
			}
		case "--in":
			if v, err = need(i); err == nil {
				fl.inFile, i = v, i+1
			}
		case "--out":
			if v, err = need(i); err == nil {
				fl.outFile, i = v, i+1
			}
		default:
			return secretsEncryptFlags{}, fmt.Errorf("unexpected argument: %q", a)
		}
		if err != nil {
			return secretsEncryptFlags{}, err
		}
	}
	sealMode := len(fl.sealTo) > 0
	switch {
	case sealMode && len(fl.to) > 0:
		return secretsEncryptFlags{}, fmt.Errorf("--seal-to encrypts to the generated cadre key; do not also pass --to")
	case sealMode && fl.cadre == "":
		return secretsEncryptFlags{}, fmt.Errorf("--seal-to requires --cadre <name>")
	case !sealMode && len(fl.to) == 0:
		return secretsEncryptFlags{}, fmt.Errorf("usage: ops secrets encrypt --to <recipient> ... | --cadre <name> --seal-to <node-recipient> ...")
	}
	return fl, nil
}

// cmdSecretsEncrypt encrypts a flat plaintext YAML map to SOPS+age.
//
//   - direct mode (--to): reads stdin, writes stdout (the in-process replacement
//     for `sops --encrypt --age <recipient>`).
//   - seal mode (--cadre + --seal-to): generates the cadre's age identity, seals
//     it to the node recipient(s) into dir/<cadre>/identity.age, encrypts to that
//     identity, and writes dir/<cadre>/secrets.sops.yaml — one command, no shell.
func cmdSecretsEncrypt(args []string, in io.Reader, out io.Writer) int {
	fl, err := parseSecretsEncrypt(args)
	if err != nil {
		fmt.Fprintln(out, "error:", err)
		return 2
	}
	// --cadre builds dir/<cadre>/ paths, so reject a traversal name before any join.
	if fl.cadre != "" && !provision.ValidName(fl.cadre) {
		fmt.Fprintf(out, "error: invalid cadre name %q (must match [a-z0-9][a-z0-9-]* and be at most %d chars)\n", fl.cadre, provision.MaxCadreName)
		return 2
	}

	var data []byte
	if fl.inFile != "" {
		data, err = os.ReadFile(fl.inFile)
	} else {
		data, err = io.ReadAll(in)
	}
	if err != nil {
		fmt.Fprintln(out, "error:", err)
		return 1
	}
	kvs, err := parsePlainYAML(data)
	if err != nil {
		fmt.Fprintln(out, "error:", err)
		return 1
	}

	recipients := fl.to
	outPath := fl.outFile
	var cadreRecipient string
	if len(fl.sealTo) > 0 {
		id, rcpt, err := age.GenerateIdentity()
		if err != nil {
			fmt.Fprintln(out, "error:", err)
			return 1
		}
		sealed, err := age.SealTo(fl.sealTo, []byte(id))
		if err != nil {
			fmt.Fprintln(out, "error:", err)
			return 1
		}
		cadreDir := filepath.Join(fl.dir, fl.cadre)
		if err := os.MkdirAll(cadreDir, 0o755); err != nil {
			fmt.Fprintln(out, "error:", err)
			return 1
		}
		if err := os.WriteFile(filepath.Join(cadreDir, "identity.age"), sealed, 0o600); err != nil {
			fmt.Fprintln(out, "error:", err)
			return 1
		}
		recipients = []string{rcpt} // encrypt to the freshly generated cadre key
		cadreRecipient = rcpt
		if outPath == "" {
			outPath = filepath.Join(cadreDir, "secrets.sops.yaml")
		}
	}

	enc, err := sopsage.Encrypt(recipients, kvs, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		fmt.Fprintln(out, "error:", err)
		return 1
	}
	if outPath == "" {
		out.Write(enc)
		return 0
	}
	if err := os.WriteFile(outPath, enc, 0o644); err != nil {
		fmt.Fprintln(out, "error:", err)
		return 1
	}
	if cadreRecipient != "" {
		fmt.Fprintln(out, cadreRecipient) // print the cadre recipient for reference
	}
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
