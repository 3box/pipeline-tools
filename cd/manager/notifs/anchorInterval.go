package notifs

import (
	"fmt"
	"time"

	"github.com/3box/pipeline-tools/cd/manager"
)

const NotifPacing = time.Hour

// lastNotifTime is the time at which the last notification was sent indicating that an anchor job was not started in a
// timely manner.
var lastNotifTime = time.UnixMilli(0)

type anchorIntervalNotif struct {
	n manager.Notifs
}

type AnchorIntervalNotif = *anchorIntervalNotif

func NewAnchorIntervalNotif(n manager.Notifs) AnchorIntervalNotif {
	return &anchorIntervalNotif{n}
}

func (a AnchorIntervalNotif) SendAlert(ts time.Time) {
	now := time.Now()
	// Don't send alerts too frequently
	if now.Add(-NotifPacing).After(lastNotifTime) {
		a.n.SendAlert("CAS anchor task not started", fmt.Sprintf("Since %s", ts.Format(time.RFC1123)))
		lastNotifTime = now
	}
}
