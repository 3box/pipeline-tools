package notifs

import (
	"fmt"
	"os"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/webhook"

	"github.com/3box/pipeline-tools/cd/manager"
	"github.com/3box/pipeline-tools/cd/manager/common/job"
)

var _ jobNotif = &anchorNotif{}

type anchorNotif struct {
	state          job.JobState
	alertWebhook   webhook.Client
	warningWebhook webhook.Client
	infoWebhook    webhook.Client
	region         string
	env            string
}

func newAnchorNotif(jobState job.JobState) (jobNotif, error) {
	if a, err := parseDiscordWebhookUrl("DISCORD_ALERT_WEBHOOK"); err != nil {
		return nil, err
	} else if w, err := parseDiscordWebhookUrl("DISCORD_WARNING_WEBHOOK"); err != nil {
		return nil, err
	} else if i, err := parseDiscordWebhookUrl("DISCORD_INFO_WEBHOOK"); err != nil {
		return nil, err
	} else {
		return &anchorNotif{
			jobState,
			a,
			w,
			i,
			os.Getenv("AWS_REGION"),
			os.Getenv(manager.EnvVar_Env),
		}, nil
	}
}

func (a anchorNotif) getChannels() []webhook.Client {
	webhooks := make([]webhook.Client, 0, 1)
	switch a.state.Stage {
	case job.JobStage_Started:
		webhooks = append(webhooks, a.infoWebhook)
	case job.JobStage_Completed:
		webhooks = append(webhooks, a.infoWebhook)
	case job.JobStage_Failed:
		webhooks = append(webhooks, a.alertWebhook)
	}
	return nil
}

func (a anchorNotif) getTitle() string {
	return fmt.Sprintf("Anchor Worker %s", strings.ToUpper(string(a.state.Stage)))
}

func (a anchorNotif) getFields() []discord.EmbedField {
	return nil
}

func (a anchorNotif) getColor() discordColor {
	return colorForStage(a.state.Stage)
}

func (a anchorNotif) getUrl() string {
	if taskId, found := a.state.Params[job.JobParam_Id].(string); found {
		idParts := strings.Split(taskId, "/")
		return fmt.Sprintf(
			"https://%s.console.aws.amazon.com/cloudwatch/home?region=%s#logsV2:log-groups/log-group/$252Fecs$252Fceramic-%s-cas/log-events/cas_anchor$252Fcas_anchor$252F%s",
			a.region,
			a.region,
			a.env,
			idParts[len(idParts)-1],
		)
	}
	return ""
}
