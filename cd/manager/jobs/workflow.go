package jobs

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"golang.org/x/oauth2"

	"github.com/google/go-github/v56/github"

	"github.com/3box/pipeline-tools/cd/manager"
	"github.com/3box/pipeline-tools/cd/manager/common/job"
)

// Allow up to 4 hours for a workflow to run
const workflowFailureTime = 4 * time.Hour

// GitHub constants
const (
	gitHub_WorkflowEventType  = "workflow_dispatch"
	gitHub_WorkflowTimeFormat = "2006-01-02T15:04:05.000Z" // ISO8601
	gitHub_WorkflowJobId      = "job_id"
)
const (
	gitHub_WorkflowStatus_Success = "success"
	gitHub_WorkflowStatus_Failure = "failure"
)

var _ manager.JobSm = &githubWorkflowJob{}

type githubWorkflowJob struct {
	baseJob
	client   *github.Client
	org      string
	repo     string
	ref      string
	workflow string
	inputs   map[string]interface{}
	env      string
}

func GitHubWorkflowJob(jobState job.JobState, db manager.Database, notifs manager.Notifs) (manager.JobSm, error) {
	if org, found := jobState.Params[job.WorkflowJobParam_Org].(string); !found {
		return nil, fmt.Errorf("githubWorkflowJob: missing org")
	} else if repo, found := jobState.Params[job.WorkflowJobParam_Repo].(string); !found {
		return nil, fmt.Errorf("githubWorkflowJob: missing repo")
	} else if ref, found := jobState.Params[job.WorkflowJobParam_Ref].(string); !found {
		return nil, fmt.Errorf("githubWorkflowJob: missing ref")
	} else if workflow, found := jobState.Params[job.WorkflowJobParam_Workflow].(string); !found {
		return nil, fmt.Errorf("githubWorkflowJob: missing workflow")
	} else {
		inputs, _ := jobState.Params[job.WorkflowJobParam_Inputs].(map[string]interface{})
		if len(inputs) == 0 {
			inputs = make(map[string]interface{}, 1)
		}
		// Add the job ID to the inputs, so we can track the right workflow corresponding to this job.
		inputs[gitHub_WorkflowJobId] = jobState.Job
		// Set the environment - it's ok to override it even if it was already set.
		env := os.Getenv("ENV")
		inputs[job.WorkflowJobParam_Environment] = env

		var httpClient *http.Client = nil
		if accessToken, found := os.LookupEnv("GITHUB_ACCESS_TOKEN"); found {
			ts := oauth2.StaticTokenSource(
				&oauth2.Token{AccessToken: accessToken},
			)
			httpClient = oauth2.NewClient(context.Background(), ts)
		}

		return &githubWorkflowJob{
			baseJob{jobState, db, notifs},
			github.NewClient(httpClient),
			org,
			repo,
			ref,
			workflow,
			inputs,
			env,
		}, nil
	}
}

func (w githubWorkflowJob) Advance() (job.JobState, error) {
	now := time.Now()
	switch w.state.Stage {
	case job.JobStage_Queued:
		{
			// No preparation needed so advance the job directly to "dequeued".
			//
			// Advance the timestamp by a tiny amount so that the "dequeued" event remains at the same position on the
			// timeline as the "queued" event but still ahead of it.
			return w.advance(job.JobStage_Dequeued, w.state.Ts.Add(time.Nanosecond), nil)
		}
	case job.JobStage_Dequeued:
		{
			if err := w.startWorkflow(); err != nil {
				return w.advance(job.JobStage_Failed, now, err)
			} else {
				w.state.Params[job.JobParam_Start] = float64(time.Now().UnixNano())
				return w.advance(job.JobStage_Started, now, nil)
			}
		}
	case job.JobStage_Started:
		{
			if workflowRun, err := w.findMatchingWorkflowRun(); err != nil {
				return w.advance(job.JobStage_Failed, now, err)
			} else if workflowRun != nil {
				// Record workflow details and advance the job
				w.state.Params[job.JobParam_Id] = float64(workflowRun.GetID())
				w.state.Params[job.WorkflowJobParam_Url] = workflowRun.GetHTMLURL()
				return w.advance(job.JobStage_Waiting, now, nil)
			} else if job.IsTimedOut(w.state, manager.DefaultWaitTime) { // Workflow did not start in time
				return w.advance(job.JobStage_Failed, now, manager.Error_StartupTimeout)
			} else {
				// Return so we come back again to check
				return w.state, nil
			}
		}
	case job.JobStage_Waiting:
		{
			if status, err := w.checkWorkflowStatus(); err != nil {
				return w.advance(job.JobStage_Failed, now, err)
			} else if status == gitHub_WorkflowStatus_Success {
				return w.advance(job.JobStage_Completed, now, nil)
			} else if status == gitHub_WorkflowStatus_Failure {
				return w.advance(job.JobStage_Failed, now, nil)
			} else if job.IsTimedOut(w.state, workflowFailureTime) { // Workflow did not finish in time
				return w.advance(job.JobStage_Failed, now, manager.Error_CompletionTimeout)
			} else {
				// Return so we come back again to check
				return w.state, nil
			}
		}
	default:
		{
			return w.advance(job.JobStage_Failed, now, fmt.Errorf("githubWorkflowJob: unexpected state: %w", manager.PrintJob(w.state)))
		}
	}
}

func (w githubWorkflowJob) startWorkflow() error {
	ctx, cancel := context.WithTimeout(context.Background(), manager.DefaultHttpWaitTime)
	defer cancel()

	resp, err := w.client.Actions.CreateWorkflowDispatchEventByFileName(
		ctx, w.org, w.repo, w.workflow, github.CreateWorkflowDispatchEventRequest{
			Ref:    w.ref,
			Inputs: w.inputs,
		})
	if err != nil {
		return err
	}
	log.Printf("startWorkflow: rate limit=%d, remaining=%d, resetAt=%s", resp.Rate.Limit, resp.Rate.Remaining, resp.Rate.Reset)
	return nil
}

func (w githubWorkflowJob) findMatchingWorkflowRun() (*github.WorkflowRun, error) {
	if workflowRuns, count, err := w.getWorkflowRuns(); err != nil {
		return nil, err
	} else if count > 0 {
		for _, workflowRun := range workflowRuns {
			if workflowJobs, count, err := w.getWorkflowJobs(workflowRun); err != nil {
				return nil, err
			} else if count > 0 {
				for _, workflowJob := range workflowJobs {
					for _, jobStep := range workflowJob.Steps {
						// If we found a job step with our job ID, then we know this is the workflow we're looking for
						// and need to monitor.
						if jobStep.GetName() == w.state.Job {
							return workflowRun, nil
						}
					}
				}
			}
		}
	}
	return nil, nil
}

func (w githubWorkflowJob) getWorkflowRuns() ([]*github.WorkflowRun, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), manager.DefaultHttpWaitTime)
	defer cancel()

	// Limit the search to runs after the start of the job (minus 30 seconds, so we avoid any races).
	searchTime := time.Unix(0, int64(w.state.Params[job.JobParam_Start].(float64))).Add(-30 * time.Second)
	if workflows, resp, err := w.client.Actions.ListWorkflowRunsByFileName(
		ctx, w.org, w.repo, w.workflow, &github.ListWorkflowRunsOptions{
			Branch: w.ref,
			Event:  gitHub_WorkflowEventType,
			// The time format assumes UTC, so we make sure to use the corresponding UTC time for the search.
			Created:             ">" + searchTime.UTC().Format(gitHub_WorkflowTimeFormat),
			ExcludePullRequests: true,
		}); err != nil {
		return nil, 0, err
	} else {
		log.Printf("getWorkflowRuns: runs=%d, rate limit=%d, remaining=%d, resetAt=%s", *workflows.TotalCount, resp.Rate.Limit, resp.Rate.Remaining, resp.Rate.Reset)
		return workflows.WorkflowRuns, *workflows.TotalCount, nil
	}
}

func (w githubWorkflowJob) getWorkflowJobs(workflowRun *github.WorkflowRun) ([]*github.WorkflowJob, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), manager.DefaultHttpWaitTime)
	defer cancel()

	if jobs, resp, err := w.client.Actions.ListWorkflowJobs(ctx, w.org, w.repo, workflowRun.GetID(), nil); err != nil {
		return nil, 0, err
	} else {
		log.Printf("getWorkflowJobs: run=%s jobs=%d, rate limit=%d, remaining=%d, resetAt=%s", workflowRun.GetHTMLURL(), *jobs.TotalCount, resp.Rate.Limit, resp.Rate.Remaining, resp.Rate.Reset)
		return jobs.Jobs, *jobs.TotalCount, nil
	}
}

func (w githubWorkflowJob) checkWorkflowStatus() (string, error) {
	// The workflow run ID should have been filled in by this point
	workflowRunId, _ := w.state.Params[job.JobParam_Id].(float64)
	if workflowRun, err := w.getWorkflowRun(int64(workflowRunId)); err != nil {
		return "", err
	} else {
		return workflowRun.GetConclusion(), nil
	}
}

func (w githubWorkflowJob) getWorkflowRun(workflowRunId int64) (*github.WorkflowRun, error) {
	ctx, cancel := context.WithTimeout(context.Background(), manager.DefaultHttpWaitTime)
	defer cancel()

	if workflowRun, resp, err := w.client.Actions.GetWorkflowRunByID(ctx, w.org, w.repo, workflowRunId); err != nil {
		return nil, err
	} else {
		log.Printf("getWorkflowRun: run=%s, rate limit=%d, remaining=%d, resetAt=%s", workflowRun.GetHTMLURL(), resp.Rate.Limit, resp.Rate.Remaining, resp.Rate.Reset)
		return workflowRun, nil
	}
}
