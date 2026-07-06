// Package nodecfg reads and updates ./hosts/<name>/configuration.yml (network/connection).
package nodecfg

import (
	"fmt"
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

type Config struct {
	Network    Network    `yaml:"network"`
	Connection Connection `yaml:"connection"`
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return c, nil
}

// LoadMerged reads the fleet-global ./hosts/configuration.yml (if present) and the
// per-host ./hosts/<name>/configuration.yml, then deep-merges the per-host doc OVER
// the global one (maps merge key-by-key; scalars and sequences are replaced) before
// decoding into Config. The global file is optional; the per-host file is required.
func LoadMerged(hostsDir, name string) (Config, error) {
	globalPath := filepath.Join(hostsDir, "configuration.yml")
	global, err := readYAMLMap(globalPath)
	if err != nil && !os.IsNotExist(err) {
		return Config{}, err
	}

	hostPath := filepath.Join(hostsDir, name, "configuration.yml")
	host, err := readYAMLMap(hostPath)
	if err != nil {
		return Config{}, err
	}

	merged := deepMerge(global, host)
	out, err := yaml.Marshal(merged)
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := yaml.Unmarshal(out, &c); err != nil {
		return Config{}, fmt.Errorf("parse merged %s: %w", hostPath, err)
	}
	return c, nil
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

// List returns the names of host subdirectories that contain a configuration.yml.
func List(hostsDir string) ([]string, error) {
	entries, err := os.ReadDir(hostsDir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(hostsDir, e.Name(), "configuration.yml")); err == nil {
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
	// The host's config directory must already exist: net join records an address for
	// a defined host, it does not create one. Surface a clear error instead of the raw
	// "open ...: no such file or directory" the write below would otherwise return.
	if dir := filepath.Dir(path); dir != "" {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			return fmt.Errorf("host directory does not exist: %s", dir)
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
