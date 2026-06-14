package bot

import (
	"fmt"
	"runtime"

	"streamly/internal/pool"

	"github.com/bwmarrin/discordgo"
)

func (b *Bot) handleStats(s *discordgo.Session, i *discordgo.InteractionCreate) {

	session := activeSession(s, i, b.Pool)

	if session == nil {

		respondEphemeral(s, i, "No active stream was found for this server.")
		return

	}

	stats := b.Pool.Stats(session)

	embed := &discordgo.MessageEmbed{

		Author: &discordgo.MessageEmbedAuthor{Name: "Stream Stats"},
		Title: fallbackCaption(stats.Caption, "Active Stream"),

		Fields: []*discordgo.MessageEmbedField{

			{Name: "Status", Value: statusLabel(stats.Paused), Inline: true},
			{Name: "Uptime", Value: formatClock(stats.UptimeMs), Inline: true},
			{Name: "Channel", Value: channelLabel(stats.ChannelID), Inline: true},
			{Name: "Position", Value: positionLabel(stats.PositionMs, stats.DurationMs), Inline: true},
			{Name: "Memory", Value: memoryUsageLabel(), Inline: true},
			{Name: "Quality", Value: fallbackCaption(stats.QualityLabel, "Auto"), Inline: true},
			{Name: "Read", Value: formatBytes(stats.BytesRead), Inline: true},
			{Name: "Subtitles", Value: subtitlesLabel(stats), Inline: true},
			{Name: "Progress", Value: progressLabel(stats.PositionMs, stats.DurationMs), Inline: true},
		},

		Color: embedColor,

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

func subtitlesLabel(stats pool.Stats) string {

	if stats.CaptionsEnabled {

		if stats.CaptionSource != "" {

			return "On (" + stats.CaptionSource + ")"

		}

		return "On"

	}

	return "Off"

}

func fallbackCaption(value, fallback string) string {

	if value == "" {

		return fallback

	}

	return value

}

func positionLabel(positionMs int64, durationMs *int64) string {

	if durationMs == nil {

		return formatClock(positionMs)

	}

	return fmt.Sprintf("%s / %s", formatClock(positionMs), formatClock(*durationMs))

}

func progressLabel(positionMs int64, durationMs *int64) string {

	if durationMs == nil || *durationMs <= 0 {

		return "—"

	}

	percent := float64(positionMs) / float64(*durationMs) * 100

	if percent > 100 {

		percent = 100

	}

	return fmt.Sprintf("%.0f%%", percent)

}

func memoryUsageLabel() string {

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	return fmt.Sprintf("%s / %s MB", formatMegabytes(mem.HeapInuse), formatMegabytes(mem.Sys))

}

func formatMegabytes(bytes uint64) string {

	return fmt.Sprintf("%.0f", float64(bytes)/(1024*1024))

}
