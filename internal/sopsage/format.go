// SPDX-License-Identifier: AGPL-3.0-or-later

package sopsage

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// ageStanza is one recipient's armored age-encrypted copy of the data key. Field
// order (enc, recipient) matches the sops CLI's emitted block.
type ageStanza struct {
	Enc       string `yaml:"enc"`
	Recipient string `yaml:"recipient"`
}

// sopsMeta is the `sops:` metadata block for the age backend.
type sopsMeta struct {
	Age               []ageStanza `yaml:"age"`
	LastModified      string      `yaml:"lastmodified"`
	Mac               string      `yaml:"mac"`
	UnencryptedSuffix string      `yaml:"unencrypted_suffix"`
	Version           string      `yaml:"version"`
	MacOnlyEncrypted  bool        `yaml:"mac_only_encrypted,omitempty"`
}

// encPair is one data entry preserving file order (order matters for the MAC).
type encPair struct {
	Key string
	Enc string // the value: an ENC[...] string for encrypted files, plaintext otherwise
}

// parseEncryptedFile splits a SOPS YAML file into its ordered data entries and
// the `sops:` metadata, preserving key order for MAC recomputation.
func parseEncryptedFile(data []byte) ([]encPair, sopsMeta, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, sopsMeta{}, fmt.Errorf("parse yaml: %w", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, sopsMeta{}, fmt.Errorf("unexpected SOPS file shape")
	}
	root := doc.Content[0]
	var pairs []encPair
	var meta sopsMeta
	haveMeta := false
	for i := 0; i+1 < len(root.Content); i += 2 {
		key := root.Content[i].Value
		val := root.Content[i+1]
		if key == "sops" {
			if err := val.Decode(&meta); err != nil {
				return nil, sopsMeta{}, fmt.Errorf("decode sops metadata: %w", err)
			}
			haveMeta = true
			continue
		}
		pairs = append(pairs, encPair{Key: key, Enc: val.Value})
	}
	if !haveMeta {
		return nil, sopsMeta{}, fmt.Errorf("no sops metadata block")
	}
	return pairs, meta, nil
}

// emitEncryptedFile renders data entries (in order) plus the `sops:` block back
// to YAML. The age stanza's multi-line armored blob is emitted as a literal
// block, matching the sops CLI.
func emitEncryptedFile(pairs []encPair, meta sopsMeta) ([]byte, error) {
	root := &yaml.Node{Kind: yaml.MappingNode}
	for _, p := range pairs {
		val := &yaml.Node{Kind: yaml.ScalarNode, Value: p.Enc}
		if p.Enc == "" {
			// force `key: ""`, not `key:` (which reads back as null)
			val.Tag = "!!str"
			val.Style = yaml.DoubleQuotedStyle
		}
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: p.Key},
			val)
	}
	var metaNode yaml.Node
	if err := metaNode.Encode(meta); err != nil {
		return nil, fmt.Errorf("encode sops metadata: %w", err)
	}
	root.Content = append(root.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: "sops"},
		&metaNode)
	doc := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}
	return yaml.Marshal(doc)
}
