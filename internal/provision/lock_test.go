// SPDX-License-Identifier: AGPL-3.0-or-later

package provision

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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

func TestLockNodeSerializesHolders(t *testing.T) {
	// The lock must actually mutually exclude, not merely acquire/release: while one holder
	// has it, a second LockNode (fresh fd on the same file — flock contends across open file
	// descriptions of the same process) must block until the first releases.
	t.Setenv("RUCHER_CADRES_DIR", t.TempDir())

	unlock, err := LockNode()
	if err != nil {
		t.Fatalf("LockNode: %v", err)
	}

	acquired := make(chan func(), 1)
	go func() {
		unlock2, err := LockNode() // blocks until the first holder releases
		if err != nil {
			t.Errorf("second LockNode: %v", err)
			close(acquired)
			return
		}
		acquired <- unlock2
	}()

	// While the first holder is live the goroutine must not acquire.
	select {
	case <-acquired:
		t.Fatal("second LockNode acquired while the first was still held: no mutual exclusion")
	case <-time.After(200 * time.Millisecond):
	}

	unlock()

	select {
	case unlock2 := <-acquired:
		if unlock2 == nil {
			t.Fatal("second LockNode failed after release")
		}
		unlock2()
	case <-time.After(2 * time.Second):
		t.Fatal("second LockNode did not acquire after release (deadlock)")
	}
}
