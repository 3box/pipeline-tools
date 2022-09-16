package repository

import (
	"context"

	"github.com/google/go-github/github"

	"github.com/3box/pipeline-tools/cd/manager"
)

var _ manager.Repository = &Github{}

type Github struct {
	client *github.Client
}

func NewRepository() manager.Repository {
	return &Github{github.NewClient(nil)}
}

func (g Github) GetLatestCommitHash(repo manager.DeployRepo, branch string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), manager.DefaultHttpWaitTime)
	defer cancel()

	if commits, _, err := g.client.Repositories.ListCommits(ctx, manager.GitHubOrg, string(repo), &github.CommitsListOptions{
		SHA:         branch,
		ListOptions: github.ListOptions{PerPage: 1},
	}); err != nil {
		return "", err
	} else {
		// There will be at least one commit in each primary branch of our repositories so this is safe to do.
		return *commits[0].SHA, nil
	}
}
