package bot

import (
	"fmt"
	"runtime"

	"github.com/bwmarrin/discordgo"
)

func (b *Bot) handleStats(s *discordgo.Session, i *discordgo.InteractionCreate) {

	session := activeSession(s, i, b.Pool)

	if session == nil {
		respondEphemeral(s, i, "No active stream was found for this server.")
		return
	}

	stats := b.Pool.Stats(session)

	position := formatDuration(stats.PositionMs)
	positionField := position

	if stats.DurationMs != nil {
		positionField = fmt.Sprintf("%s / %s", position, formatDuration(*stats.DurationMs))
	}

	embed := &discordgo.MessageEmbed{
		Color:  0x5865f2,
		Author: &discordgo.MessageEmbedAuthor{Name: "Stream Stats"},
		Title:  fallbackCaption(stats.Caption, "Active Stream"),
		Fields: []*discordgo.MessageEmbedField{
			{Name: "Status", Value: statusLabel(stats.Paused), Inline: true},
			{Name: "Uptime", Value: formatDuration(stats.UptimeMs), Inline: true},
			{Name: "Channel", Value: channelLabel(stats.ChannelID), Inline: true},
			{Name: "Position", Value: positionField, Inline: true},
			{Name: "Memory", Value: formatBytes(memoryRSS()), Inline: true},
			{Name: "Quality", Value: fallbackCaption(stats.QualityLabel, "Auto"), Inline: true},
		},
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{embed}, Flags: discordgo.MessageFlagsEphemeral},
	})

}

func statusLabel(paused bool) string {

	if paused {
		return "Paused"
	}

	return "Streaming"

}

func channelLabel(channelID string) string {

	if channelID == "" {
		return "Unknown"
	}

	return fmt.Sprintf("<#%s>", channelID)

}

func fallbackCaption(value, fallback string) string {

	if value == "" {
		return fallback
	}

	return value

}

func memoryRSS() int64 {

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	return int64(mem.Sys)

}