package bot

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"

	"streamly/internal/media"
	"streamly/internal/tvapi"
)

func (b *Bot) handleChannels(s *discordgo.Session, i *discordgo.InteractionCreate) {

	_ = deferReply(s, i)

	channels, totalPages, err := b.Resolver.ListTVChannels(1)

	if err != nil {
		editMessage(s, i, &discordgo.WebhookEdit{Content: strPtr("Couldn't load the live TV channel catalog right now.")})
		return
	}

	if len(channels) == 0 {
		editMessage(s, i, &discordgo.WebhookEdit{Content: strPtr("No live TV channels were found.")})
		return
	}

	embed := channelsEmbed(channels, 1, totalPages)
	components := channelsComponents(1, totalPages, channels)
	editMessage(s, i, &discordgo.WebhookEdit{Embeds: ptrEmbeds([]*discordgo.MessageEmbed{embed}), Components: ptrComponents(components)})

}

func (b *Bot) handleChannelsComponent(s *discordgo.Session, i *discordgo.InteractionCreate, parts []string) {

	if len(parts) < 3 {
		return
	}

	switch parts[1] {
	case "prev", "next":

		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredMessageUpdate})

		page, _ := strconv.Atoi(parts[2])

		if parts[1] == "prev" {
			page--
		} else {
			page++
		}

		if page < 1 {
			page = 1
		}

		channels, totalPages, err := b.Resolver.ListTVChannels(page)

		if err != nil || len(channels) == 0 {
			editMessage(s, i, &discordgo.WebhookEdit{Content: strPtr("Couldn't load that channel page.")})
			return
		}

		embed := channelsEmbed(channels, page, totalPages)
		components := channelsComponents(page, totalPages, channels)
		editMessage(s, i, &discordgo.WebhookEdit{Embeds: ptrEmbeds([]*discordgo.MessageEmbed{embed}), Components: ptrComponents(components)})

	case "pick":

		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredMessageUpdate})

		values := i.MessageComponentData().Values

		if len(values) == 0 {
			return
		}

		selection, err := b.Resolver.ResolveTVSelection(values[0])

		if err != nil || selection == nil {
			editMessage(s, i, &discordgo.WebhookEdit{Content: strPtr("Couldn't resolve that live TV channel.")})
			return
		}

		b.startLiveStream(s, i, selection.Channel, selection.Channel.Name, values[0])

	}

}

func channelsEmbed(channels []tvapi.Channel, page, totalPages int) *discordgo.MessageEmbed {

	embed := &discordgo.MessageEmbed{
		Color:  embedColor,
		Title:  "Channels",
		Footer: &discordgo.MessageEmbedFooter{Text: fmt.Sprintf("Page %d of %d", page, totalPages)},
	}

	var lines []string

	for index, channel := range channels {
		lines = append(lines, fmt.Sprintf("**%d.** %s", (page-1)*media.ChannelPageSize+index+1, channelLine(channel)))
	}

	embed.Description = strings.Join(lines, "\n")

	return embed

}

func channelLine(channel tvapi.Channel) string {

	region := channel.Country.Name

	if region == "" {
		region = strings.ToUpper(channel.Country.Code)
	}

	category := channel.Category

	if category == "" {
		category = "General"
	}

	return fmt.Sprintf("%s — %s · %s", channel.Name, category, region)

}

func channelsComponents(page, totalPages int, channels []tvapi.Channel) []discordgo.MessageComponent {

	components := []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.SelectMenu{
				CustomID:    fmt.Sprintf("channels:pick:%d", page),
				Placeholder: "Choose a channel to watch",
				Options:     channelSelectOptions(channels),
			},
		}},
	}

	nav := []discordgo.MessageComponent{
		discordgo.Button{
			Label:    "Previous",
			CustomID: fmt.Sprintf("channels:prev:%d", page),
			Style:    discordgo.SecondaryButton,
			Disabled: page <= 1,
		},
		discordgo.Button{
			Label:    "Next",
			CustomID: fmt.Sprintf("channels:next:%d", page),
			Style:    discordgo.SecondaryButton,
			Disabled: page >= totalPages,
		},
	}

	components = append(components, discordgo.ActionsRow{Components: nav})

	return components

}

func channelSelectOptions(channels []tvapi.Channel) []discordgo.SelectMenuOption {

	options := make([]discordgo.SelectMenuOption, 0, len(channels))

	for _, channel := range channels {

		description := channel.Category

		if channel.Country.Name != "" {
			if description != "" {
				description += " · "
			}
			description += channel.Country.Name
		}

		options = append(options, discordgo.SelectMenuOption{
			Label:       truncate(channel.Name, 100),
			Value:       media.TVSelectionValue(channel.DaddyID),
			Description: truncate(description, 100),
		})

	}

	return options

}
