package manager

import "github.com/disgoorg/disgo/discord"

// TODO: Implement Discord notifications
func CreateMessage() discord.WebhookMessageCreate {
	builder := discord.NewWebhookMessageCreateBuilder()
	builder.Content = "TODO"
	return builder.Build()
}
