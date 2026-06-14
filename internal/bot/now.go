package bot

import (
	"fmt"

	"streamly/internal/pool"

	"github.com/bwmarrin/discordgo"
)

func (b *Bot) handleNow(s *discordgo.Session, i *discordgo.InteractionCreate) {

	if i.GuildID == "" {

		respondEphemeral(s, i, "This command only works inside a server.")
		return

	}

	session := b.Pool.ActiveInGuild(i.GuildID)

	if session == nil || !session.Busy {

		respondEmbed(s, i, &discordgo.MessageEmbed{

			Title: "Now",
			Description: "Nothing is streaming in this server.",

			Color: embedColor,

		})

		return

	}

	target, ok := streamTargetFromSession(session)

	if !ok {

		respondEmbed(s, i, nowFallbackEmbed(b.Pool, session))
		return

	}

	respondEmbed(s, i, nowPlayingEmbed(b.Pool, session, target))

}

func nowPlayingEmbed(p *pool.Pool, session *pool.Session, target streamTarget) *discordgo.MessageEmbed {

	stats := p.Stats(session)

	header := "Now Streaming"

	if stats.Paused {

		header = "Paused"

	}

	var embed *discordgo.MessageEmbed

	if target.Live && target.TVChannel != nil {

		embed = liveStreamingEmbed(target.Details, *target.TVChannel, stats.ChannelID)
	} else {

		embed = streamingEmbed(target.Details, stats.ChannelID, target.Episode)

	}

	embed.Author = &discordgo.MessageEmbedAuthor{Name: header}
	embed.Fields = appendNowFields(embed.Fields, stats, session.Live())

	return embed

}

func nowFallbackEmbed(p *pool.Pool, session *pool.Session) *discordgo.MessageEmbed {

	stats := p.Stats(session)
	caption := fallbackCaption(stats.Caption, "Active Stream")

	header := "Now Streaming"

	if stats.Paused {

		header = "Paused"

	}

	embed := &discordgo.MessageEmbed{

		Author: &discordgo.MessageEmbedAuthor{Name: header},
		Title: caption,

		Color: embedColor,

	}

	embed.Fields = appendNowFields([]*discordgo.MessageEmbedField{

		{

			Name: "Channel",
			Value: channelLabel(stats.ChannelID),

			Inline: true,

		},

	}, stats, session.Live())

	return embed

}

func appendNowFields(fields []*discordgo.MessageEmbedField, stats pool.Stats, live bool) []*discordgo.MessageEmbedField {

	fields = append(fields, &discordgo.MessageEmbedField{

		Name: "Status",
		Value: statusLabel(stats.Paused),

		Inline: true,

	})

	if live {

		fields = append(fields, &discordgo.MessageEmbedField{

			Name: "Quality",
			Value: fallbackCaption(stats.QualityLabel, "Live"),

			Inline: true,

		})

	} else {

		position := formatDuration(stats.PositionMs)

		if stats.DurationMs != nil {

			position = fmt.Sprintf("%s / %s", position, formatDuration(*stats.DurationMs))

		}

		fields = append(fields,

			&discordgo.MessageEmbedField{Name: "Position", Value: position, Inline: true},
			&discordgo.MessageEmbedField{Name: "Quality", Value: fallbackCaption(stats.QualityLabel, "Auto"), Inline: true},

		)

	}

	return fields

}
