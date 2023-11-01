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
const workflowLogsFieldName = "Logs"

type workflowNotif struct {
	state           job.JobState
	workflowWebhook webhook.Client
}

func newWorkflowNotif(jobState job.JobState) (jobNotif, error) {
	if w, err := parseDiscordWebhookUrl("DISCORD_WORKFLOW_WEBHOOK"); err != nil {
		return nil, err
	} else {
		return &workflowNotif{jobState, w}, nil
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
	if workflowUrl, found := w.state.Params[job.WorkflowJobParam_Url].(string); found {
		repo, _ := w.state.Params[job.WorkflowJobParam_Repo].(string)
		// The workflow run ID should have been filled in by this point
		workflowRunId, _ := w.state.Params[job.JobParam_Id].(float64)
		return []discord.EmbedField{
			{
				Name:  workflowLogsFieldName,
				Value: fmt.Sprintf("[%s (%s)](%s)", repo, strconv.Itoa(int(workflowRunId)), workflowUrl),
			},
		}
	}
	return nil
}

func (w workflowNotif) getColor() discordColor {
	return getColorForStage(w.state.Stage)
}
