package repository

import (
	"context"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/go-github/github"
	"golang.org/x/oauth2"

	"github.com/3box/pipeline-tools/cd/manager"
)

var _ manager.Repository = &Github{}

type Github struct {
	client *github.Client
}

func NewRepository() manager.Repository {
	var httpClient *http.Client = nil
	if accessToken, found := os.LookupEnv("GITHUB_ACCESS_TOKEN"); found {
		ts := oauth2.StaticTokenSource(
			&oauth2.Token{AccessToken: accessToken},
		)
		httpClient = oauth2.NewClient(context.Background(), ts)
	}
	return &Github{github.NewClient(httpClient)}
}

func (g Github) GetLatestCommitHash(repo manager.DeployRepo, branch, shaTag string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), manager.DefaultHttpWaitTime)
	defer cancel()

	if commits, resp, err := g.client.Repositories.ListCommits(ctx, manager.GitHubOrg, string(repo), &github.CommitsListOptions{
		SHA: branch,
		// We want to find the newest commit with all passed status checks so that we don't use a commit that doesn't
		// already have a corresponding Docker image in ECR, and we might as well request the maximum number of commits.
		// No need to implement pagination here - we should never need more than the first 100 commits to find an
		// eligible commit, and if we do then there's something else seriously wrong.
		ListOptions: github.ListOptions{PerPage: 100},
	}); err != nil {
		return "", err
	} else {
		log.Printf("getLatestCommitHash: list commits rate limit=%d, remaining=%d, resetAt=%s", resp.Limit, resp.Remaining, resp.Reset)
		for _, commit := range commits {
			sha := *commit.SHA
			if checksPassed, err := g.checkRefStatus(repo, sha); err != nil {
				return "", err
			} else if checksPassed { // Return the newest commit with passed checks
				return sha, nil
			} else if strings.HasPrefix(sha, shaTag) { // Return the commit for which the job was created
				return sha, nil
			}
		}
		// Return the newest commit and hope for the best. There will be at least one commit in each primary branch of
		// our repositories so this is safe to do.
		return *commits[0].SHA, nil
	}
}

func (g Github) checkRefStatus(repo manager.DeployRepo, ref string) (bool, error) {
	getRefStatus := func() (*github.CombinedStatus, error) {
		ctx, cancel := context.WithTimeout(context.Background(), manager.DefaultHttpWaitTime)
		defer cancel()

		status, resp, err := g.client.Repositories.GetCombinedStatus(ctx, manager.GitHubOrg, string(repo), ref, &github.ListOptions{PerPage: 100})
		log.Printf("checkRefStatus: status=%s, rate limit=%d, remaining=%d, resetAt=%s", status, resp.Limit, resp.Remaining, resp.Reset)
		return status, err
	}
	// Wait a few minutes for the status to finalize if it is currently "pending"
	now := time.Now()
	for time.Now().Before(now.Add(manager.DefaultWaitTime)) {
		if status, err := getRefStatus(); err != nil {
			return false, err
		} else {
			// Return immediately for success/failure statuses
			switch *status.State {
			case manager.CommitStatus_Success:
				// Make sure that image verification has run. We could reach here after CircleCI tests have passed but
				// image verification has not started yet, and so the combined status would appear to be successful.
				for _, statusCheck := range status.Statuses {
					if *statusCheck.Context == manager.ImageVerificationStatusCheck {
						return true, nil
					}
				}
			case manager.CommitStatus_Failure:
				return false, nil
			}
		}
		// Sleep for a few seconds so we don't get rate limited
		time.Sleep(manager.DefaultTick)
	}
	return false, nil
}
