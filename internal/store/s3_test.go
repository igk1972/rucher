package store

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestRevisionOf(t *testing.T) {
	a := []objInfo{
		{Key: "placement.yml", ETag: "aaa"},
		{Key: "compartments/web/compartment.yml", ETag: "bbb"},
	}
	// Same set in a different order must hash to the same revision.
	b := []objInfo{
		{Key: "compartments/web/compartment.yml", ETag: "bbb"},
		{Key: "placement.yml", ETag: "aaa"},
	}
	if revisionOf(a) != revisionOf(b) {
		t.Fatalf("revisionOf not order-independent: %q != %q", revisionOf(a), revisionOf(b))
	}

	// A changed ETag must change the revision.
	c := []objInfo{
		{Key: "placement.yml", ETag: "zzz"},
		{Key: "compartments/web/compartment.yml", ETag: "bbb"},
	}
	if revisionOf(a) == revisionOf(c) {
		t.Fatal("revisionOf unchanged after an ETag change")
	}

	if revisionOf(a) == "" {
		t.Fatal("empty revision")
	}
}

func TestResolveDest(t *testing.T) {
	base := filepath.Join(t.TempDir(), "cache")

	// A normal nested key resolves under base.
	got, err := resolveDest(base, filepath.Join("compartments", "web", "compartment.yml"))
	if err != nil {
		t.Fatalf("nested key: unexpected error: %v", err)
	}
	if want := filepath.Join(base, "compartments", "web", "compartment.yml"); got != want {
		t.Fatalf("nested key = %q, want %q", got, want)
	}

	// A key with "../" that escapes base must be rejected.
	if _, err := resolveDest(base, filepath.Join("..", "..", "etc", "x")); err == nil {
		t.Fatal("escaping key: expected error, got nil")
	}

	// A key that Rel-resolves back to exactly ".." must be rejected.
	if _, err := resolveDest(base, ".."); err == nil {
		t.Fatal(`".." key: expected error, got nil`)
	}

	// filepath.Join cleans a leading slash, so an "absolute" key stays under base and is SAFE.
	got, err = resolveDest(base, "/abs")
	if err != nil {
		t.Fatalf("leading-slash key: unexpected error: %v", err)
	}
	if want := filepath.Join(base, "abs"); got != want {
		t.Fatalf("leading-slash key = %q, want %q", got, want)
	}
}

// freePort asks the OS for an ephemeral TCP port, then releases it.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

func TestS3SyncAgainstRclone(t *testing.T) {
	if _, err := exec.LookPath("rclone"); err != nil {
		t.Skip("rclone not installed")
	}

	// Build a source tree; rclone serve s3 exposes each top-level dir as a bucket.
	src := t.TempDir()
	bucket := filepath.Join(src, "infrastructure")
	if err := os.MkdirAll(filepath.Join(bucket, "compartments", "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bucket, "placement.yml"), []byte("placements: {web: node-a}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bucket, "compartments", "web", "compartment.yml"), []byte("name: web\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	port := freePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "rclone", "serve", "s3", src,
		"--addr", addr,
		"--auth-key", "TESTKEY,TESTSECRET",
	)
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start rclone: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		cmd.Wait()
	})

	// Wait for the server to accept connections (up to ~5s).
	ready := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			conn.Close()
			ready = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		t.Fatalf("rclone s3 server never came up on %s", addr)
	}

	s := S3{
		Endpoint:  addr,
		Bucket:    "infrastructure",
		AccessKey: "TESTKEY",
		SecretKey: "TESTSECRET",
		UseSSL:    false,
		Region:    "us-east-1",
		CachePath: filepath.Join(t.TempDir(), "cache"),
	}
	co, rev, err := s.Sync(ctx)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if rev == "" {
		t.Fatal("empty revision")
	}

	got, err := os.ReadFile(filepath.Join(co, "placement.yml"))
	if err != nil {
		t.Fatalf("read placement.yml: %v", err)
	}
	if string(got) != "placements: {web: node-a}\n" {
		t.Fatalf("placement.yml = %q", got)
	}

	got, err = os.ReadFile(filepath.Join(co, "compartments", "web", "compartment.yml"))
	if err != nil {
		t.Fatalf("read compartment.yml: %v", err)
	}
	if string(got) != "name: web\n" {
		t.Fatalf("compartment.yml = %q", got)
	}
}
