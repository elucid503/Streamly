package bot

import (
	"streamly/internal/media"

	"github.com/bwmarrin/discordgo"
)

func (b *Bot) handleSports(s *discordgo.Session, i *discordgo.InteractionCreate) {

	_ = deferReply(s, i)

	if err := b.Pool.RequireWorker(i.GuildID); err != nil {

		editMessage(s, i, &discordgo.WebhookEdit{Content: strPtr(err.Error())})
		return

	}

	game := optionString(i, "game")

	selection, err := b.Resolver.ResolveSportsSelection(game)

	if err != nil || selection == nil {

		editMessage(s, i, &discordgo.WebhookEdit{Content: strPtr("Couldn't find that game. Run /sports again and pick one from the list.")})
		return

	}

	channel := media.SportsChannel(*selection)

	b.startLiveStream(s, i, channel, channel.Name, "", false)

}

func (b *Bot) sportsGameChoices(query string) []*discordgo.ApplicationCommandOptionChoice {

	events, err := b.Resolver.SportsSearch(query, maxOptions)

	if err != nil {

		return nil

	}

	choices := make([]*discordgo.ApplicationCommandOptionChoice, 0, len(events))

	for _, event := range events {

		value := media.SportsSelectionValue(event)

		if value == "" {

			continue

		}

		choices = append(choices, &discordgo.ApplicationCommandOptionChoice{

			Name: truncate(media.SportsLabel(event), 100),
			Value: value,

		})

	}

	return choices

}
