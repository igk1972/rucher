// Package manifest parses and validates a compartment.yml manifest.
package manifest

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

type Manifest struct {
	Name       string     `yaml:"name"`
	Secrets    Secrets    `yaml:"secrets"`
	Registries Registries `yaml:"registries"`
	Resources  Resources  `yaml:"resources"`
}

type Secrets struct {
	From   string   `yaml:"from"`
	Create []string `yaml:"create"`
}

type Registries struct {
	Login []Login `yaml:"login"`
}

type Login struct {
	Registry    string `yaml:"registry"`
	Username    string `yaml:"username"`
	PasswordKey string `yaml:"passwordKey"`
	Insecure    bool   `yaml:"insecure"`
}

type Resources struct {
	MemoryMax string `yaml:"memoryMax"`
	CPUQuota  string `yaml:"cpuQuota"`
}

const defaultSecretsFile = "secrets.sops.yaml"

func Load(data []byte) (Manifest, error) {
	var m Manifest
	// strict decode: reject unknown keys so a typo'd field (e.g. memmoryMax) is a
	// hard error rather than being silently dropped.
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("parse compartment.yml: %w", err)
	}
	if m.Secrets.From == "" {
		m.Secrets.From = defaultSecretsFile
	}
	return m, nil
}

func (m Manifest) Validate() error {
	if m.Name == "" {
		return fmt.Errorf("manifest: name is required")
	}
	for i, l := range m.Registries.Login {
		if l.Registry == "" || l.Username == "" || l.PasswordKey == "" {
			return fmt.Errorf("manifest: login[%d] needs registry, username and passwordKey", i)
		}
	}
	return nil
}
