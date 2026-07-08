// SPDX-License-Identifier: AGPL-3.0-or-later

// Package manifest parses and validates a rucher.yml manifest.
package manifest

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

type Manifest struct {
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
	// An empty rucher.yml is a valid nameless manifest (every field defaults); yaml.v3
	// reports an empty/comment-only document as io.EOF, which is not a parse error here.
	if err := dec.Decode(&m); err != nil && !errors.Is(err, io.EOF) {
		return Manifest{}, fmt.Errorf("parse rucher.yml: %w", err)
	}
	if m.Secrets.From == "" {
		m.Secrets.From = defaultSecretsFile
	}
	return m, nil
}

func (m Manifest) Validate() error {
	for i, l := range m.Registries.Login {
		if l.Registry == "" || l.Username == "" || l.PasswordKey == "" {
			return fmt.Errorf("manifest: login[%d] needs registry, username and passwordKey", i)
		}
	}
	return nil
}
