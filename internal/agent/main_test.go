package agent

import (
	"os"
	"testing"

	"podman-essaim-compartment-manager/internal/reconcile"
)

// TestMain redirects reconcile's persisted-state directory into a temp dir so the
// package's tests don't need write access to the production /var/lib base path
// (reconcile.Apply always saves state there).
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "agent-state")
	if err != nil {
		panic(err)
	}
	reconcile.BaseDirForState = dir
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
