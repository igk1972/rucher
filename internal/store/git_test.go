package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// makeSourceRepo creates a real repo on disk with one commit and returns its path.
func makeSourceRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "placement.yml"), []byte("placements: {web: node-a}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wt, _ := repo.Worktree()
	wt.Add("placement.yml")
	_, err = wt.Commit("init", &git.CommitOptions{Author: &object.Signature{Name: "t", Email: "t@t"}})
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestHTTPSUsername(t *testing.T) {
	if got := httpsUsername(""); got != "git" {
		t.Fatalf("httpsUsername(%q) = %q, want %q", "", got, "git")
	}
	if got := httpsUsername("oauth2"); got != "oauth2" {
		t.Fatalf("httpsUsername(%q) = %q, want %q", "oauth2", got, "oauth2")
	}
}

func TestGitSyncClonesThenPulls(t *testing.T) {
	src := makeSourceRepo(t)
	g := Git{URL: src, Branch: "master", CachePath: filepath.Join(t.TempDir(), "cache")}

	co, rev1, err := g.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(co, "placement.yml")); err != nil {
		t.Fatalf("checkout missing placement.yml: %v", err)
	}
	if rev1 == "" {
		t.Fatal("empty revision")
	}

	// add a second commit to the source, then Sync should pull it
	repo, _ := git.PlainOpen(src)
	wt, _ := repo.Worktree()
	os.WriteFile(filepath.Join(src, "placement.yml"), []byte("placements: {web: node-b}\n"), 0o644)
	wt.Add("placement.yml")
	wt.Commit("update", &git.CommitOptions{Author: &object.Signature{Name: "t", Email: "t@t"}})

	_, rev2, err := g.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rev2 == rev1 {
		t.Fatal("Sync did not pull the new commit")
	}
	got, _ := os.ReadFile(filepath.Join(co, "placement.yml"))
	if string(got) != "placements: {web: node-b}\n" {
		t.Fatalf("checkout not updated: %q", got)
	}
}

func TestGitSyncFallsBackToCachedCheckout(t *testing.T) {
	src := makeSourceRepo(t)
	g := Git{URL: src, Branch: "master", CachePath: filepath.Join(t.TempDir(), "cache")}

	_, rev1, err := g.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// Make the remote unreachable; a subsequent pull must fail.
	if err := os.RemoveAll(src); err != nil {
		t.Fatal(err)
	}

	co2, rev2, err := g.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync should fall back to the cached checkout, got: %v", err)
	}
	if rev2 != rev1 {
		t.Fatalf("revision changed on fallback: %q != %q", rev2, rev1)
	}
	if _, err := os.Stat(filepath.Join(co2, "placement.yml")); err != nil {
		t.Fatalf("cached checkout missing placement.yml: %v", err)
	}
}
