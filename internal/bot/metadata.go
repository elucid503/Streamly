package bot

import (
	"streamly/internal/media"
	"streamly/internal/pool"
	"streamly/internal/tvapi"
)

type streamTarget struct {

	FID int
	ChannelID string

	ShareKey string
	VideoName string
	Target int
	Label string

	Live bool
	Details media.TitleDetails
	Episode *episodeRef

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

		FID: metadata.FID,
		ChannelID: metadata.ChannelID,

		ShareKey: metadata.ShareKey,

		VideoName: metadata.VideoName,

		Target: metadata.Target,
		Label: metadata.Label,

		Live: metadata.Live,

		Details: metadata.Details,
		Episode: episodeRefFromPool(metadata.Episode),

		TVChannel: metadata.TVChannel,

	}

}

func metadataFromStream(details media.TitleDetails, shareKey string, fid int, videoName string, target int, label string, episode *episodeRef, userID string, captionsPreferred bool, autoNext *pool.AutoNextContext, textChannelID, textChannelName string) *pool.StreamMetadata {

	var poolEpisode *pool.EpisodeRef

	if episode != nil {

		poolEpisode = &pool.EpisodeRef{Season: episode.Season, Episode: episode.Episode, Title: episode.Title}

	}

	return &pool.StreamMetadata{

		FID: fid,
		UserID: userID,

		ShareKey: shareKey,

		VideoName: videoName,

		Target: target,
		Label: label,

		Details: details,
		Episode: poolEpisode,

		AutoNext: autoNext,

		CaptionsPreferred: captionsPreferred,

		TextChannelID: textChannelID,
		TextChannelName: textChannelName,

	}

}
