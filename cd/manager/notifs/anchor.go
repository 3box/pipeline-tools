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
	region         string
	env            string
}

func newAnchorNotif(jobState job.JobState) (jobNotif, error) {
	if a, err := parseDiscordWebhookUrl("DISCORD_ALERT_WEBHOOK"); err != nil {
		return nil, err
	} else if w, err := parseDiscordWebhookUrl("DISCORD_WARNING_WEBHOOK"); err != nil {
		return nil, err
	} else {
		return &anchorNotif{
			jobState,
			a,
			w,
			os.Getenv("AWS_REGION"),
			os.Getenv(manager.EnvVar_Env),
		}, nil
	}
}

func (a anchorNotif) getChannels() []webhook.Client {
	// We only care about "waiting" notifications from the CD manager for the time being. Other notifications are sent
	// directly from the anchor worker.
	if a.state.Stage == job.JobStage_Waiting {
		webhooks := make([]webhook.Client, 0, 1)
		if stalled, _ := a.state.Params[job.AnchorJobParam_Stalled].(bool); stalled {
			webhooks = append(webhooks, a.alertWebhook)
		} else if delayed, _ := a.state.Params[job.AnchorJobParam_Delayed].(bool); delayed {
			webhooks = append(webhooks, a.warningWebhook)
		}
	}
	return nil
}

func (a anchorNotif) getTitle() string {
	jobStageRepr := string(a.state.Stage)
	// If "waiting", update the job stage representation to qualify the severity of the delay, if applicable.
	if a.state.Stage == job.JobStage_Waiting {
		if stalled, _ := a.state.Params[job.AnchorJobParam_Stalled].(bool); stalled {
			jobStageRepr = job.AnchorJobParam_Stalled
		} else if delayed, _ := a.state.Params[job.AnchorJobParam_Delayed].(bool); delayed {
			jobStageRepr = job.AnchorJobParam_Delayed
		}
	}
	return fmt.Sprintf("Anchor Worker %s", strings.ToUpper(jobStageRepr))
}

func (a anchorNotif) getFields() []discord.EmbedField {
	return nil
}

func (a anchorNotif) getColor() discordColor {
	if a.state.Stage == job.JobStage_Waiting {
		if stalled, _ := a.state.Params[job.AnchorJobParam_Stalled].(bool); stalled {
			return discordColor_Alert
		} else if delayed, _ := a.state.Params[job.AnchorJobParam_Delayed].(bool); delayed {
			return discordColor_Warning
		}
	}
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
