// SPDX-License-Identifier: AGPL-3.0-or-later

// Package nodecfg reads and updates ./nodes/<name>/configuration.yml (network/connection).
package nodecfg

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"gopkg.in/yaml.v3"
)

type Network struct {
	Address string `yaml:"address"`
}

type Connection struct {
	Host     string `yaml:"host"`
	User     string `yaml:"user"`
	Port     int    `yaml:"port"`
	Identity string `yaml:"identity"`
}

// Podman selects where a node gets podman when it has none yet. Source "apt" (the
// default) installs the distro package; "prebuilt" installs the .deb from a GitHub
// Release (see deploy.podmanDebRepo), with Version pinning a release tag (empty = latest).
type Podman struct {
	Source     string     `yaml:"source"`
	Version    string     `yaml:"version"`
	Registries Registries `yaml:"registries"`
}

type Registries struct {
	Search []string        `yaml:"search"`
	Login  []RegistryLogin `yaml:"login"`
}

type RegistryLogin struct {
	Registry    string `yaml:"registry"`
	Username    string `yaml:"username"`
	PasswordEnv string `yaml:"passwordEnv"`
}

type Config struct {
	Network    Network    `yaml:"network"`
	Connection Connection `yaml:"connection"`
	Podman     Podman     `yaml:"podman"`
}

// Load reads a single configuration.yml. The runtime path is lenient — an unknown key (a
// field another tool sharing the file owns) is ignored, not fatal. ValidateMerged is where
// a typo gets caught, via `ops validate`.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := decode(data, &c, false); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return c, nil
}

// decode unmarshals YAML into out. With strict, an unknown key is a hard error (validation);
// otherwise it is ignored. An empty document is not an error.
func decode(data []byte, out any, strict bool) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(strict)
	if err := dec.Decode(out); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// LoadMerged reads the optional global ./nodes/configuration.yml and the required per-node
// ./nodes/<name>/configuration.yml, deep-merging the per-node doc OVER the global one (maps
// merge key-by-key; scalars and sequences are replaced). Lenient like Load; ValidateMerged
// is the strict counterpart.
func LoadMerged(nodesDir, name string) (Config, error) {
	out, err := mergedBytes(nodesDir, name)
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := decode(out, &c, false); err != nil {
		return Config{}, fmt.Errorf("parse merged %s config: %w", name, err)
	}
	return c, nil
}

// ValidateMerged strict-decodes a node's merged config, erroring on any unknown key — the
// check `ops validate` runs before a deploy, which the lenient runtime path skips.
func ValidateMerged(nodesDir, name string) error {
	out, err := mergedBytes(nodesDir, name)
	if err != nil {
		return err
	}
	var c Config
	return decode(out, &c, true)
}

func mergedBytes(nodesDir, name string) ([]byte, error) {
	global, err := readYAMLMap(filepath.Join(nodesDir, "configuration.yml"))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	nodeDoc, err := readYAMLMap(filepath.Join(nodesDir, name, "configuration.yml"))
	if err != nil {
		return nil, err
	}
	return yaml.Marshal(deepMerge(global, nodeDoc))
}

// readYAMLMap reads a YAML file into a map. An empty file yields a nil map.
func readYAMLMap(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return m, nil
}

// deepMerge returns base with over applied on top: when both values at a key are
// maps they merge recursively; otherwise over's value replaces base's.
func deepMerge(base, over map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(over))
	for k, v := range base {
		out[k] = v
	}
	for k, ov := range over {
		if bv, ok := out[k]; ok {
			bm, bok := bv.(map[string]any)
			om, ook := ov.(map[string]any)
			if bok && ook {
				out[k] = deepMerge(bm, om)
				continue
			}
		}
		out[k] = ov
	}
	return out
}

// List returns the names of node subdirectories that contain a configuration.yml.
func List(nodesDir string) ([]string, error) {
	entries, err := os.ReadDir(nodesDir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(nodesDir, e.Name(), "configuration.yml")); err == nil {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	return names, nil
}

func scalar(v string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
}

// setKey replaces the value for key in a mapping node, or appends the pair.
func setKey(m *yaml.Node, key string, val *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = val
			return
		}
	}
	m.Content = append(m.Content, scalar(key), val)
}

// WriteNetwork inserts/updates the `network:` block, preserving other keys and comments.
func WriteNetwork(path string, n Network) error {
	// The node's config directory must already exist: `ops nodes join` records an address
	// for a defined node, it does not create one. Surface a clear error instead of the raw
	// "open ...: no such file or directory" the write below would otherwise return.
	if dir := filepath.Dir(path); dir != "" {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			return fmt.Errorf("node directory does not exist: %s", dir)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var doc yaml.Node
	if len(data) > 0 {
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
	}
	// resolve (or create) the root mapping node
	var root *yaml.Node
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		root = doc.Content[0]
	} else {
		root = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}
	}
	if root.Kind != yaml.MappingNode {
		root.Kind, root.Tag, root.Content = yaml.MappingNode, "!!map", nil
	}
	netVal := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
		scalar("address"), scalar(n.Address),
	}}
	setKey(root, "network", netVal)

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}
