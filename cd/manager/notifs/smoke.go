package notifs

import (
	"fmt"
	"github.com/3box/pipeline-tools/cd/manager"
	"os"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/webhook"

	"github.com/3box/pipeline-tools/cd/manager/common/job"
)

var _ jobNotif = &smokeTestNotif{}

type smokeTestNotif struct {
	state  job.JobState
	region string
	env    string
}

func newSmokeTestNotif(jobState job.JobState) (jobNotif, error) {
	return &smokeTestNotif{jobState, os.Getenv("AWS_REGION"), os.Getenv(manager.EnvVar_Env)}, nil
}

func (s smokeTestNotif) getChannels() []webhook.Client {
	return nil
}

func (s smokeTestNotif) getTitle() string {
	return fmt.Sprintf("Smoke Tests %s", strings.ToUpper(string(s.state.Stage)))
}

func (s smokeTestNotif) getFields() []discord.EmbedField {
	return nil
}

func (s smokeTestNotif) getColor() discordColor {
	return colorForStage(s.state.Stage)
}

func (s smokeTestNotif) getUrl() string {
	if taskId, found := s.state.Params[job.JobParam_Id].(string); found {
		idParts := strings.Split(taskId, "/")
		return fmt.Sprintf(
			"https://%s.console.aws.amazon.com/cloudwatch/home?region=%s#logsV2:log-groups/log-group/$252Fecs$252Fceramic-qa-tests/log-events/%s-smoke-tests$252Fsmoke$252F%s",
			s.region,
			s.region,
			s.env,
			idParts[len(idParts)-1],
		)
	}
	return ""
}
