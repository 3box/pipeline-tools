package manager

import "github.com/disgoorg/disgo/discord"

func CreateMessage() discord.WebhookMessageCreate {
	builder := discord.NewWebhookMessageCreateBuilder()
	builder.Content = "blah"
	return builder.Build()
}
