// Package placement resolves which compartments are assigned to a node.
package placement

import (
	"fmt"
	"slices"

	"gopkg.in/yaml.v3"
)

// nodeList accepts either a single node string or a list of node strings.
type nodeList []string

func (n *nodeList) UnmarshalYAML(value *yaml.Node) error {
	var one string
	if err := value.Decode(&one); err == nil {
		*n = nodeList{one}
		return nil
	}
	var many []string
	if err := value.Decode(&many); err != nil {
		return fmt.Errorf("placement value must be a node or a list of nodes: %w", err)
	}
	*n = many
	return nil
}

type file struct {
	Placements map[string]nodeList `yaml:"placements"`
}

func Assigned(data []byte, nodeID string) ([]string, error) {
	var f file
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse placement.yml: %w", err)
	}
	var out []string
	for name, nodes := range f.Placements {
		if slices.Contains(nodes, nodeID) {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out, nil
}
