// SPDX-License-Identifier: AGPL-3.0-or-later
//go:build integration

package integration

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeFileP writes body to path, creating parent directories.
func writeFileP(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// waitListen blocks until addr accepts a TCP connection or the timeout elapses.
func waitListen(t *testing.T, addr string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err == nil {
			c.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("nothing listening on %s after %s", addr, timeout)
}

const (
	s3Port      = "9419"
	s3Bucket    = "infra"
	s3AccessKey = "rucherkey123"
	s3SecretKey = "ruchersecret456"
)

// s3store is an rclone-backed S3 endpoint serving a temp dir on the host, reachable from
// the guests over the Lima gateway. rclone treats each top-level subdir as a bucket;
// object keys are paths within it, served live (writes are visible on the next pass).
type s3store struct {
	dir    string // served root
	bucket string
	base   string // dir/bucket
}

func startS3Store(t *testing.T) *s3store {
	t.Helper()
	if _, err := exec.LookPath("rclone"); err != nil {
		t.Fatal("rclone not found on host")
	}
	s := &s3store{bucket: s3Bucket}
	s.dir = homeTemp(t, "s3-")
	s.base = filepath.Join(s.dir, s.bucket)
	if err := os.MkdirAll(s.base, 0o755); err != nil {
		t.Fatalf("mkdir bucket: %v", err)
	}
	rclone := exec.Command("rclone", "serve", "s3",
		"--addr", "0.0.0.0:"+s3Port,
		"--auth-key", s3AccessKey+","+s3SecretKey,
		"--force-path-style=true", "--vfs-cache-mode", "off", s.dir)
	if err := rclone.Start(); err != nil {
		t.Fatalf("start rclone: %v", err)
	}
	t.Cleanup(func() { rclone.Process.Kill(); rclone.Wait() })
	waitListen(t, "127.0.0.1:"+s3Port, 10*time.Second)
	return s
}

func (s *s3store) write(t *testing.T, rel, body string) {
	writeFileP(t, filepath.Join(s.base, rel), body)
}

func (s *s3store) seedCadre(t *testing.T, name string) {
	s.write(t, "cadres/"+name+"/rucher.yml", "{}\n")
	s.write(t, "cadres/"+name+"/data.volume", volumeUnit)
}

// prepare clears the store cache, ensures the node key, and writes an S3 agent config
// per node (the S3 counterpart of prepareGitOps).
func (s *s3store) prepare(t *testing.T, nodes ...string) {
	t.Helper()
	for _, n := range nodes {
		resetAgentCache(t, n)
		nodeKeyInit(t, n)
		cfg := "node: " + n + "\nstore:\n  kind: s3\n" +
			"  endpoint: host.lima.internal:" + s3Port + "\n" +
			"  bucket: " + s.bucket + "\n" +
			"  accessKey: " + s3AccessKey + "\n" +
			"  secretKey: " + s3SecretKey + "\n" +
			"  region: us-east-1\n" +
			"  useSSL: false\n"
		nodeSudo(t, n, "mkdir", "-p", "/etc/rucher")
		if r := nodeSudoStdin(t, n, []byte(cfg), "tee", "/etc/rucher/agent.yml"); r.code != 0 {
			t.Fatalf("write agent.yml on %s: %s", n, r.stderr)
		}
	}
}

// T3.4 — the agent reconciles a cadre from an S3 store, not just git.
func TestS3StorePlacement(t *testing.T) {
	requireNodes(t, node1)
	const name = "its3"
	t.Cleanup(func() { cleanupCadre(t, name, node1) })

	s := startS3Store(t)
	s.seedCadre(t, name)
	s.write(t, "placement.yml", "placements:\n  "+name+": "+node1+"\n")
	s.prepare(t, node1)

	if r := agentRun(t, node1); r.code != 0 || !strings.Contains(r.stdout, "applied=1") {
		t.Fatalf("S3 agent run: code=%d out=%q err=%q", r.code, r.stdout, r.stderr)
	}
	if u := nodeSudo(t, node1, "id", "-u", "rucher-"+name); u.code != 0 {
		t.Fatalf("cadre user not created from the S3 store")
	}
}

// S3 parity with TestPlacementAcrossNodes: one S3 placement fans a cadre out to several
// nodes; a node it is not assigned to leaves it alone.
func TestS3PlacementAcrossNodes(t *testing.T) {
	requireNodes(t, node1, node2, node3)
	const name = "its3fan"
	t.Cleanup(func() { cleanupCadre(t, name, node1, node2, node3) })

	s := startS3Store(t)
	s.seedCadre(t, name)
	s.write(t, "placement.yml", "placements:\n  "+name+":\n    - "+node1+"\n    - "+node2+"\n")
	s.prepare(t, node1, node2, node3)

	if r := agentRun(t, node1); r.code != 0 || !strings.Contains(r.stdout, "applied=1") {
		t.Fatalf("%s: want applied=1, out=%q err=%q", node1, r.stdout, r.stderr)
	}
	if r := agentRun(t, node2); r.code != 0 || !strings.Contains(r.stdout, "applied=1") {
		t.Fatalf("%s: want applied=1, out=%q err=%q", node2, r.stdout, r.stderr)
	}
	if r := agentRun(t, node3); r.code != 0 || !strings.Contains(r.stdout, "applied=0") {
		t.Fatalf("%s: want applied=0, out=%q err=%q", node3, r.stdout, r.stderr)
	}
	if u := nodeSudo(t, node1, "id", "-u", "rucher-"+name); u.code != 0 {
		t.Fatalf("cadre user missing on %s", node1)
	}
	if u := nodeSudo(t, node3, "id", "-u", "rucher-"+name); u.code == 0 {
		t.Fatalf("cadre user unexpectedly present on %s", node3)
	}
}

// S3 parity with TestCadreMigration: rewriting the S3 placement migrates a cadre — the
// old node unmanages it (removed, user kept), the new node applies it. Proves the S3
// agent picks up placement changes across passes.
func TestS3CadreMigration(t *testing.T) {
	requireNodes(t, node1, node2)
	const name = "its3mig"
	t.Cleanup(func() { cleanupCadre(t, name, node1, node2) })

	s := startS3Store(t)
	s.seedCadre(t, name)
	s.write(t, "placement.yml", "placements:\n  "+name+": "+node1+"\n")
	s.prepare(t, node1, node2)

	if r := agentRun(t, node1); r.code != 0 || !strings.Contains(r.stdout, "applied=1") {
		t.Fatalf("initial %s: want applied=1, out=%q err=%q", node1, r.stdout, r.stderr)
	}

	// Repoint the placement at node2 (rclone serves the live file).
	s.write(t, "placement.yml", "placements:\n  "+name+": "+node2+"\n")

	if r := agentRun(t, node1); r.code != 0 || !strings.Contains(r.stdout, "removed=1") {
		t.Fatalf("migrate %s: want removed=1, out=%q err=%q", node1, r.stdout, r.stderr)
	}
	if u := nodeSudo(t, node1, "id", "-u", "rucher-"+name); u.code != 0 {
		t.Fatalf("cadre user must be retained on %s after unmanage", node1)
	}
	if r := agentRun(t, node2); r.code != 0 || !strings.Contains(r.stdout, "applied=1") {
		t.Fatalf("migrate %s: want applied=1, out=%q err=%q", node2, r.stdout, r.stderr)
	}
}
