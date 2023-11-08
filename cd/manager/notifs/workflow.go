package notifs

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/webhook"

	"github.com/3box/pipeline-tools/cd/manager/common/job"
)

var _ jobNotif = &workflowNotif{}

const defaultWorkflowJobName = "Workflow"
const (
	workflowNotifField_Ref          = "Ref"
	workflowNotifField_TestSelector = "Test Selector"
	workflowNotifField_Logs         = "Logs"
)
const (
	workflowTestSelector_Wildcard = "."
	workflowTestSelector_All      = "all"
)

type workflowNotif struct {
	state           job.JobState
	workflow        job.Workflow
	workflowWebhook webhook.Client
}

func newWorkflowNotif(jobState job.JobState) (jobNotif, error) {
	if w, err := parseDiscordWebhookUrl("DISCORD_WORKFLOWS_WEBHOOK"); err != nil {
		return nil, err
	} else if workflow, err := job.CreateWorkflowJob(jobState); err != nil {
		return nil, err
	} else {
		return &workflowNotif{jobState, workflow, w}, nil
	}
}

func (w workflowNotif) getChannels() []webhook.Client {
	// Skip "started" notifications so that the channel doesn't get too noisy
	if w.state.Stage != job.JobStage_Started {
		return []webhook.Client{w.workflowWebhook}
	}
	return nil
}

func (w workflowNotif) getTitle() string {
	jobName := defaultWorkflowJobName
	if workflowName, found := w.state.Params[job.WorkflowJobParam_Name].(string); found {
		jobName = workflowName
	}
	return fmt.Sprintf("%s %s", jobName, strings.ToUpper(string(w.state.Stage)))
}

func (w workflowNotif) getFields() []discord.EmbedField {
	notifFields := []discord.EmbedField{
		{
			Name:  workflowNotifField_Ref,
			Value: fmt.Sprintf("[%s](https://github.com/%s/%s/tree/%s)", w.workflow.Ref, w.workflow.Org, w.workflow.Repo, w.workflow.Ref),
		},
	}
	// If this is a test workflow, also report the test selector.
	if testSelector, found := w.workflow.Inputs[job.WorkflowJobParam_TestSelector].(string); found {
		// Convert wildcard selector to "all" selector
		if testSelector == workflowTestSelector_Wildcard {
			testSelector = workflowTestSelector_All
		}
		notifFields = append(
			notifFields,
			discord.EmbedField{
				Name:  workflowNotifField_TestSelector,
				Value: testSelector,
			},
		)
	}
	if len(w.workflow.Url) > 0 {
		notifFields = append(
			notifFields,
			discord.EmbedField{
				Name:  workflowNotifField_Logs,
				Value: fmt.Sprintf("[%s (%s)](%s)", w.workflow.Repo, strconv.Itoa(int(w.workflow.Id)), w.workflow.Url),
			},
		)
	}
	return notifFields
}

func (w workflowNotif) getColor() discordColor {
	return colorForStage(w.state.Stage)
}
