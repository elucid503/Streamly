package bot

import (
	"streamly/internal/media"
	"streamly/internal/pool"
	"streamly/internal/tvapi"
)

type streamTarget struct {
	ShareKey  string
	FID       int
	VideoName string
	Target    int
	Label     string
	Live      bool
	DaddyID   string
	Details   media.TitleDetails
	Episode   *episodeRef
	TVChannel *tvapi.Channel
}

func streamTargetFromSession(session *pool.Session) (streamTarget, bool) {

	if session == nil || session.Metadata == nil {
		return streamTarget{}, false
	}

	return streamTargetFromMetadata(*session.Metadata), true

}

func streamTargetFromMetadata(metadata pool.StreamMetadata) streamTarget {

	return streamTarget{
		ShareKey:  metadata.ShareKey,
		FID:       metadata.FID,
		VideoName: metadata.VideoName,
		Target:    metadata.Target,
		Label:     metadata.Label,
		Live:      metadata.Live,
		DaddyID:   metadata.DaddyID,
		Details:   metadata.Details,
		Episode:   episodeRefFromPool(metadata.Episode),
		TVChannel: metadata.TVChannel,
	}

}

func metadataFromStream(details media.TitleDetails, shareKey string, fid int, videoName string, target int, label string, episode *episodeRef, userID string, captionsPreferred bool, autoNext *pool.AutoNextContext, textChannelID, textChannelName string) *pool.StreamMetadata {

	var poolEpisode *pool.EpisodeRef

	if episode != nil {
		poolEpisode = &pool.EpisodeRef{Season: episode.Season, Episode: episode.Episode, Title: episode.Title}
	}

	return &pool.StreamMetadata{
		ShareKey:          shareKey,
		FID:               fid,
		VideoName:         videoName,
		Target:            target,
		Label:             label,
		Details:           details,
		Episode:           poolEpisode,
		UserID:            userID,
		CaptionsPreferred: captionsPreferred,
		TextChannelID:     textChannelID,
		TextChannelName:   textChannelName,
		AutoNext:          autoNext,
	}

}