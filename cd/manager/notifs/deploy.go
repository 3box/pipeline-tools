package notifs

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/webhook"

	"github.com/3box/pipeline-tools/cd/manager"
	"github.com/3box/pipeline-tools/cd/manager/common/job"
)

var _ jobNotif = &deployNotif{}

type deployNotif struct {
	state              job.JobState
	deploymentsWebhook webhook.Client
	communityWebhook   webhook.Client
	env                manager.EnvType
}

const (
	envName_Dev  string = "dev"
	envName_Qa   string = "dev-qa"
	envName_Tnet string = "testnet-clay"
	envName_Prod string = "mainnet"
)

func newDeployNotif(jobState job.JobState) (jobNotif, error) {
	if d, err := parseDiscordWebhookUrl("DISCORD_DEPLOYMENTS_WEBHOOK"); err != nil {
		return nil, err
	} else if c, err := parseDiscordWebhookUrl("DISCORD_COMMUNITY_NODES_WEBHOOK"); err != nil {
		return nil, err
	} else {
		return &deployNotif{jobState, d, c, manager.EnvType(os.Getenv(manager.EnvVar_Env))}, nil
	}
}

func (d deployNotif) getChannels() []webhook.Client {
	webhooks := []webhook.Client{d.deploymentsWebhook}
	// Don't send Dev/QA notifications to the community channel
	if (d.env != manager.EnvType_Dev) && (d.env != manager.EnvType_Qa) {
		webhooks = append(webhooks, d.communityWebhook)
	}
	return webhooks
}

func (d deployNotif) getTitle() string {
	component := d.state.Params[job.DeployJobParam_Component].(string)
	qualifier := ""
	// A rollback is always a force job, while a non-rollback force job is always manual, so we can optimize.
	if rollback, _ := d.state.Params[job.DeployJobParam_Rollback].(bool); rollback {
		qualifier = job.DeployJobParam_Rollback
	} else if force, _ := d.state.Params[job.DeployJobParam_Force].(bool); force {
		qualifier = job.DeployJobParam_Force
	} else if manual, _ := d.state.Params[job.DeployJobParam_Manual].(bool); manual {
		qualifier = job.DeployJobParam_Manual
	}
	return fmt.Sprintf(
		"3Box Labs `%s` %s %s %s %s",
		envName(d.env),
		strings.ToUpper(component),
		cases.Title(language.English).String(qualifier),
		"Deployment",
		strings.ToUpper(string(d.state.Stage)),
	)
}

func (d deployNotif) getFields() []discord.EmbedField {
	return nil
}

func (d deployNotif) getColor() discordColor {
	return colorForStage(d.state.Stage)
}

func envName(env manager.EnvType) string {
	switch env {
	case manager.EnvType_Dev:
		return envName_Dev
	case manager.EnvType_Qa:
		return envName_Qa
	case manager.EnvType_Tnet:
		return envName_Tnet
	case manager.EnvType_Prod:
		return envName_Prod
	default:
		return ""
	}
}

func (d deployNotif) getUrl() string {
	return ""
}
