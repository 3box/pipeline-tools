package repository

import (
	"context"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/google/go-github/v56/github"

	"github.com/3box/pipeline-tools/cd/manager"
)

var _ manager.Repository = &Github{}

const (
	GitHub_WorkflowEventType     = "workflow_dispatch"
	GitHub_WorkflowTimeFormat    = "2006-01-02T15:04:05.000Z" // ISO8601
	GitHub_WorkflowJobId         = "job_id"
	GitHub_WorkflowStatusSuccess = "success"
	GitHub_WorkflowStatusFailure = "failure"
)

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
		log.Printf("getLatestCommitHash: list commits rate limit=%d, remaining=%d, resetAt=%s", resp.Rate.Limit, resp.Rate.Remaining, resp.Rate.Reset)
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
		log.Printf("checkRefStatus: status=%s, rate limit=%d, remaining=%d, resetAt=%s", status, resp.Rate.Limit, resp.Rate.Remaining, resp.Rate.Reset)
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

func (g Github) StartWorkflow(workflow manager.Workflow) error {
	ctx, cancel := context.WithTimeout(context.Background(), manager.DefaultHttpWaitTime)
	defer cancel()

	resp, err := g.client.Actions.CreateWorkflowDispatchEventByFileName(
		ctx, workflow.Org, workflow.Repo, workflow.Workflow, github.CreateWorkflowDispatchEventRequest{
			Ref:    workflow.Ref,
			Inputs: workflow.Inputs,
		})
	if err != nil {
		return err
	}
	log.Printf("startWorkflow: rate limit=%d, remaining=%d, resetAt=%s", resp.Rate.Limit, resp.Rate.Remaining, resp.Rate.Reset)
	return nil
}

func (g Github) FindMatchingWorkflowRun(workflow manager.Workflow, jobId string, searchTime time.Time) (*int64, error) {
	if workflowRuns, count, err := g.getWorkflowRuns(workflow, searchTime); err != nil {
		return nil, err
	} else if count > 0 {
		for _, workflowRun := range workflowRuns {
			if workflowJobs, count, err := g.getWorkflowJobs(workflow.Org, workflow.Repo, workflowRun); err != nil {
				return nil, err
			} else if count > 0 {
				for _, workflowJob := range workflowJobs {
					for _, jobStep := range workflowJob.Steps {
						// If we found a job step with our job ID, then we know this is the workflow we're looking for
						// and need to monitor.
						if jobStep.GetName() == jobId {
							workflowRunId := workflowRun.GetID()
							return &workflowRunId, nil
						}
					}
				}
			}
		}
	}
	return nil, nil
}

func (g Github) getWorkflowRuns(workflow manager.Workflow, searchTime time.Time) ([]*github.WorkflowRun, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), manager.DefaultHttpWaitTime)
	defer cancel()

	if workflows, resp, err := g.client.Actions.ListWorkflowRunsByFileName(
		ctx, workflow.Org, workflow.Repo, workflow.Workflow, &github.ListWorkflowRunsOptions{
			Branch: workflow.Ref,
			Event:  GitHub_WorkflowEventType,
			// The time format assumes UTC, so we make sure to use the corresponding UTC time for the search.
			Created:             ">" + searchTime.UTC().Format(GitHub_WorkflowTimeFormat),
			ExcludePullRequests: true,
		}); err != nil {
		return nil, 0, err
	} else {
		log.Printf("getWorkflowRuns: runs=%d, rate limit=%d, remaining=%d, resetAt=%s", *workflows.TotalCount, resp.Rate.Limit, resp.Rate.Remaining, resp.Rate.Reset)
		return workflows.WorkflowRuns, *workflows.TotalCount, nil
	}
}

func (g Github) getWorkflowJobs(org, repo string, workflowRun *github.WorkflowRun) ([]*github.WorkflowJob, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), manager.DefaultHttpWaitTime)
	defer cancel()

	if jobs, resp, err := g.client.Actions.ListWorkflowJobs(ctx, org, repo, workflowRun.GetID(), nil); err != nil {
		return nil, 0, err
	} else {
		log.Printf("getWorkflowJobs: run=%s jobs=%d, rate limit=%d, remaining=%d, resetAt=%s", workflowRun.GetHTMLURL(), *jobs.TotalCount, resp.Rate.Limit, resp.Rate.Remaining, resp.Rate.Reset)
		return jobs.Jobs, *jobs.TotalCount, nil
	}
}

func (g Github) CheckWorkflowStatus(workflow manager.Workflow, workflowRunId int64) (bool, bool, error) {
	if workflowRun, err := g.getWorkflowRun(workflow.Org, workflow.Repo, workflowRunId); err != nil {
		return false, false, err
	} else if workflowRun.GetConclusion() == GitHub_WorkflowStatusSuccess {
		return true, true, nil
	} else if workflowRun.GetConclusion() == GitHub_WorkflowStatusFailure {
		return false, true, nil
	} else {
		return false, false, nil // Still in progress
	}
}

func (g Github) getWorkflowRun(org, repo string, workflowRunId int64) (*github.WorkflowRun, error) {
	ctx, cancel := context.WithTimeout(context.Background(), manager.DefaultHttpWaitTime)
	defer cancel()

	if workflowRun, resp, err := g.client.Actions.GetWorkflowRunByID(ctx, org, repo, workflowRunId); err != nil {
		return nil, err
	} else {
		log.Printf("getWorkflowRun: run=%s, rate limit=%d, remaining=%d, resetAt=%s", workflowRun.GetHTMLURL(), resp.Rate.Limit, resp.Rate.Remaining, resp.Rate.Reset)
		return workflowRun, nil
	}
}
