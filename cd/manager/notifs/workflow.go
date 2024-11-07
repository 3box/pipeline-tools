package notifs

import (
	"fmt"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/webhook"

	"github.com/3box/pipeline-tools/cd/manager/common/job"
)

var _ jobNotif = &workflowNotif{}

const defaultWorkflowJobName = "Workflow"
const (
	workflowNotifField_Branch       = "Branch"
	workflowNotifField_TestSelector = "Test Selector"
)

type workflowNotif struct {
	state           job.JobState
	workflow        job.Workflow
	webhooks        []webhook.Client
	failureWebhooks []webhook.Client
}

func newWorkflowNotif(jobState job.JobState) (jobNotif, error) {
	if workflow, err := job.CreateWorkflowJob(jobState); err != nil {
		return nil, err
	} else if webhooks, failureWebhooks, err := webhooksForLabels(workflow.Labels); err != nil {
		return nil, err
	} else {
		return &workflowNotif{jobState, workflow, webhooks, failureWebhooks}, nil
	}
}

func (w workflowNotif) getChannels() []webhook.Client {
	if w.state.Stage == job.JobStage_Failed {
		// Send to both the normal and failure webhooks
		return append(w.webhooks, w.failureWebhooks...)
	} else
	// Skip "started" notifications so that the channel doesn't get too noisy
	if w.state.Stage != job.JobStage_Started {
		return w.webhooks
	}
	return nil
}

func (w workflowNotif) getTitle() string {
	jobName := defaultWorkflowJobName
	if workflowName, found := w.state.Params[job.WorkflowJobParam_Name].(string); found {
		jobName = workflowName
	}
	prettyStage := string(w.state.Stage)
	if w.state.Stage == job.JobStage_Dequeued {
		prettyStage = prettyStageDequeued
	}
	return fmt.Sprintf("%s %s", jobName, strings.ToUpper(prettyStage))
}

func (w workflowNotif) getFields() []discord.EmbedField {
	notifFields := []discord.EmbedField{
		{
			Name:  workflowNotifField_Branch,
			Value: fmt.Sprintf("[%s (%s)](https://github.com/%s/%s/tree/%s)", w.workflow.Repo, w.workflow.Ref, w.workflow.Org, w.workflow.Repo, w.workflow.Ref),
		},
	}
	// If this is a test workflow, also report the test selector.
	if testSelector, found := w.workflow.Inputs[job.WorkflowJobParam_TestSelector].(string); found {
		notifFields = append(
			notifFields,
			discord.EmbedField{
				Name:  workflowNotifField_TestSelector,
				Value: testSelector,
			},
		)
	}
	return notifFields
}

func (w workflowNotif) getColor() discordColor {
	return colorForStage(w.state.Stage)
}

func (w workflowNotif) getUrl() string {
	return w.workflow.Url
}
