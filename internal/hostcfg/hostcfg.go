// Package hostcfg reads and updates ./hosts/<name>/configuration.yml (network/connection).
package hostcfg

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"gopkg.in/yaml.v3"
)

type Network struct {
	Driver  string `yaml:"driver"`
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
