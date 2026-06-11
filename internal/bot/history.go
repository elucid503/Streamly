package bot

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

const recentSearchLimit = 3

func (b *Bot) recordHistory(i *discordgo.InteractionCreate, title, value string) {

	userID := interactionUserID(i)

	if b.DB == nil || userID == "" {
		return
	}

	go func() {

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := b.DB.RecordStream(ctx, userID, title, value); err != nil {
			log.Printf("failed to record stream history: %v", err)
		}

	}()

}

func (b *Bot) recentSearchChoices(ctx context.Context, i *discordgo.InteractionCreate, query string) []*discordgo.ApplicationCommandOptionChoice {

	userID := interactionUserID(i)

	if b.DB == nil || userID == "" {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()

	entries, err := b.DB.RecentSearches(ctx, userID, 10)

	if err != nil {
		log.Printf("failed to load stream history: %v", err)
		return nil
	}

	var choices []*discordgo.ApplicationCommandOptionChoice

	for _, entry := range entries {

		if !historyMatchesQuery(entry.Title, query) {
			continue
		}

		choices = append(choices, &discordgo.ApplicationCommandOptionChoice{
			Name:  historyAutocompleteLabel(entry.Title),
			Value: entry.Value,
		})

		if len(choices) >= recentSearchLimit {
			break
		}

	}

	return choices

}

func historyAutocompleteLabel(title string) string {

	return truncate("Recent Search • "+title, 100)

}

func historyMatchesQuery(title, query string) bool {

	query = strings.TrimSpace(query)

	if query == "" {
		return true
	}

	return strings.Contains(strings.ToLower(title), strings.ToLower(query))

}

func interactionUserID(i *discordgo.InteractionCreate) string {

	if i.Member != nil && i.Member.User != nil {
		return i.Member.User.ID
	}

	if i.User != nil {
		return i.User.ID
	}

	return ""

}
