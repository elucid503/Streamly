package bot

import (
	"context"
	"errors"

	"github.com/bwmarrin/discordgo"

	"streamly/internal/config"
	"streamly/internal/pool"
)

func (b *Bot) handleConfigure(s *discordgo.Session, i *discordgo.InteractionCreate) {

	if i.GuildID == "" {
		respondEphemeral(s, i, "This command can only be used in a server.")
		return
	}

	if i.Member == nil || i.Member.User == nil || !isAdmin(i.Member.User.ID) {
		respondEphemeral(s, i, "You do not have permission to use this command.")
		return
	}

	key := optionString(i, "key")

	if key == "" {
		respondEphemeral(s, i, "A key is required.")
		return
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
	})

	if err := b.Pool.SetKey(context.Background(), i.GuildID, key); err != nil {

		message := "Could not apply that key."

		if errors.Is(err, pool.ErrKeyChangeActive) {
			message = "A stream is active. Stop it before changing the key."
		}

		editMessage(s, i, &discordgo.WebhookEdit{Content: strPtr(message)})
		return
	}

	editMessage(s, i, &discordgo.WebhookEdit{Content: strPtr("Server key updated.")})

}

func isAdmin(userID string) bool {

	for _, id := range config.App.AdminUserIDs {
		if id == userID {
			return true
		}
	}

	return false

}