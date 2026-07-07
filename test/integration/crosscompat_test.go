//go:build integration

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"rucher/internal/age"
)

// TestSopsCLICrossCompat proves the in-process codec is byte-compatible with the
// real sops CLI: our `ops secrets encrypt` output decrypts with `sops -d`. The
// reverse direction (sops --encrypt -> our in-process decrypt) is exercised on
// the node by TestSecretReachesContainerEnv, whose fixtures are built with the
// host sops CLI. This test is host-only (no Lima node required).
func TestSopsCLICrossCompat(t *testing.T) {
	if _, err := exec.LookPath("sops"); err != nil {
		t.Fatal("sops CLI required on the host for the cross-compat test")
	}
	build(t) // builds hostBin

	id, rec, err := age.GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	idFile := filepath.Join(t.TempDir(), "identity.txt")
	if err := os.WriteFile(idFile, []byte(id+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Our encrypt.
	enc := runCmd(t, exec.Command(hostBin, "ops", "secrets", "encrypt", "--to", rec),
		[]byte("db_password: s3cr3t\napi_key: k-123\n"))
	if enc.code != 0 {
		t.Fatalf("ops secrets encrypt exited %d: %s", enc.code, enc.stderr)
	}

	// The real sops CLI must accept and decrypt it.
	cmd := exec.Command("sops", "-d", "--input-type", "yaml", "--output-type", "json", "/dev/stdin")
	cmd.Env = append(os.Environ(), "SOPS_AGE_KEY_FILE="+idFile)
	dec := runCmd(t, cmd, []byte(enc.stdout))
	if dec.code != 0 {
		t.Fatalf("sops -d rejected our output: %s", dec.stderr)
	}
	if !strings.Contains(dec.stdout, `"s3cr3t"`) || !strings.Contains(dec.stdout, `"k-123"`) {
		t.Fatalf("sops decrypt = %s", dec.stdout)
	}
}
