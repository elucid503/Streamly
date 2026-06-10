package bot

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/bwmarrin/discordgo"

	"streamly/internal/captions"
	"streamly/internal/pool"
)

const (
	subtitleModeEnabled  = "enabled"
	subtitleModeDisabled = "disabled"
)

func (b *Bot) handleSubtitles(s *discordgo.Session, i *discordgo.InteractionCreate) {

	mode := optionString(i, "mode")

	session := activeSession(s, i, b.Pool)

	if session == nil || !session.Busy {
		respondEmbed(s, i, simpleEmbed("Stream Control", "No Active Stream", "No active stream was found for this server."))
		return
	}

	if session.Live() {
		respondEmbed(s, i, controlEmbed(b.Pool, session, "Live TV", "Live streams do not support subtitles."))
		return
	}

	target, ok := streamMedia[session.ID]

	if !ok {
		respondEmbed(s, i, controlEmbed(b.Pool, session, "Subtitles Unavailable", "Couldn't identify this stream for subtitle lookup."))
		return
	}

	switch mode {
	case subtitleModeDisabled:
		b.disableSubtitles(s, i, session)
	case subtitleModeEnabled:
		b.enableSubtitles(s, i, session, target)
	default:
		respondEmbed(s, i, controlEmbed(b.Pool, session, "Subtitles Failed", "Choose Enabled or Disabled for subtitles."))
	}

}

func (b *Bot) disableSubtitles(s *discordgo.Session, i *discordgo.InteractionCreate, session *pool.Session) {

	if !b.Pool.CaptionsEnabled(session) {
		respondEmbed(s, i, controlEmbed(b.Pool, session, "Already Disabled", "Subtitles are already off for this stream."))
		return
	}

	enabled, err := b.Pool.SetCaptions(session, false, "", "", "", "")

	if err != nil {
		respondEmbed(s, i, controlEmbed(b.Pool, session, "Subtitles Failed", "Couldn't turn subtitles off right now."))
		return
	}

	if !enabled {
		respondEmbed(s, i, controlEmbed(b.Pool, session, "Subtitles Disabled", "Subtitles are now off."))
	}

}

func (b *Bot) enableSubtitles(s *discordgo.Session, i *discordgo.InteractionCreate, session *pool.Session, target streamTarget) {

	if b.Pool.CaptionsEnabled(session) {
		respondEmbed(s, i, controlEmbed(b.Pool, session, "Already Enabled", "Subtitles are already on for this stream."))
		return
	}

	fontsDir, err := captions.FontsDir()

	if err != nil {
		respondEmbed(s, i, controlEmbed(b.Pool, session, "Font Missing", "Drop assets/font.ttf next to the bot binary, then try again."))
		return
	}

	query := subtitleQueryFor(target)

	if query.ShareKey != "" && target.FID > 0 && query.VideoName == "" {
		query.VideoName = b.Resolver.FileName(target.ShareKey, target.FID)
	}

	queryKey := captions.QueryKey(query)

	if session.Captions != nil && session.Captions.HasSubtitle() && session.CaptionQueryKey == queryKey {

		if !captions.ValidateSubtitleFile(session.Captions.StoredPath()) {
			session.Captions.Reset()
		} else {
			enabled, err := b.Pool.SetCaptions(session, true, "", fontsDir, session.CaptionSource, queryKey)

			if err != nil {
				respondEmbed(s, i, b.subtitlesFailedEmbed(session, err))
				return
			}

			if enabled {
				respondEmbed(s, i, controlEmbed(b.Pool, session, "Subtitles Enabled", "Subtitles are now on."))
			}

			return
		}

	}

	if session.Captions != nil {
		session.Captions.Reset()
	}

	file, fileErr := os.CreateTemp("", "streamly-subs-"+session.ID+"-*.srt")

	if fileErr != nil {
		respondEmbed(s, i, controlEmbed(b.Pool, session, "Subtitles Failed", "Couldn't prepare a subtitle file."))
		return
	}

	subtitlePath := file.Name()
	file.Close()

	_, fetchErr := b.Captions.Fetch(context.Background(), query, subtitlePath)

	if fetchErr != nil {
		_ = os.Remove(subtitlePath)
		respondEmbed(s, i, b.subtitlesFailedEmbed(session, fetchErr))
		return
	}

	enabled, applyErr := b.Pool.SetCaptions(session, true, subtitlePath, fontsDir, "", queryKey)

	if applyErr != nil {
		_ = os.Remove(subtitlePath)
		respondEmbed(s, i, b.subtitlesFailedEmbed(session, applyErr))
		return
	}

	if enabled {
		respondEmbed(s, i, controlEmbed(b.Pool, session, "Subtitles Enabled", "Subtitles are now on."))
	}

}

func subtitleQueryFor(target streamTarget) captions.Query {

	season := 0
	episode := 0

	if target.Episode != nil {
		season = target.Episode.Season
		episode = target.Episode.Episode
	}

	return captions.Query{
		IMDBId:    target.Details.IMDBId,
		TMDBId:    target.Details.TMDBId,
		ShareKey:  target.ShareKey,
		VideoFID:  target.FID,
		VideoName: target.VideoName,
		Season:    season,
		Episode:   episode,
	}

}

func (b *Bot) subtitlesFailedEmbed(session *pool.Session, err error) *discordgo.MessageEmbed {

	switch {
	case errors.Is(err, captions.ErrNoSubtitle):
		return controlEmbed(b.Pool, session, "No Subtitles Found", "No English subtitles were found for this title.")
	case errors.Is(err, captions.ErrNoFont):
		return controlEmbed(b.Pool, session, "Font Missing", "Drop assets/font.ttf next to the bot binary, then try again.")
	case errors.Is(err, captions.ErrUnconfigured):
		return controlEmbed(b.Pool, session, "Subtitles Unavailable", "No subtitles were found for this file.")
	case errors.Is(err, captions.ErrUnauthorized):
		return controlEmbed(b.Pool, session, "Subtitles Failed", "Couldn't fetch subtitles with the current configuration.")
	case errors.Is(err, captions.ErrRateLimited):
		return controlEmbed(b.Pool, session, "Subtitles Failed", "Subtitle lookup was rate limited. Try again shortly.")
	case errors.Is(err, captions.ErrUnseekable), errors.Is(err, pool.ErrUnseekable):
		return controlEmbed(b.Pool, session, "Subtitles Unavailable", "This stream's source doesn't support restarting for subtitle burn-in.")
	default:
		if strings.Contains(err.Error(), "no stream metadata") {
			return controlEmbed(b.Pool, session, "Subtitles Unavailable", "Couldn't identify this stream for subtitle lookup.")
		}
		return controlEmbed(b.Pool, session, "Subtitles Failed", "Couldn't enable subtitles right now.")
	}

}
