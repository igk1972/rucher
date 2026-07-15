// SPDX-License-Identifier: AGPL-3.0-or-later

package provision

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLockNodeAcquiresAndReleases(t *testing.T) {
	t.Setenv("RUCHER_CADRES_DIR", t.TempDir())

	unlock, err := LockNode()
	if err != nil {
		t.Fatalf("LockNode: %v", err)
	}
	if _, err := os.Stat(filepath.Join(BaseDir(), ".lock")); err != nil {
		t.Fatalf("lock file not created: %v", err)
	}
	unlock()

	// Re-acquiring after release must not deadlock (fresh fd, same file).
	unlock2, err := LockNode()
	if err != nil {
		t.Fatalf("re-acquire after release failed: %v", err)
	}
	unlock2()
}
