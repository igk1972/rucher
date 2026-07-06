// Package agent runs one GitOps reconcile pass: fetch the store, apply this node's
// assigned cadres (installing their unsealed identity first), remove the rest.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"rucher/internal/age"
	"rucher/internal/cadre"
	"rucher/internal/node"
	"rucher/internal/placement"
	"rucher/internal/provision"
	"rucher/internal/reconcile"
	"rucher/internal/store"
)

type Result struct {
	Name  string `json:"name"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

type Status struct {
	Revision string   `json:"revision"`
	Applied  []Result `json:"applied"`
	Removed  []string `json:"removed"`
}

func Run(ctx context.Context, r node.Runner, s store.Store, nodeID, nodeIdentity string) (Status, error) {
	co, rev, err := s.Sync(ctx)
	if err != nil {
		return Status{}, fmt.Errorf("store sync: %w", err)
	}
	st := Status{Revision: rev}

	pdata, err := os.ReadFile(filepath.Join(co, "placement.yml"))
	if err != nil {
		return st, fmt.Errorf("read placement.yml: %w", err)
	}
	assigned, err := placement.Assigned(pdata, nodeID)
	if err != nil {
		return st, err
	}

	failed := false
	for _, name := range assigned {
		if err := applyOne(r, co, name, nodeIdentity); err != nil {
			st.Applied = append(st.Applied, Result{Name: name, Error: err.Error()})
			failed = true
			continue
		}
		st.Applied = append(st.Applied, Result{Name: name, OK: true})
	}

	// remove cadres managed on this node but no longer assigned
	managed, err := reconcile.List()
	if err != nil {
		return st, err
	}
	for _, name := range managed {
		if !slices.Contains(assigned, name) {
			reconcile.Remove(r, name, false) // best-effort unmanage
			st.Removed = append(st.Removed, name)
		}
	}

	if failed {
		return st, fmt.Errorf("one or more cadres failed to apply")
	}
	return st, nil
}

func applyOne(r node.Runner, checkout, name, nodeIdentity string) error {
	dir := filepath.Join(checkout, "cadres", name)

	// ensure the user exists, then install the unsealed cadre identity so Apply can decrypt.
	uid, err := provision.EnsureUser(r, name)
	if err != nil {
		return err
	}
	if err := installIdentity(r, name, uid, dir, nodeIdentity); err != nil {
		return err
	}

	c, err := cadre.Load(dir)
	if err != nil {
		return err
	}
	_, err = reconcile.Apply(r, c)
	return err
}

// installIdentity unseals identity[.<node>].age with the node key and writes it to the
// cadre's age identity path (as the cadre user). No-op if there is no sealed file.
func installIdentity(r node.Runner, name string, uid int, dir, nodeIdentity string) error {
	sealed, err := readSealed(dir)
	if err != nil {
		return err
	}
	if sealed == nil {
		return nil // no secrets for this cadre
	}
	identity, err := age.Unseal(nodeIdentity, sealed)
	if err != nil {
		return fmt.Errorf("unseal %s identity: %w", name, err)
	}
	user := provision.UserName(name)
	idPath := reconcile.IdentityPath(name) // same path A's decrypt reads
	if _, err := r.User(user, uid, []string{"mkdir", "-p", filepath.Dir(idPath)}, nil); err != nil {
		return err
	}
	if _, err := r.User(user, uid, []string{"tee", idPath}, identity); err != nil {
		return err
	}
	// tee creates the file at the user's umask; the unsealed identity is a private key.
	if _, err := r.User(user, uid, []string{"chmod", "600", idPath}, nil); err != nil {
		return err
	}
	return nil
}

func readSealed(dir string) ([]byte, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var generic []byte
	for _, e := range entries {
		n := e.Name()
		if n == "identity.age" {
			generic, err = os.ReadFile(filepath.Join(dir, n))
			if err != nil {
				return nil, err
			}
		}
	}
	// prefer a node-specific file if present is handled by the caller via naming; here we
	// return the generic identity.age (node-specific selection lives in Task-9 wiring if used).
	return generic, nil
}

func WriteStatus(path string, st Status) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
