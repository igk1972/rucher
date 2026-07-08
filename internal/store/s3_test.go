// SPDX-License-Identifier: AGPL-3.0-or-later

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
		{Key: "cadres/web/rucher.yml", ETag: "bbb"},
	}
	// Same set in a different order must hash to the same revision.
	b := []objInfo{
		{Key: "cadres/web/rucher.yml", ETag: "bbb"},
		{Key: "placement.yml", ETag: "aaa"},
	}
	if revisionOf(a) != revisionOf(b) {
		t.Fatalf("revisionOf not order-independent: %q != %q", revisionOf(a), revisionOf(b))
	}

	// A changed ETag must change the revision.
	c := []objInfo{
		{Key: "placement.yml", ETag: "zzz"},
		{Key: "cadres/web/rucher.yml", ETag: "bbb"},
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
	got, err := resolveDest(base, filepath.Join("cadres", "web", "rucher.yml"))
	if err != nil {
		t.Fatalf("nested key: unexpected error: %v", err)
	}
	if want := filepath.Join(base, "cadres", "web", "rucher.yml"); got != want {
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

func TestS3State(t *testing.T) {
	// A missing state file yields an empty (non-nil) state that forces a full download.
	missing := loadS3State(filepath.Join(t.TempDir(), "none.json"))
	if missing.Store != "" || len(missing.Objects) != 0 || missing.Objects == nil {
		t.Fatalf("missing state = %+v, want empty non-nil Objects", missing)
	}

	// A round trip preserves the store identity and object ETags.
	path := filepath.Join(t.TempDir(), "s3state.json")
	in := s3State{Store: "h|b|p/", Objects: map[string]string{"placement.yml": "e1", "cadres/web/x": "e2"}}
	if err := saveS3State(path, in); err != nil {
		t.Fatal(err)
	}
	out := loadS3State(path)
	if out.Store != in.Store || out.Objects["placement.yml"] != "e1" || out.Objects["cadres/web/x"] != "e2" {
		t.Fatalf("round trip = %+v, want %+v", out, in)
	}

	// A corrupt file degrades to an empty state (full resync) rather than an error.
	if err := os.WriteFile(path, []byte("{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if bad := loadS3State(path); len(bad.Objects) != 0 {
		t.Fatalf("corrupt state = %+v, want empty", bad)
	}
}

func TestS3StoreIdentity(t *testing.T) {
	base := S3{Endpoint: "h:9000", Bucket: "infra", Prefix: "store/"}
	if base.storeIdentity() == (S3{Endpoint: "h:9000", Bucket: "infra", Prefix: "other/"}).storeIdentity() {
		t.Fatal("different prefixes must yield different identities")
	}
	if base.storeIdentity() != (S3{Endpoint: "h:9000", Bucket: "infra", Prefix: "store/"}).storeIdentity() {
		t.Fatal("same config must yield the same identity")
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

// startRclone runs `rclone serve s3` over src on addr and returns a stop func once
// the server accepts connections. Restarting on the same addr makes a fresh process
// re-read src (rclone caches directory listings), which the incremental test needs.
func startRclone(t *testing.T, src, addr string) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "rclone", "serve", "s3", src,
		"--addr", addr,
		"--auth-key", "TESTKEY,TESTSECRET",
	)
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start rclone: %v", err)
	}
	stop := func() { cancel(); cmd.Wait() }
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err == nil {
			conn.Close()
			return stop
		}
		time.Sleep(100 * time.Millisecond)
	}
	stop()
	t.Fatalf("rclone s3 server never came up on %s", addr)
	return nil
}

// serveRcloneS3 starts rclone on a fresh port and returns its address.
func serveRcloneS3(t *testing.T, src string) string {
	t.Helper()
	addr := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	t.Cleanup(startRclone(t, src, addr))
	return addr
}

func TestS3SyncAgainstRclone(t *testing.T) {
	if _, err := exec.LookPath("rclone"); err != nil {
		t.Skip("rclone not installed")
	}

	// Build a source tree; rclone serve s3 exposes each top-level dir as a bucket.
	src := t.TempDir()
	bucket := filepath.Join(src, "infrastructure")
	if err := os.MkdirAll(filepath.Join(bucket, "cadres", "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bucket, "placement.yml"), []byte("placements: {web: node-a}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bucket, "cadres", "web", "rucher.yml"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	addr := serveRcloneS3(t, src)
	s := S3{
		Endpoint:  addr,
		Bucket:    "infrastructure",
		AccessKey: "TESTKEY",
		SecretKey: "TESTSECRET",
		UseSSL:    false,
		Region:    "us-east-1",
		CachePath: filepath.Join(t.TempDir(), "cache"),
	}
	co, rev, err := s.Sync(context.Background())
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

	got, err = os.ReadFile(filepath.Join(co, "cadres", "web", "rucher.yml"))
	if err != nil {
		t.Fatalf("read rucher.yml: %v", err)
	}
	if string(got) != "{}\n" {
		t.Fatalf("rucher.yml = %q", got)
	}
}

// TestS3IncrementalSync proves a second sync fetches only changed/new objects, keeps
// unchanged ones untouched, and drops removed ones.
func TestS3IncrementalSync(t *testing.T) {
	if _, err := exec.LookPath("rclone"); err != nil {
		t.Skip("rclone not installed")
	}
	src := t.TempDir()
	bucket := filepath.Join(src, "infra")
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(bucket, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("placement.yml", "placements: {web: node-a}\n")
	write("cadres/web/rucher.yml", "{}\n")
	write("cadres/old/rucher.yml", "{}\n")

	addr := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	stop := startRclone(t, src, addr)
	s := S3{
		Endpoint: addr, Bucket: "infra",
		AccessKey: "TESTKEY", SecretKey: "TESTSECRET", Region: "us-east-1",
		CachePath: filepath.Join(t.TempDir(), "cache"),
	}
	ctx := context.Background()

	co, rev1, err := s.Sync(ctx)
	if err != nil {
		t.Fatalf("sync1: %v", err)
	}

	// Replace an unchanged object's cached copy with a sentinel; if sync2 skips it (no
	// re-download), the sentinel survives — proving the fetch was incremental.
	const sentinel = "LOCAL-SENTINEL\n"
	if err := os.WriteFile(filepath.Join(co, "placement.yml"), []byte(sentinel), 0o644); err != nil {
		t.Fatal(err)
	}

	// Change one object, add one, remove one, then restart rclone on the same addr so a
	// fresh process serves the updated tree (same endpoint keeps the sync incremental).
	write("cadres/web/rucher.yml", "memoryMax: 1\n")
	write("cadres/new/rucher.yml", "{}\n")
	if err := os.Remove(filepath.Join(bucket, "cadres", "old", "rucher.yml")); err != nil {
		t.Fatal(err)
	}
	stop()
	t.Cleanup(startRclone(t, src, addr))

	_, rev2, err := s.Sync(ctx)
	if err != nil {
		t.Fatalf("sync2: %v", err)
	}
	if rev1 == rev2 {
		t.Fatal("revision unchanged after the store changed")
	}
	if got, _ := os.ReadFile(filepath.Join(co, "placement.yml")); string(got) != sentinel {
		t.Errorf("unchanged placement.yml was re-downloaded: %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(co, "cadres", "web", "rucher.yml")); string(got) != "memoryMax: 1\n" {
		t.Errorf("changed rucher.yml = %q, want refetched content", got)
	}
	if _, err := os.Stat(filepath.Join(co, "cadres", "new", "rucher.yml")); err != nil {
		t.Errorf("added file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(co, "cadres", "old", "rucher.yml")); !os.IsNotExist(err) {
		t.Errorf("removed file still present (err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(co, "cadres", "old")); !os.IsNotExist(err) {
		t.Errorf("emptied cadres/old dir was not pruned (err=%v)", err)
	}
}

// TestS3LastGoodOnListFailure: when a fresh listing fails but a valid cache exists, the
// sync keeps running on the last-good checkout and revision instead of erroring.
func TestS3LastGoodOnListFailure(t *testing.T) {
	if _, err := exec.LookPath("rclone"); err != nil {
		t.Skip("rclone not installed")
	}
	src := t.TempDir()
	bucket := filepath.Join(src, "infra")
	if err := os.MkdirAll(filepath.Join(bucket, "cadres", "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bucket, "placement.yml"), []byte("placements: {web: node-a}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bucket, "cadres", "web", "rucher.yml"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	addr := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	stop := startRclone(t, src, addr)
	s := S3{
		Endpoint: addr, Bucket: "infra",
		AccessKey: "TESTKEY", SecretKey: "TESTSECRET", Region: "us-east-1",
		CachePath: filepath.Join(t.TempDir(), "cache"),
	}

	co, rev1, err := s.Sync(context.Background())
	if err != nil {
		t.Fatalf("sync1: %v", err)
	}

	// Take the endpoint down; the next listing fails on the same store identity.
	stop()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	co2, rev2, err := s.Sync(ctx)
	if err != nil {
		t.Fatalf("expected last-good, got error: %v", err)
	}
	if co2 != co || rev2 != rev1 {
		t.Fatalf("last-good = (%q,%q), want (%q,%q)", co2, rev2, co, rev1)
	}
	if _, err := os.Stat(filepath.Join(co, "placement.yml")); err != nil {
		t.Errorf("cached checkout lost: %v", err)
	}
}
