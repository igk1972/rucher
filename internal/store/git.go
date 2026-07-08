// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	gossh "golang.org/x/crypto/ssh"
)

type Git struct {
	URL             string
	Branch          string
	CachePath       string
	SSHKey          string // path to a private key for git-over-ssh (optional)
	Token           string // token for https basic auth (optional)
	User            string // https basic-auth username (GitLab wants "oauth2"); defaults to "git"
	InsecureHostKey bool   // skip SSH host-key verification (see auth)
}

// httpsUsername resolves the https basic-auth username, defaulting to "git"
// (GitHub's convention) when none is configured.
func httpsUsername(user string) string {
	if user == "" {
		return "git"
	}
	return user
}

func (g Git) auth() (transport.AuthMethod, error) {
	switch {
	case g.SSHKey != "":
		pk, err := gitssh.NewPublicKeysFromFile("git", g.SSHKey, "")
		if err != nil {
			return nil, err
		}
		if g.InsecureHostKey {
			// Controlled deployments may spin up fresh nodes that never pre-seed
			// known_hosts, so the default callback would fail the first clone.
			pk.HostKeyCallback = gossh.InsecureIgnoreHostKey()
		}
		return pk, nil
	case g.Token != "":
		return &githttp.BasicAuth{Username: httpsUsername(g.User), Password: g.Token}, nil
	default:
		return nil, nil
	}
}

// cacheMatches reports whether the existing cache was cloned from the URL and
// branch currently configured. A pull only ever refetches the origin recorded at
// clone time, so a mismatch means the operator changed store.url/branch and the
// cache must be rebuilt rather than pulled.
func (g Git) cacheMatches(repo *git.Repository) bool {
	remote, err := repo.Remote(git.DefaultRemoteName)
	if err != nil {
		return false
	}
	urls := remote.Config().URLs
	if len(urls) == 0 || urls[0] != g.URL {
		return false
	}
	head, err := repo.Head()
	if err != nil {
		return false
	}
	return head.Name() == plumbing.NewBranchReferenceName(g.Branch)
}

func (g Git) Sync(ctx context.Context) (string, string, error) {
	auth, err := g.auth()
	if err != nil {
		return "", "", fmt.Errorf("git auth: %w", err)
	}
	ref := plumbing.NewBranchReferenceName(g.Branch)

	clone := func() (*git.Repository, error) {
		return git.PlainCloneContext(ctx, g.CachePath, false, &git.CloneOptions{
			URL:           g.URL,
			ReferenceName: ref,
			SingleBranch:  true,
			Auth:          auth,
		})
	}

	cloned := false
	repo, err := git.PlainOpen(g.CachePath)
	switch {
	case errors.Is(err, git.ErrRepositoryNotExists):
		repo, err = clone()
		cloned = true
	case err == nil && !g.cacheMatches(repo):
		// The cache points at a different store than currently configured; drop it and
		// re-clone so we honor the new URL/branch. This is a deliberate reconfigure, not
		// a transient fetch failure, so we intentionally do not fall back to last-good.
		if rmErr := os.RemoveAll(g.CachePath); rmErr != nil {
			return "", "", fmt.Errorf("git reset cache: %w", rmErr)
		}
		repo, err = clone()
		cloned = true
	}
	if err != nil {
		return "", "", fmt.Errorf("git open/clone: %w", err)
	}

	// A fresh clone already fetched the branch, so pulling again is a redundant round-trip.
	if !cloned {
		wt, err := repo.Worktree()
		if err != nil {
			return "", "", err
		}
		err = wt.PullContext(ctx, &git.PullOptions{ReferenceName: ref, SingleBranch: true, Auth: auth})
		if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
			// Last-good: if a valid checkout already exists, keep running on it rather than
			// stopping reconciliation on a transient fetch failure.
			if head, herr := repo.Head(); herr == nil {
				return g.CachePath, head.Hash().String(), nil
			}
			return "", "", fmt.Errorf("git pull: %w", err)
		}
	}
	head, err := repo.Head()
	if err != nil {
		return "", "", err
	}
	return g.CachePath, head.Hash().String(), nil
}
