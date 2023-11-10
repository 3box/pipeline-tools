package notifs

import (
	"fmt"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/webhook"

	"github.com/3box/pipeline-tools/cd/manager/common/job"
)

var _ jobNotif = &e2eTestNotif{}

type e2eTestNotif struct {
	state job.JobState
}

func newE2eTestNotif(jobState job.JobState) (jobNotif, error) {
	return &e2eTestNotif{jobState}, nil
}

func (e e2eTestNotif) getChannels() []webhook.Client {
	return nil
}

func (e e2eTestNotif) getTitle() string {
	return fmt.Sprintf("E2E Tests %s", strings.ToUpper(string(e.state.Stage)))
}

func (e e2eTestNotif) getFields() []discord.EmbedField {
	return nil
}

func (e e2eTestNotif) getColor() discordColor {
	return colorForStage(e.state.Stage)
}

func (e e2eTestNotif) getUrl() string {
	return ""
}
