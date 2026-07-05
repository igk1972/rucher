package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
)

type Git struct {
	URL       string
	Branch    string
	CachePath string
	SSHKey    string // path to a private key for git-over-ssh (optional)
	Token     string // token for https basic auth (optional)
}

func (g Git) auth() (transport.AuthMethod, error) {
	switch {
	case g.SSHKey != "":
		return gitssh.NewPublicKeysFromFile("git", g.SSHKey, "")
	case g.Token != "":
		return &githttp.BasicAuth{Username: "git", Password: g.Token}, nil
	default:
		return nil, nil
	}
}

func (g Git) Sync(ctx context.Context) (string, string, error) {
	auth, err := g.auth()
	if err != nil {
		return "", "", fmt.Errorf("git auth: %w", err)
	}
	ref := plumbing.NewBranchReferenceName(g.Branch)

	repo, err := git.PlainOpen(g.CachePath)
	if errors.Is(err, git.ErrRepositoryNotExists) {
		repo, err = git.PlainCloneContext(ctx, g.CachePath, false, &git.CloneOptions{
			URL:           g.URL,
			ReferenceName: ref,
			SingleBranch:  true,
			Auth:          auth,
		})
	}
	if err != nil {
		return "", "", fmt.Errorf("git open/clone: %w", err)
	}

	wt, err := repo.Worktree()
	if err != nil {
		return "", "", err
	}
	err = wt.PullContext(ctx, &git.PullOptions{ReferenceName: ref, SingleBranch: true, Auth: auth})
	if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		return "", "", fmt.Errorf("git pull: %w", err)
	}
	head, err := repo.Head()
	if err != nil {
		return "", "", err
	}
	return g.CachePath, head.Hash().String(), nil
}
