package bot

import (
	"fmt"
	"strconv"
	"strings"

	"streamly/internal/febapi"
	"streamly/internal/media"

	"github.com/bwmarrin/discordgo"
)

func (b *Bot) handleTop(s *discordgo.Session, i *discordgo.InteractionCreate) {

	_ = deferReply(s, i)

	results, err := b.Resolver.Top(media.TopLimit)

	if err != nil {

		editMessage(s, i, &discordgo.WebhookEdit{Content: strPtr("Couldn't load trending titles right now.")})
		return

	}

	if len(results) == 0 {

		editMessage(s, i, &discordgo.WebhookEdit{Content: strPtr("No trending titles were found.")})
		return

	}

	embed := topEmbed(results)
	editMessage(s, i, &discordgo.WebhookEdit{Embeds: ptrEmbeds([]*discordgo.MessageEmbed{embed})})

}

func topEmbed(results []febapi.SearchResult) *discordgo.MessageEmbed {

	embed := &discordgo.MessageEmbed{

		Color: embedColor,
		Title: "Top",

	}

	if len(results) > 0 && results[0].Poster != "" {

		embed.Thumbnail = &discordgo.MessageEmbedThumbnail{URL: results[0].Poster}

	}

	var lines []string

	for index, result := range results {

		lines = append(lines, fmt.Sprintf("**%d.** %s", index+1, topLine(result)))

	}

	embed.Description = strings.Join(lines, "\n")

	return embed

}

func topLine(result febapi.SearchResult) string {

	kind := "Movie"

	if result.BoxType == febapi.BoxSeries {

		kind = "TV Show"

	}

	title := result.Title

	if result.Year > 0 {

		title = fmt.Sprintf("%s (%d)", result.Title, result.Year)

	}

	line := fmt.Sprintf("%s — %s", title, kind)

	rating := "?"

	if raw := strings.TrimSpace(result.IMDBRating); raw != "" {

		if score, err := strconv.ParseFloat(raw, 64); err == nil && score > 0 {

			rating = raw

		}

	}

	line += fmt.Sprintf(" · %s/10", rating)

	return line

}
