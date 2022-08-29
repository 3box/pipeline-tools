package notifs

import (
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"
	"sync"
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

const DiscordUnknown = "UNKNOWN"
const DiscordPacing = 2 * time.Second
const GitHubOrg = "https://github.com/ceramicnetwork"

var _ manager.Notifs = &JobNotifs{}

type JobNotifs struct {
	state              *sync.Map
	alertWebhook       webhook.Client
	warningWebhook     webhook.Client
	infoWebhook        webhook.Client
	deploymentsWebhook webhook.Client
	communityWebhook   webhook.Client
}

func NewJobNotifs() (manager.Notifs, error) {
	if a, err := parseDiscordWebhookUrl("DISCORD_ALERT_WEBHOOK"); err != nil {
		return nil, err
	} else if w, err := parseDiscordWebhookUrl("DISCORD_WARNING_WEBHOOK"); err != nil {
		return nil, err
	} else if i, err := parseDiscordWebhookUrl("DISCORD_INFO_WEBHOOK"); err != nil {
		return nil, err
	} else if d, err := parseDiscordWebhookUrl("DISCORD_DEPLOYMENTS_WEBHOOK"); err != nil {
		return nil, err
	} else if c, err := parseDiscordWebhookUrl("DISCORD_COMMUNITY_NODES_WEBHOOK"); err != nil {
		return nil, err
	} else {
		return &JobNotifs{new(sync.Map), a, w, i, d, c}, nil
	}
}

func parseDiscordWebhookUrl(urlEnv string) (webhook.Client, error) {
	webhookUrl := os.Getenv(urlEnv)
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

func (n JobNotifs) NotifyJob(jobs ...manager.JobState) {
	for _, jobState := range jobs {
		n.cacheJobState(jobState)
		n.sendNotif(
			n.getJobTitle(jobState),
			n.getJobDesc(jobState),
			n.getNotifColor(jobState),
		)
	}
}

func (n JobNotifs) sendNotif(title, desc string, color DiscordColor) {
	messageEmbed := discord.Embed{
		Title:       title,
		Type:        discord.EmbedTypeRich,
		Description: desc,
		Color:       int(color),
	}
	if _, err := n.alertWebhook.CreateMessage(discord.NewWebhookMessageCreateBuilder().
		SetEmbeds(messageEmbed).
		Build(),
		rest.WithDelay(DiscordPacing),
	); err != nil {
		log.Printf("notifyJob: error sending discord notification: %v, %s, %s, %d", err, title, desc, color)
	}
}

func (n JobNotifs) getJobTitle(jobState manager.JobState) string {
	var jobName string
	switch jobState.Type {
	case manager.JobType_Deploy:
		component := jobState.Params[manager.EventParam_Component]
		jobName = fmt.Sprintf("3Box Labs `%s` %s %s", os.Getenv("ENV"), strings.ToUpper(component.(string)), manager.NotifTitle_Deploy)
	case manager.JobType_Anchor:
		jobName = manager.NotifTitle_Anchor
	case manager.JobType_TestE2E:
		jobName = manager.NotifTitle_TestE2E
	case manager.JobType_TestSmoke:
		jobName = manager.NotifTitle_TestSmoke
	default:
		log.Printf("sendNotif: unknown job type: %s", manager.PrintJob(jobState))
		return DiscordUnknown
	}
	return jobName + " " + strings.ToUpper(string(jobState.Stage))
}

func (n JobNotifs) getJobDesc(jobState manager.JobState) string {
	switch jobState.Type {
	case manager.JobType_Deploy:
		return n.getCommitHashes()
	case manager.JobType_Anchor:
		return n.getCommitHashes()
	case manager.JobType_TestE2E:
		return n.getCommitHashes()
	case manager.JobType_TestSmoke:
		return n.getCommitHashes()
	default:
		log.Printf("sendNotif: unknown job type: %s", manager.PrintJob(jobState))
		return DiscordUnknown
	}
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

func (n JobNotifs) getCommitHashes() string {
	ceramicMsg := n.getComponentMsg(manager.DeployComponent_Ceramic)
	casMsg := n.getComponentMsg(manager.DeployComponent_Cas)
	ipfsMsg := n.getComponentMsg(manager.DeployComponent_Ipfs)
	return fmt.Sprintf("%s\n%s\n%s", ceramicMsg, casMsg, ipfsMsg)
}

func (n JobNotifs) getComponentMsg(component string) string {
	if jobState, found := n.state.Load(n.getStateKey(manager.JobType_Deploy, component)); found {
		sha := jobState.(manager.JobState).Params[manager.EventParam_Sha].(string)
		shaTag := jobState.(manager.JobState).Params[manager.EventParam_ShaTag].(string)
		return fmt.Sprintf("[%s (%s)](%s/%s/commit/%s)", component, shaTag, GitHubOrg, component, sha)
	}
	return component + " (n/a)"
}

func (n JobNotifs) cacheJobState(jobState manager.JobState) {
	// If this notification is for a newer, completed deployment, cache the commit hash for later use.
	if (jobState.Type == manager.JobType_Deploy) && (jobState.Stage == manager.JobStage_Completed) {
		component := jobState.Params[manager.EventParam_Component].(string)
		stateKey := n.getStateKey(manager.JobType_Deploy, component)
		if cachedState, found := n.state.Load(stateKey); !found || jobState.Ts.After(cachedState.(manager.JobState).Ts) {
			n.state.Store(stateKey, jobState)
		}
	}
}

func (n JobNotifs) getStateKey(parts ...interface{}) string {
	// The first part will always be the job type.
	key := string(parts[0].(manager.JobType))
	for i := 1; i < len(parts); i++ {
		key += "_" + parts[i].(string)
	}
	return key
}
