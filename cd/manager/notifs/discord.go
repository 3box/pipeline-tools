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

const (
	notifField_References string = "References"
	notifField_JobId      string = "Job ID"
	notifField_RunTime    string = "Time Running"
	notifField_WaitTime   string = "Time Waiting"
	notifField_Deploy     string = "Deployment(s)"
	notifField_Anchor     string = "Anchor Worker(s)"
	notifField_TestE2E    string = "E2E Tests"
	notifField_TestSmoke  string = "Smoke Tests"
	notifField_Workflow   string = "Workflow(s)"
	notifField_Logs       string = "Logs"
)

const discordPacing = 2 * time.Second

const shaTagLength = 12

// Show "queued" for "dequeued" jobs to make it more understandable
const prettyStageDequeued = "queued"

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
	getUrl() string
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

func webhooksForLabels(labels []string) ([]webhook.Client, error) {
	webhooks := make([]webhook.Client, 0)
	for _, label := range labels {
		switch label {
		case job.WorkflowJobLabel_Test:
			if t, err := parseDiscordWebhookUrl("DISCORD_TESTS_WEBHOOK"); err != nil {
				return nil, err
			} else {
				webhooks = append(webhooks, t)
			}
		case job.WorkflowJobLabel_Deploy:
			if t, err := parseDiscordWebhookUrl("DISCORD_DEPLOYMENTS_WEBHOOK"); err != nil {
				return nil, err
			} else {
				webhooks = append(webhooks, t)
			}
		}
	}
	return webhooks, nil
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
		rest.WithDelay(discordPacing),
	); err != nil {
		log.Printf("notifyJob: error sending discord notification: %v, %s, %v, %d", err, title, fields, color)
	}
}

func (n JobNotifs) getNotifFields(jobState job.JobState) []discord.EmbedField {
	fields := []discord.EmbedField{
		{
			Name:  notifField_JobId,
			Value: jobState.JobId,
		},
	}
	// Return deploy tags for all jobs if we were able to retrieve them successfully.
	if deployTags := n.getDeployTags(jobState); len(deployTags) > 0 {
		fields = append(fields, discord.EmbedField{
			Name:  notifField_References,
			Value: deployTags,
		})
	}
	// If the job just started, also display the queue wait time.
	if jobState.Stage == job.JobStage_Started {
		if waitTime, found := jobState.Params[job.JobParam_WaitTime].(string); found {
			parsedWaitTime, _ := time.ParseDuration(waitTime)
			prettyWaitTime := prettyDuration(parsedWaitTime)
			if len(prettyWaitTime) > 0 {
				fields = append(fields, discord.EmbedField{
					Name:  notifField_WaitTime,
					Value: prettyWaitTime,
				})
			}
		}
	} else
	// Only need to display the run time once the job progresses beyond the "started" stage
	if startTime, found := jobState.Params[job.JobParam_Start].(float64); found {
		runTime := prettyDuration(time.Since(time.Unix(0, int64(startTime))))
		if len(runTime) > 0 {
			fields = append(fields, discord.EmbedField{
				Name:  notifField_RunTime,
				Value: runTime,
			})
		}
	}
	// Add the list of jobs in progress
	if activeJobs := n.getActiveJobs(jobState); len(activeJobs) > 0 {
		fields = append(fields, activeJobs...)
	}
	if jn, err := n.getJobNotif(jobState); err == nil {
		if len(jn.getUrl()) > 0 {
			fields = append(fields, discord.EmbedField{
				Name:  notifField_Logs,
				Value: fmt.Sprintf("[%s](%s)", jobState.JobId, jn.getUrl()),
			})
		}
	}
	return fields
}

func (n JobNotifs) getDeployTags(jobState job.JobState) string {
	if deployTags, err := n.db.GetDeployTags(); err != nil {
		return ""
	} else {
		if jobState.Type == job.JobType_Deploy {
			if deployTag, found := jobState.Params[job.DeployJobParam_DeployTag].(string); found {
				// This should always be present
				sha := jobState.Params[job.DeployJobParam_Sha].(string)
				deployTags[manager.DeployComponent(jobState.Params[job.DeployJobParam_Component].(string))] = deployTag + "," + sha
			}
		}
		// Prepare component messages with GitHub commit hashes and hyperlinks
		ceramicMsg := n.getComponentMsg(manager.DeployComponent_Ceramic, deployTags)
		casMsg := n.getComponentMsg(manager.DeployComponent_Cas, deployTags)
		casV5Msg := n.getComponentMsg(manager.DeployComponent_CasV5, deployTags)
		ipfsMsg := n.getComponentMsg(manager.DeployComponent_Ipfs, deployTags)
		rustCeramicMsg := n.getComponentMsg(manager.DeployComponent_RustCeramic, deployTags)
		return n.combineComponentMsgs(ceramicMsg, casMsg, casV5Msg, ipfsMsg, rustCeramicMsg)
	}
}

func (n JobNotifs) getComponentMsg(component manager.DeployComponent, deployTags map[manager.DeployComponent]string) string {
	if deployTag, found := deployTags[component]; found && len(deployTag) > 0 {
		if repo, err := manager.ComponentRepo(component); err == nil {
			deployTagParts := strings.Split(deployTag, ",")
			tagString := deployTagParts[0]
			// Check if we have metadata associated with the deployed tag
			if (len(deployTagParts) > 1) && (deployTagParts[1] == job.DeployJobTarget_Release) {
				return fmt.Sprintf("[%s (v%s)](https://github.com/%s/%s/releases/tag/v%s)", repo.Name, tagString, repo.Org, repo.Name, tagString)
			} else if manager.IsValidSha(tagString) {
				return fmt.Sprintf("[%s (%s)](https://github.com/%s/%s/commit/%s)", repo.Name, tagString[:shaTagLength], repo.Org, repo.Name, tagString)
			}
		}
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
		if activeJob.JobId != jobState.JobId {
			if jn, err := n.getJobNotif(activeJob); (err == nil) && (len(jn.getUrl()) > 0) {
				message += fmt.Sprintf("[%s](%s)\n", activeJob.JobId, jn.getUrl())
			} else {
				message += activeJob.JobId + "\n"
			}
		}
	}
	return discord.EmbedField{
		Name:  notifField(jobType) + " In Progress:",
		Value: message,
	}, len(message) > 0
}

func notifField(jt job.JobType) string {
	switch jt {
	case job.JobType_Deploy:
		return notifField_Deploy
	case job.JobType_Anchor:
		return notifField_Anchor
	case job.JobType_TestE2E:
		return notifField_TestE2E
	case job.JobType_TestSmoke:
		return notifField_TestSmoke
	case job.JobType_Workflow:
		return notifField_Workflow
	default:
		return ""
	}
}

func colorForStage(jobStage job.JobStage) discordColor {
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
		log.Printf("colorForStage: unknown job stage: %s", jobStage)
		return discordColor_Alert
	}
}

func prettyDuration(duration time.Duration) string {
	hours := int(duration.Seconds() / 3600)
	minutes := int(duration.Seconds()/60) % 60
	seconds := int(duration.Seconds()) % 60
	if (hours != 0) || (minutes != 0) || (seconds != 0) {
		return fmt.Sprintf("%dh %dm %ds", hours, minutes, seconds)
	}
	return ""
}
