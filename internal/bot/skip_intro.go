package bot

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"streamly/internal/introdb"
	"streamly/internal/pool"

	"github.com/bwmarrin/discordgo"
)

const skipIntroValue = "skip-intro"

func (b *Bot) handleSkipIntro(s *discordgo.Session, i *discordgo.InteractionCreate) {

	session := activeSession(s, i, b.Pool)

	if session == nil || !session.Busy {

		respondEmbed(s, i, simpleEmbed("Stream Control", "No Active Stream", "No active stream was found for this server."))
		return

	}

	if session.Live() {

		respondEmbed(s, i, controlEmbed(b.Pool, session, "Live TV", "Live streams cannot be seeked."))
		return

	}

	b.executeSkipIntro(s, i, session)

}

func (b *Bot) executeSkipIntro(s *discordgo.Session, i *discordgo.InteractionCreate, session *pool.Session) {

	current := time.Duration(b.Pool.Stats(session).PositionMs) * time.Millisecond

	target, err := b.resolveIntroSkipTarget(session, current)

	if errors.Is(err, introdb.ErrNoIntroData) {

		respondEmbed(s, i, controlEmbed(b.Pool, session, "No Intro Data", "No intro timing was recorded for this title."))
		return

	}

	if errors.Is(err, introdb.ErrPastIntro) {

		respondEmbed(s, i, controlEmbed(b.Pool, session, "Past Intro", "You're already past the intro."))
		return

	}

	if errors.Is(err, introdb.ErrNotInIntro) {

		respondEmbed(s, i, controlEmbed(b.Pool, session, "Not In Intro", "You're not in the intro right now."))
		return

	}

	if err != nil {

		respondEmbed(s, i, b.introLookupFailedEmbed(session, err))
		return

	}

	actual, err := b.Pool.Seek(session, target)

	if errors.Is(err, pool.ErrUnseekable) {

		respondEmbed(s, i, controlEmbed(b.Pool, session, "Seek Unavailable", "This stream's source doesn't support seeking."))
		return

	}

	if err != nil {

		respondEmbed(s, i, controlEmbed(b.Pool, session, "Seek Failed", "Couldn't seek this stream right now."))
		return

	}

	respondEmbed(s, i, controlEmbed(b.Pool, session, "Skipping Intro", fmt.Sprintf("Jumping to %s.", formatDuration(actual.Milliseconds()))))

}

func (b *Bot) introLookupFailedEmbed(session *pool.Session, err error) *discordgo.MessageEmbed {

	switch {

		case errors.Is(err, introdb.ErrBlocked):

			return controlEmbed(b.Pool, session, "Intro Lookup Failed", "TheIntroDB blocked this request at the network edge. Try again shortly.")

		case errors.Is(err, introdb.ErrUnauthorized):

			return controlEmbed(b.Pool, session, "Intro Lookup Failed", "TheIntroDB rejected the configured API key.")

		case errors.Is(err, introdb.ErrAccountLocked):

			return controlEmbed(b.Pool, session, "Intro Lookup Failed", "TheIntroDB account tied to this API key is locked.")

		case errors.Is(err, introdb.ErrRateLimited):

			return controlEmbed(b.Pool, session, "Intro Lookup Failed", "TheIntroDB rate limit was hit. Try again shortly.")

		default:

			if strings.Contains(err.Error(), "no stream metadata") {

				return controlEmbed(b.Pool, session, "Intro Lookup Failed", "Couldn't identify this stream for intro lookup.")

			}

			return controlEmbed(b.Pool, session, "Intro Lookup Failed", "Couldn't look up intro timing right now.")

	}

}

func (b *Bot) resolveIntroSkipTarget(session *pool.Session, current time.Duration) (time.Duration, error) {

	target, ok := streamTargetFromSession(session)

	if !ok {

		return 0, errors.New("no stream metadata")

	}

	season := 0
	episode := 0

	if target.Episode != nil {

		season = target.Episode.Season
		episode = target.Episode.Episode

	}

	stats := b.Pool.Stats(session)

	query, err := introdb.QueryForTitle(target.Details.TMDBId, target.Details.IMDBId, season, episode, stats.DurationMs)

	if err != nil {

		return 0, err

	}

	record, err := b.IntroDB.GetMedia(query)

	if err != nil {

		return 0, introdb.MapGetMediaError(err)

	}

	return introdb.IntroSkipTarget(record, current)

}

func skipIntroEligible(session *pool.Session) bool {

	if session == nil || !session.Busy || session.Live() {

		return false

	}

	target, ok := streamTargetFromSession(session)

	if !ok {

		return false

	}

	if target.Details.TMDBId <= 0 && strings.TrimSpace(target.Details.IMDBId) == "" {

		return false

	}

	return true

}

func (b *Bot) onSeekAutocomplete(s *discordgo.Session, i *discordgo.InteractionCreate) {

	query := ""

	for _, option := range i.ApplicationCommandData().Options {

		if option.Focused {

			query = strings.TrimSpace(option.StringValue())

		}

	}

	var choices []*discordgo.ApplicationCommandOptionChoice

	session := activeSession(s, i, b.Pool)

	if skipIntroEligible(session) && autocompleteMatches("Skip Intro", query) {

		choices = append(choices, &discordgo.ApplicationCommandOptionChoice{

			Name: "Skip Intro",
			Value: skipIntroValue,
		})

	}

	for _, preset := range seekPositionChoices {

		if autocompleteMatches(preset.Name, query) {

			choices = append(choices, preset)

		}

		if len(choices) >= maxOptions {

			break

		}

	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{

		Type: discordgo.InteractionApplicationCommandAutocompleteResult,
		Data: &discordgo.InteractionResponseData{Choices: choices},

	})

}

func autocompleteMatches(label, query string) bool {

	if query == "" {

		return true

	}

	return strings.Contains(strings.ToLower(label), strings.ToLower(query))

}
