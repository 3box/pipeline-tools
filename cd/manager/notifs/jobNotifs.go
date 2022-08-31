package notifs

import (
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/disgo/webhook"
	"github.com/disgoorg/snowflake/v2"

	"github.com/3box/pipeline-tools/cd/manager"
)

type DiscordColor int

const (
	DiscordColor_None    = iota
	DiscordColor_Info    = 3447003
	DiscordColor_Ok      = 3581519
	DiscordColor_Warning = 16776960
	DiscordColor_Alert   = 16711712
)

const DiscordUnknownJob = "UNKNOWN JOB"
const DiscordPacing = 2 * time.Second
const GitHubOrg = "https://github.com/ceramicnetwork"

var _ manager.Notifs = &JobNotifs{}

type JobNotifs struct {
	db                 manager.Database
	deploymentsWebhook webhook.Client
	communityWebhook   webhook.Client
	env                manager.EnvType
}

func NewJobNotifs(db manager.Database) (manager.Notifs, error) {
	if d, err := parseDiscordWebhookUrl("DISCORD_DEPLOYMENTS_WEBHOOK"); err != nil {
		return nil, err
	} else if c, err := parseDiscordWebhookUrl("DISCORD_COMMUNITY_NODES_WEBHOOK"); err != nil {
		return nil, err
	} else {
		return &JobNotifs{db, d, c, manager.EnvType(os.Getenv("ENV"))}, nil
	}
}

func parseDiscordWebhookUrl(urlEnv string) (webhook.Client, error) {
	webhookUrl := os.Getenv(urlEnv)
	if len(webhookUrl) > 0 {
		if parsedUrl, err := url.Parse(webhookUrl); err != nil {
			return nil, err
		} else {
			urlParts := strings.Split(parsedUrl.Path, "/")
			if id, err := snowflake.Parse(urlParts[len(urlParts)-2]); err != nil {
				return nil, err
			} else {
				return webhook.New(id, urlParts[len(urlParts)-1]), nil
			}
		}
	}
	return nil, nil
}

func (n JobNotifs) NotifyJob(jobs ...manager.JobState) {
	for _, jobState := range jobs {
		for _, channel := range n.getNotifChannels(jobState) {
			if channel != nil {
				n.sendNotif(
					n.getNotifTitle(jobState),
					n.getNotifDesc(jobState),
					n.getNotifColor(jobState),
					channel,
				)
			}
		}
	}
}

func (n JobNotifs) sendNotif(title, desc string, color DiscordColor, channel webhook.Client) {
	messageEmbed := discord.Embed{
		Title:       title,
		Type:        discord.EmbedTypeRich,
		Description: desc,
		Color:       int(color),
	}
	if _, err := channel.CreateMessage(discord.NewWebhookMessageCreateBuilder().
		SetEmbeds(messageEmbed).
		Build(),
		rest.WithDelay(DiscordPacing),
	); err != nil {
		log.Printf("notifyJob: error sending discord notification: %v, %s, %s, %d", err, title, desc, color)
	}
}

func (n JobNotifs) getNotifChannels(jobState manager.JobState) []webhook.Client {
	webhooks := make([]webhook.Client, 0, 1)
	if jobState.Type == manager.JobType_Deploy {
		webhooks = append(webhooks, n.deploymentsWebhook)
		// Don't send Dev/QA notifications to the community channel.
		if (n.env != manager.EnvType_Dev) && (n.env != manager.EnvType_Qa) {
			webhooks = append(webhooks, n.communityWebhook)
		}
	}
	return webhooks
}

func (n JobNotifs) getNotifTitle(jobState manager.JobState) string {
	var jobTitlePfx string
	if jobState.Type == manager.JobType_Deploy {
		component := jobState.Params[manager.EventParam_Component]
		jobTitlePfx = fmt.Sprintf("3Box Labs `%s` `%s` ", manager.EnvName(n.env), component.(string))
	}
	return fmt.Sprintf("%s%s %s", jobTitlePfx, manager.JobName(jobState.Type), strings.ToUpper(string(jobState.Stage)))
}

func (n JobNotifs) getNotifDesc(jobState manager.JobState) string {
	if jobState.Type == manager.JobType_Deploy {
		return n.getDeployHashes()
	}
	return ""
}

func (n JobNotifs) getNotifColor(jobState manager.JobState) DiscordColor {
	switch jobState.Stage {
	case manager.JobStage_Queued:
		return DiscordColor_None
	case manager.JobStage_Skipped:
		return DiscordColor_Warning
	case manager.JobStage_Started:
		return DiscordColor_None
	case manager.JobStage_Waiting:
		return DiscordColor_Info
	case manager.JobStage_Delayed:
		return DiscordColor_Warning
	case manager.JobStage_Failed:
		return DiscordColor_Alert
	case manager.JobStage_Completed:
		return DiscordColor_Ok
	default:
		log.Printf("sendNotif: unknown job stage: %s", manager.PrintJob(jobState))
		return DiscordColor_Alert
	}
}

func (n JobNotifs) getDeployHashes() string {
	if commitHashes, err := n.db.GetDeployHashes(); err != nil {
		return ""
	} else {
		ceramicMsg := n.getComponentMsg(manager.DeployComponent_Ceramic, commitHashes[manager.DeployComponent_Ceramic])
		casMsg := n.getComponentMsg(manager.DeployComponent_Cas, commitHashes[manager.DeployComponent_Cas])
		ipfsMsg := n.getComponentMsg(manager.DeployComponent_Ipfs, commitHashes[manager.DeployComponent_Ipfs])
		return fmt.Sprintf("%s\n%s\n%s", ceramicMsg, casMsg, ipfsMsg)
	}
}

func (n JobNotifs) getComponentMsg(component manager.DeployComponent, sha string) string {
	var repo string
	switch component {
	case manager.DeployComponent_Ceramic:
		repo = manager.DeployRepo_Ceramic
	case manager.DeployComponent_Cas:
		repo = manager.DeployRepo_Cas
	case manager.DeployComponent_Ipfs:
		repo = manager.DeployRepo_Ipfs
	}
	return fmt.Sprintf("[%s (%s)](%s/%s/commit/%s)", repo, sha[:12], GitHubOrg, repo, sha)
}
