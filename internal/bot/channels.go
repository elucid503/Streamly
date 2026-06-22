package bot

import (
	"fmt"
	"strconv"
	"strings"

	"streamly/internal/media"
	"streamly/internal/tvapi"

	"github.com/bwmarrin/discordgo"
)

func (b *Bot) handleChannels(s *discordgo.Session, i *discordgo.InteractionCreate) {

	_ = deferReply(s, i)

	channels, totalPages, err := b.Resolver.ListTVChannelsGuided(1)

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

		channels, totalPages, err := b.Resolver.ListTVChannelsGuided(page)

		if err != nil || len(channels) == 0 {

			editMessage(s, i, &discordgo.WebhookEdit{Content: strPtr("Couldn't load that channel page.")})
			return

		}

		embed := channelsEmbed(channels, page, totalPages)
		components := channelsComponents(page, totalPages, channels)

		editMessage(s, i, &discordgo.WebhookEdit{Embeds: ptrEmbeds([]*discordgo.MessageEmbed{embed}), Components: ptrComponents(components)})

	case "pick":

		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredMessageUpdate})

		if err := b.Pool.RequireWorker(i.GuildID); err != nil {

			editMessage(s, i, &discordgo.WebhookEdit{Content: strPtr(err.Error())})
			return

		}

		values := i.MessageComponentData().Values

		if len(values) == 0 {

			return

		}

		selection, err := b.Resolver.ResolveTVSelection(values[0])

		if err != nil || selection == nil {

			editMessage(s, i, &discordgo.WebhookEdit{Content: strPtr("Couldn't resolve that live TV channel.")})
			return

		}

		b.startLiveStream(s, i, selection.Channel, selection.Channel.Name, values[0], true)

	}

}

func channelsEmbed(channels []media.GuidedChannel, page, totalPages int) *discordgo.MessageEmbed {

	embed := &discordgo.MessageEmbed{

		Color:  embedColor,
		Title:  "Channels",
		Footer: &discordgo.MessageEmbedFooter{Text: fmt.Sprintf("Page %d of %d", page, totalPages)},
	}

	for index, channel := range channels {

		number := (page-1)*media.ChannelPageSize + index + 1
		embed.Fields = append(embed.Fields, channelField(number, channel))

	}

	return embed

}

func channelField(number int, guided media.GuidedChannel) *discordgo.MessageEmbedField {

	value := channelGuideSubtext(guided.Now, guided.Next)

	if value == "" {

		value = channelDetailSubtext(guided.Channel)

	}

	return &discordgo.MessageEmbedField{

		Name:  channelHeader(number, guided.Channel),
		Value: value,

		Inline: true,
	}

}

func channelDetailSubtext(channel tvapi.Channel) string {

	if detail := channelMeta(channel); detail != "" {

		return detail

	}

	if channel.Status != "" {

		return channel.Status

	}

	return "No details available."

}

func channelHeader(number int, channel tvapi.Channel) string {

	header := fmt.Sprintf("**%d. %s**", number, channel.Name)

	if meta := channelMeta(channel); meta != "" {

		header += " — " + meta

	}

	return header

}

func channelMeta(channel tvapi.Channel) string {

	var parts []string

	if channel.Category != "" {

		parts = append(parts, channel.Category)

	}

	region := channel.Country.Name

	if region == "" {

		region = strings.ToUpper(channel.Country.Code)

	}

	if region != "" {

		parts = append(parts, region)

	}

	return strings.Join(parts, " · ")

}

func channelGuideSubtext(now, next string) string {

	var parts []string

	if now != "" {

		parts = append(parts, "Now: "+now)

	}

	if next != "" {

		parts = append(parts, "Next: "+next)

	}

	if len(parts) == 0 {

		return ""

	}

	return strings.Join(parts, " · ") + "\n"

}

func channelsComponents(page, totalPages int, channels []media.GuidedChannel) []discordgo.MessageComponent {

	components := []discordgo.MessageComponent{

		discordgo.ActionsRow{Components: []discordgo.MessageComponent{

			discordgo.SelectMenu{

				CustomID: fmt.Sprintf("channels:pick:%d", page),
				Placeholder: "Choose a channel to watch",
				Options: channelSelectOptions(channels),

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

func channelSelectOptions(channels []media.GuidedChannel) []discordgo.SelectMenuOption {

	options := make([]discordgo.SelectMenuOption, 0, len(channels))

	for _, guided := range channels {

		channel := guided.Channel

		description := channel.Category

		if guided.Now != "" {

			description = "Now: " + guided.Now

		} else if channel.Country.Name != "" {

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
