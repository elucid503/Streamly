package bot

import (
	"fmt"
	"strings"

	"streamly/internal/febapi"
	"streamly/internal/media"
	"streamly/internal/pool"

	"github.com/bwmarrin/discordgo"
)

const (

	maxOptions = 25
	embedColor = 0x96BEFF

)

func truncate(text string, max int) string {

	if len(text) <= max {

		return text

	}

	return text[:max-3] + "..."

}

func baseEmbed(details media.TitleDetails, header string) *discordgo.MessageEmbed {

	title := details.Title

	if details.Year != "" {

		title = fmt.Sprintf("%s (%s)", details.Title, details.Year)

	}

	embed := &discordgo.MessageEmbed{

		Author: &discordgo.MessageEmbedAuthor{Name: header},
		Title: title,

		Color: embedColor,

	}

	if details.Poster != "" {

		embed.Thumbnail = &discordgo.MessageEmbedThumbnail{URL: details.Poster}

	}

	if details.Description != "" {

		embed.Description = truncate(details.Description, 280)

	}

	return embed

}

func simpleEmbed(header, title, description string) *discordgo.MessageEmbed {

	return &discordgo.MessageEmbed{

		Title: title,
		Author: &discordgo.MessageEmbedAuthor{Name: header},

		Description: description,

		Color: embedColor,

	}

}

func controlEmbed(p *pool.Pool, session *pool.Session, header, description string) *discordgo.MessageEmbed {

	title := "Active Stream"

	if session != nil {

		if caption := p.Stats(session).Caption; caption != "" {

			title = caption

		}

	}

	return simpleEmbed(header, title, description)

}

func controlRow(sessionID string, paused, live bool) []discordgo.MessageComponent {

	label := "Pause"
	kind := "pause"

	if !live && paused {

		label = "Resume"
		kind = "resume"

	}

	components := []discordgo.MessageComponent{

		discordgo.Button{

			Label: label,
			CustomID: fmt.Sprintf("stream:%s:%s", kind, sessionID),

			Style: discordgo.SecondaryButton,
			Disabled: live,

		},

		discordgo.Button{

			Label: "Stop",
			CustomID: fmt.Sprintf("stream:stop:%s", sessionID),

			Style: discordgo.DangerButton,

		},

	}

	return []discordgo.MessageComponent{

		discordgo.ActionsRow{Components: components},

	}

}

func endedControlRow() []discordgo.MessageComponent {

	return []discordgo.MessageComponent{

		discordgo.ActionsRow{Components: []discordgo.MessageComponent{

			discordgo.Button{Label: "Pause", CustomID: "stream:ended:pause", Style: discordgo.SecondaryButton, Disabled: true},
			discordgo.Button{Label: "Stop", CustomID: "stream:ended:stop", Style: discordgo.DangerButton, Disabled: true},

		}},
	}

}

func endedCard(embeds []*discordgo.MessageEmbed, label string, attachments []*discordgo.MessageAttachment) ([]*discordgo.MessageEmbed, []discordgo.MessageComponent) {

	if len(embeds) == 0 {

		return nil, endedControlRow()

	}

	card := *embeds[0]
	card.Author = &discordgo.MessageEmbedAuthor{Name: label}
	normalizeEndedThumbnail(&card, attachments)

	return []*discordgo.MessageEmbed{&card}, endedControlRow()

}

// Fixes it so that the thumbnail is not an attachment URL, but a direct URL to the image.
func normalizeEndedThumbnail(card *discordgo.MessageEmbed, attachments []*discordgo.MessageAttachment) {

	if card.Thumbnail == nil {

		return

	}

	if !strings.HasPrefix(card.Thumbnail.URL, "attachment://") {

		return

	}

	name := strings.TrimPrefix(card.Thumbnail.URL, "attachment://")

	for _, attachment := range attachments {

		if attachment.Filename == name && attachment.URL != "" {

			card.Thumbnail = &discordgo.MessageEmbedThumbnail{URL: attachment.URL}
			return

		}

	}

	card.Thumbnail = nil

}

func clearAttachments() *[]*discordgo.MessageAttachment {

	empty := []*discordgo.MessageAttachment{}

	return &empty

}

func closeStreamMessage(s *discordgo.Session, i *discordgo.InteractionCreate, embed *discordgo.MessageEmbed, label string) {

	embeds, components := endedCard([]*discordgo.MessageEmbed{embed}, label, nil)
	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &embeds, Components: &components, Attachments: clearAttachments()})

}

func memberVoiceChannel(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.Channel {

	channelID := voiceChannelID(s, i)

	if channelID == "" {

		return nil

	}

	channel, err := s.Channel(channelID)

	if err != nil {

		return nil

	}

	return channel

}

func voiceChannelID(s *discordgo.Session, i *discordgo.InteractionCreate) string {

	if i.Member == nil || i.Member.User == nil {

		return ""

	}

	guild, err := s.State.Guild(i.GuildID)

	if err != nil {

		return ""

	}

	for _, state := range guild.VoiceStates {

		if state.UserID == i.Member.User.ID {

			return state.ChannelID

		}

	}

	return ""

}

func activeSession(s *discordgo.Session, i *discordgo.InteractionCreate, p *pool.Pool) *pool.Session {

	if i.GuildID == "" {

		return nil

	}

	return p.Active(i.GuildID, voiceChannelID(s, i))

}

func formatBytes(bytes int64) string {

	if bytes < 1024 {

		return fmt.Sprintf("%d B", bytes)

	}

	units := []string{"KB", "MB", "GB", "TB"}
	value := float64(bytes) / 1024
	index := 0

	for value >= 1024 && index < len(units)-1 {

		value /= 1024
		index++

	}

	switch {

		case value >= 100:

			return fmt.Sprintf("%.0f %s", value, units[index])

		case value >= 10:

			return fmt.Sprintf("%.1f %s", value, units[index])

		default:

			return fmt.Sprintf("%.2f %s", value, units[index])

	}

}

func formatDuration(ms int64) string {

	total := max64(0, ms / 1000)

	hours := total / 3600
	minutes := (total % 3600) / 60
	seconds := total % 60

	if hours > 0 {

		return fmt.Sprintf("%dh %dm %ds", hours, minutes, seconds)

	}

	if minutes > 0 {

		return fmt.Sprintf("%dm %ds", minutes, seconds)

	}

	return fmt.Sprintf("%ds", seconds)

}

func formatClock(ms int64) string {

	total := max64(0, ms / 1000)

	hours := total / 3600
	minutes := (total % 3600) / 60
	seconds := total % 60

	if hours > 0 {

		return fmt.Sprintf("%d:%02d:%02d", hours, minutes, seconds)

	}

	return fmt.Sprintf("%d:%02d", minutes, seconds)

}

func qualityLabel(quality febapi.FileQuality) string {

	label := strings.TrimSpace(quality.Quality + " " + quality.Name)

	if strings.Contains(strings.ToLower(label), "org") || strings.Contains(strings.ToLower(label), "origin") {

		return "Original"

	}

	return truncate(label, 100)

}

func max64(a, b int64) int64 {

	if a > b {

		return a

	}

	return b

}
