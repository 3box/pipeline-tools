package notifs

import (
	"fmt"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/webhook"

	"github.com/3box/pipeline-tools/cd/manager/common/job"
)

var _ jobNotif = &smokeTestNotif{}

type smokeTestNotif struct {
	state job.JobState
}

func newSmokeTestNotif(jobState job.JobState) (jobNotif, error) {
	return &smokeTestNotif{jobState}, nil
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
