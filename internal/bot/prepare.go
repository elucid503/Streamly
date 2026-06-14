package bot

import (
	"context"
	"os"
	"time"

	"streamly/internal/captions"
	"streamly/internal/introdb"
	"streamly/internal/pool"
)

const captionWarmupTimeout = 4 * time.Second

func (b *Bot) prepareStream(session *pool.Session) {

	if fontPath, err := captions.FontPath(); err == nil {

		session.SetCTAFont(fontPath)

	}

	if session.Metadata == nil || session.Metadata.Live {

		return

	}

	b.warmIntroTiming(session, nil)

	if session.Metadata.CaptionsPreferred {

		b.warmCaptions(session)

	}

}

func (b *Bot) warmIntroTiming(session *pool.Session, durationMs *int64) {

	if b.refreshIntroRecord(session, durationMs) && !session.HasIntroCTA() {

		b.armIntroCTA(session)

	}

}

func (b *Bot) refreshIntroRecord(session *pool.Session, durationMs *int64) bool {

	target := session.Metadata

	if target == nil || target.Details.TMDBId <= 0 && target.Details.IMDBId == "" {

		return false

	}

	season := 0
	episode := 0

	if target.Episode != nil {

		season = target.Episode.Season
		episode = target.Episode.Episode

	}

	query, err := introdb.QueryForTitle(target.Details.TMDBId, target.Details.IMDBId, season, episode, durationMs)

	if err != nil {

		return false

	}

	record, err := b.IntroDB.GetMedia(query)

	if err != nil {

		return false

	}

	target.IntroRecord = record

	return true

}

func (b *Bot) armIntroCTA(session *pool.Session) {

	if session.Metadata == nil || session.Metadata.IntroRecord == nil || session.HasIntroCTA() {

		return

	}

	if startMs, endMs, ok := introdb.IntroWindow(session.Metadata.IntroRecord); ok {

		session.SetTimedCTA(pool.TimedCTA{

			Text: pool.IntroSkipCTAText,

			StartMs: startMs,
			EndMs: endMs,

		})

	}

}

func (b *Bot) armIntroOnProbe(session *pool.Session, durationMs int64) {

	if session == nil || session.Metadata == nil || session.Metadata.Live {

		return

	}

	b.refreshIntroRecord(session, &durationMs)

	if !session.HasIntroCTA() {

		b.armIntroCTA(session)

	}

}

func (b *Bot) warmCaptions(session *pool.Session) {

	if session.Metadata == nil {

		return

	}

	fontsDir, err := captions.FontsDir()

	if err != nil {

		return

	}

	target := streamTargetFromMetadata(*session.Metadata)
	query := subtitleQueryFor(target)

	if query.ShareKey != "" && target.FID > 0 && query.VideoName == "" {

		query.VideoName = b.Resolver.FileName(target.ShareKey, target.FID)

	}

	queryKey := captions.QueryKey(query)

	ctx, cancel := context.WithTimeout(context.Background(), captionWarmupTimeout)
	defer cancel()

	file, err := os.CreateTemp("", "streamly-subs-"+session.ID+"-*.srt")

	if err != nil {

		return

	}

	subtitlePath := file.Name()
	file.Close()

	if _, err := b.Captions.Fetch(ctx, query, subtitlePath); err != nil {

		_ = os.Remove(subtitlePath)
		return

	}

	if session.Captions == nil {

		session.Captions = &captions.Track{}

	}

	session.Captions.Set(subtitlePath)
	session.FontsDir = fontsDir
	session.CaptionQueryKey = queryKey

}

func episodeRefFromPool(episode *pool.EpisodeRef) *episodeRef {

	if episode == nil {

		return nil

	}

	return &episodeRef{Season: episode.Season, Episode: episode.Episode, Title: episode.Title}

}
