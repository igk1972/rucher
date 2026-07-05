package agent

import (
	"os"
	"testing"
)

// TestMain redirects reconcile's persisted-state directory into a temp dir so the
// package's tests don't need write access to the production /var/lib base path
// (reconcile.Apply always saves state there). TestMain has no *testing.T, so use
// os.Setenv; it's process-scoped for this test binary, which is fine.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "agent-state")
	if err != nil {
		panic(err)
	}
	os.Setenv("PECM_STATE_DIR", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
