// SPDX-License-Identifier: AGPL-3.0-or-later

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
	// Error carries a pass-level failure (store sync, placement, listing). Without it
	// a failed pass persists a zero/partial Status and `ops nodes status` reads the
	// node as healthy; the field lets the reader surface the failure instead.
	Error string `json:"error,omitempty"`
}

func Run(ctx context.Context, r node.Runner, s store.Store, nodeID, nodeIdentity string) (Status, error) {
	co, rev, err := s.Sync(ctx)
	if err != nil {
		err = fmt.Errorf("store sync: %w", err)
		return Status{Error: err.Error()}, err
	}
	st := Status{Revision: rev}

	pdata, err := os.ReadFile(filepath.Join(co, "placement.yml"))
	if err != nil {
		err = fmt.Errorf("read placement.yml: %w", err)
		st.Error = err.Error()
		return st, err
	}
	assigned, err := placement.Assigned(pdata, nodeID)
	if err != nil {
		st.Error = err.Error()
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
		st.Error = err.Error()
		return st, err
	}
	for _, name := range managed {
		if !slices.Contains(assigned, name) {
			reconcile.Remove(r, name, false) // best-effort unmanage
			st.Removed = append(st.Removed, name)
		}
	}

	if failed {
		// Per-cadre failures are already carried in st.Applied; leave st.Error unset so the
		// reader does not print a redundant generic line on top of the specific ones. The
		// returned error still drives the caller's exit code.
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
	// A non-zero exit surfaces via Result.Code, not err, so both must be checked or a
	// failed mkdir/install would silently leave the identity absent or unwritten.
	if res, err := r.User(user, uid, []string{"mkdir", "-p", filepath.Dir(idPath)}, nil); err != nil || res.Code != 0 {
		return fmt.Errorf("mkdir %s identity dir: code=%d stderr=%s err=%v", name, res.Code, res.Stderr, err)
	}
	// install writes with the final 0600 in one step; tee would create the private key at
	// the user's umask (0644) and leave a TOCTOU window until a separate chmod.
	if res, err := r.User(user, uid, []string{"install", "-m", "600", "/dev/stdin", idPath}, identity); err != nil || res.Code != 0 {
		return fmt.Errorf("install %s identity: code=%d stderr=%s err=%v", name, res.Code, res.Stderr, err)
	}
	return nil
}

// readSealed returns the cadre's sealed identity.age, or nil if it ships none.
// One identity.age is sealed to every node that runs the cadre (age.SealTo writes
// a stanza per recipient), so there is no per-node file to choose between.
func readSealed(dir string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(dir, "identity.age"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return data, nil
}

// StatusPath is where the agent writes its last-pass status and where the operator
// plane (`ops nodes status`) reads it from — one constant so the two never drift.
const StatusPath = "/var/lib/rucher/agent-status.json"

func WriteStatus(path string, st Status) error {
	// 0711 (not 0755) on the dir: it is also the parent of cadre homes, so it must stay
	// traversable but need not be listable. The status file itself is 0600 — it carries every
	// cadre's names and error text, which no co-tenant cadre user should read.
	if err := os.MkdirAll(filepath.Dir(path), 0o711); err != nil {
		return err
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
