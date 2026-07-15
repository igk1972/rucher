// SPDX-License-Identifier: AGPL-3.0-or-later

package provision

import (
	"os"
	"path/filepath"
)

// LockNode takes an exclusive, node-wide advisory lock and returns an unlock func.
// It serializes the mutating node-side operations (subuid/subgid allocation in
// EnsureUser, per-cadre state writes in reconcile) so the agent timer and a manual
// `node cadre apply`/`new`/`rm` cannot interleave — which could otherwise assign
// overlapping subuid ranges (an isolation break) or clobber a state file.
//
// The lock is a flock on <BaseDir>/.lock, advisory and process-scoped; it is a no-op
// on non-unix builds since node-side mutation only ever runs on Linux.
func LockNode() (func(), error) {
	dir := BaseDir()
	// 0711, not 0700: BaseDir is the parent of every cadre user's home, so a rootless cadre
	// user needs the search (+x) bit to reach its own home; each home stays 0700 (useradd), so
	// others still cannot list this dir or read a sibling cadre's home.
	if err := os.MkdirAll(dir, 0o711); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := flockEx(f.Fd()); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		flockUn(f.Fd())
		f.Close()
	}, nil
}
