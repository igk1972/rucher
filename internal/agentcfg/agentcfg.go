// SPDX-License-Identifier: AGPL-3.0-or-later

// Package agentcfg loads the on-node agent configuration.
package agentcfg

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type StoreConfig struct {
	Kind   string `yaml:"kind"` // "git" (default) | "s3"
	URL    string `yaml:"url"`
	Branch string `yaml:"branch"`
	SSHKey string `yaml:"sshKey"`
	Token  string `yaml:"token"`
	User   string `yaml:"user"`

	InsecureHostKey bool `yaml:"insecureHostKey"`

	// S3 store fields (kind: s3).
	Endpoint  string `yaml:"endpoint"` // host:port, no scheme
	Bucket    string `yaml:"bucket"`
	Prefix    string `yaml:"prefix"`
	AccessKey string `yaml:"accessKey"`
	SecretKey string `yaml:"secretKey"`
	Region    string `yaml:"region"`
	UseSSL    bool   `yaml:"useSSL"`
}

type Config struct {
	Node     string      `yaml:"node"`
	Store    StoreConfig `yaml:"store"`
	Interval string      `yaml:"interval"`
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read agent config: %w", err)
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return Config{}, fmt.Errorf("parse agent config: %w", err)
	}
	if c.Store.Kind == "" {
		c.Store.Kind = "git"
	}
	if c.Store.Branch == "" {
		c.Store.Branch = "main"
	}
	return c, nil
}

// NodeID returns the configured node id, defaulting to the OS hostname.
func (c Config) NodeID() (string, error) {
	if c.Node != "" {
		return c.Node, nil
	}
	host, err := os.Hostname()
	if err != nil {
		return "", fmt.Errorf("resolve hostname for node id: %w", err)
	}
	return host, nil
}
