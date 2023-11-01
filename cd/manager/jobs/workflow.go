package jobs

import (
	"context"
	"fmt"
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
var _ manager.JobSm = &githubWorkflowJob{}

type githubWorkflowJob struct {
	baseJob
	workflow manager.Workflow
	env      string
	client   *github.Client
	r        manager.Repository
}

func GitHubWorkflowJob(jobState job.JobState, db manager.Database, notifs manager.Notifs, r manager.Repository) (manager.JobSm, error) {
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
		inputs[job.WorkflowJobParam_JobId] = jobState.JobId
		// Set the environment so that the workflow knows which environment to target
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
			manager.Workflow{org, repo, workflow, ref, inputs},
			env,
			github.NewClient(httpClient),
			r,
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
			if err := w.r.StartWorkflow(w.workflow); err != nil {
				return w.advance(job.JobStage_Failed, now, err)
			} else {
				w.state.Params[job.JobParam_Start] = float64(time.Now().UnixNano())
				return w.advance(job.JobStage_Started, now, nil)
			}
		}
	case job.JobStage_Started:
		{
			// The start time should have been filled in by this point. Limit the search to runs after the start of the
			// job (minus 30 seconds, so we avoid any races).
			searchTime := time.Unix(0, int64(w.state.Params[job.JobParam_Start].(float64))).Add(-30 * time.Second)
			if workflowRunId, workflowRunUrl, err := w.r.FindMatchingWorkflowRun(w.workflow, w.state.JobId, searchTime); err != nil {
				return w.advance(job.JobStage_Failed, now, err)
			} else if workflowRunId != -1 {
				// Record workflow details and advance the job
				w.state.Params[job.JobParam_Id] = float64(workflowRunId)
				w.state.Params[job.WorkflowJobParam_Url] = workflowRunUrl
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
			// The workflow run ID should have been filled in by this point
			workflowRunId, _ := w.state.Params[job.JobParam_Id].(float64)
			if status, err := w.r.CheckWorkflowStatus(w.workflow, int64(workflowRunId)); err != nil {
				return w.advance(job.JobStage_Failed, now, err)
			} else if status == manager.WorkflowStatus_Success {
				return w.advance(job.JobStage_Completed, now, nil)
			} else if status == manager.WorkflowStatus_Failure {
				return w.advance(job.JobStage_Failed, now, nil)
			} else if status == manager.WorkflowStatus_Canceled {
				return w.advance(job.JobStage_Canceled, now, nil)
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
