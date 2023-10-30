package notifs

import (
	"fmt"
	"log"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/disgo/webhook"
	"github.com/disgoorg/snowflake/v2"

	"github.com/3box/pipeline-tools/cd/manager"
	"github.com/3box/pipeline-tools/cd/manager/common/job"
)

type discordColor int

const (
	discordColor_None    = iota
	discordColor_Info    = 3447003
	discordColor_Ok      = 3581519
	discordColor_Warning = 16776960
	discordColor_Alert   = 16711712
)

const DiscordPacing = 2 * time.Second

const ShaTagLength = 12

var _ manager.Notifs = &JobNotifs{}

type JobNotifs struct {
	db          manager.Database
	cache       manager.Cache
	testWebhook webhook.Client
}

type jobNotif interface {
	getChannels() []webhook.Client
	getTitle() string
	getFields() []discord.EmbedField
	getColor() discordColor
}

func NewJobNotifs(db manager.Database, cache manager.Cache) (manager.Notifs, error) {
	if t, err := parseDiscordWebhookUrl("DISCORD_TEST_WEBHOOK"); err != nil {
		return nil, err
	} else {
		return &JobNotifs{db, cache, t}, nil
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

func (n JobNotifs) NotifyJob(jobs ...job.JobState) {
	for _, jobState := range jobs {
		if jn, err := n.getJobNotif(jobState); err != nil {
			log.Printf("notifyJob: error creating job notification: %v, %s", err, manager.PrintJob(jobState))
		} else {
			// Send all notifications to the test webhook
			channels := append(jn.getChannels(), n.testWebhook)
			for _, channel := range channels {
				if channel != nil {
					n.sendNotif(
						jn.getTitle(),
						append(n.getNotifFields(jobState), jn.getFields()...),
						jn.getColor(),
						channel,
					)
				}
			}
		}
	}
}

func (n JobNotifs) getJobNotif(jobState job.JobState) (jobNotif, error) {
	switch jobState.Type {
	case job.JobType_Deploy:
		return newDeployNotif(jobState)
	case job.JobType_Anchor:
		return newAnchorNotif(jobState)
	case job.JobType_TestE2E:
		return newE2eTestNotif(jobState)
	case job.JobType_TestSmoke:
		return newSmokeTestNotif(jobState)
	case job.JobType_Workflow:
		return newWorkflowNotif(jobState)
	default:
		return nil, fmt.Errorf("getJobNotif: unknown job type: %s", jobState.Type)
	}
}

func (n JobNotifs) sendNotif(title string, fields []discord.EmbedField, color discordColor, channel webhook.Client) {
	messageEmbed := discord.Embed{
		Title:  title,
		Type:   discord.EmbedTypeRich,
		Fields: fields,
		Color:  int(color),
	}
	if _, err := channel.CreateMessage(discord.NewWebhookMessageCreateBuilder().
		SetEmbeds(messageEmbed).
		SetUsername(manager.ServiceName).
		Build(),
		rest.WithDelay(DiscordPacing),
	); err != nil {
		log.Printf("notifyJob: error sending discord notification: %v, %s, %v, %d", err, title, fields, color)
	}
}

func (n JobNotifs) getNotifFields(jobState job.JobState) []discord.EmbedField {
	fields := []discord.EmbedField{
		{
			Name:  manager.NotifField_JobId,
			Value: jobState.Job,
		},
	}
	// Return deploy hashes for all jobs, if we were able to retrieve them successfully.
	if commitHashes := n.getDeployHashes(jobState); len(commitHashes) > 0 {
		fields = append(fields, discord.EmbedField{
			Name:  manager.NotifField_CommitHashes,
			Value: commitHashes,
		})
	}
	fields = append(fields, discord.EmbedField{
		Name:  manager.NotifField_Time,
		Value: time.Now().Format(time.RFC1123), // "Mon, 02 Jan 2006 15:04:05 MST"
	})
	// Add the list of jobs in progress
	if activeJobs := n.getActiveJobs(jobState); len(activeJobs) > 0 {
		fields = append(fields, activeJobs...)
	}
	return fields
}

func (n JobNotifs) getDeployHashes(jobState job.JobState) string {
	if commitHashes, err := n.db.GetDeployHashes(); err != nil {
		return ""
	} else {
		if jobState.Type == job.JobType_Deploy {
			sha := jobState.Params[job.DeployJobParam_Sha].(string)
			// If the specified hash is valid, overwrite the previous hash from the database.
			if isValidSha, _ := regexp.MatchString(manager.CommitHashRegex, sha); isValidSha {
				commitHashes[manager.DeployComponent(jobState.Params[job.DeployJobParam_Component].(string))] = sha
			}
		}
		// Prepare component messages with GitHub commit hashes and hyperlinks
		ceramicMsg := n.getComponentMsg(manager.DeployComponent_Ceramic, commitHashes)
		casMsg := n.getComponentMsg(manager.DeployComponent_Cas, commitHashes)
		casV5Msg := n.getComponentMsg(manager.DeployComponent_CasV5, commitHashes)
		ipfsMsg := n.getComponentMsg(manager.DeployComponent_Ipfs, commitHashes)
		return n.combineComponentMsgs(ceramicMsg, casMsg, casV5Msg, ipfsMsg)
	}
}

func (n JobNotifs) getComponentMsg(component manager.DeployComponent, commitHashes map[manager.DeployComponent]string) string {
	if commitHash, found := commitHashes[component]; found && (len(commitHash) >= ShaTagLength) {
		repo := manager.ComponentRepo(component)
		return fmt.Sprintf("[%s (%s)](https://github.com/%s/%s/commit/%s)", repo, commitHash[:ShaTagLength], manager.GitHubOrg, repo, commitHash)
	}
	return ""
}

func (n JobNotifs) combineComponentMsgs(msgs ...string) string {
	message := ""
	for i, msg := range msgs {
		if len(msg) > 0 {
			message += msg
			if i < len(msgs)-1 {
				message += "\n"
			}
		}
	}
	return message
}

func (n JobNotifs) getActiveJobs(jobState job.JobState) []discord.EmbedField {
	fields := make([]discord.EmbedField, 0, 0)
	if field, found := n.getActiveJobsByType(jobState, job.JobType_Deploy); found {
		fields = append(fields, field)
	}
	if field, found := n.getActiveJobsByType(jobState, job.JobType_Anchor); found {
		fields = append(fields, field)
	}
	if field, found := n.getActiveJobsByType(jobState, job.JobType_TestE2E); found {
		fields = append(fields, field)
	}
	if field, found := n.getActiveJobsByType(jobState, job.JobType_TestSmoke); found {
		fields = append(fields, field)
	}
	if field, found := n.getActiveJobsByType(jobState, job.JobType_Workflow); found {
		fields = append(fields, field)
	}
	return fields
}

func (n JobNotifs) getActiveJobsByType(jobState job.JobState, jobType job.JobType) (discord.EmbedField, bool) {
	activeJobs := n.cache.JobsByMatcher(func(js job.JobState) bool {
		return job.IsActiveJob(js) && (js.Type == jobType)
	})
	message := ""
	for _, activeJob := range activeJobs {
		// Exclude job for which this notification is being generated
		if activeJob.Job != jobState.Job {
			message += fmt.Sprintf("%s (%s)\n", activeJob.Job, activeJob.Stage)
		}
	}
	return discord.EmbedField{
		Name:  manager.NotifField(jobType) + " In Progress:",
		Value: message,
	}, len(message) > 0
}

func getColorForStage(jobStage job.JobStage) discordColor {
	switch jobStage {
	case job.JobStage_Dequeued:
		return discordColor_Info
	case job.JobStage_Skipped:
		return discordColor_Warning
	case job.JobStage_Started:
		return discordColor_None
	case job.JobStage_Waiting:
		return discordColor_Info
	case job.JobStage_Failed:
		return discordColor_Alert
	case job.JobStage_Canceled:
		return discordColor_Warning
	case job.JobStage_Completed:
		return discordColor_Ok
	default:
		log.Printf("getColorForStage: unknown job stage: %s", jobStage)
		return discordColor_Alert
	}
}
