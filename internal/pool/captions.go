package pool

import (
	"time"

	"streamly/internal/captions"
	"streamly/internal/source"
)

// CaptionsEnabled reports whether burned-in captions are active.
func (p *Pool) CaptionsEnabled(session *Session) bool {

	return session.Captions != nil && session.Captions.Enabled()

}

// SetCaptions toggles burned-in subtitles and restarts playback at the current position.
func (p *Pool) SetCaptions(session *Session, enabled bool, subtitlePath, fontsDir, sourceLabel, queryKey string) (bool, error) {

	if !session.Busy || session.request == nil {
		return false, captions.ErrNoMetadata
	}

	if session.request.Live {
		return false, captions.ErrLiveUnsupported
	}

	if source.IsHlsURL(session.request.InitialURL) {
		return false, captions.ErrUnseekable
	}

	if session.Captions == nil {
		session.Captions = &captions.Track{}
	}

	if enabled {

		if subtitlePath != "" {
			session.Captions.Set(subtitlePath)
			session.FontsDir = fontsDir
			session.CaptionSource = sourceLabel
			session.CaptionQueryKey = queryKey
		} else if !session.Captions.HasSubtitle() {
			return false, captions.ErrNoSubtitle
		} else {
			session.Captions.Enable()
		}

	} else {
		session.Captions.Disable()
	}

	if err := p.restartAtCurrentPosition(session); err != nil {
		return session.Captions.Enabled(), err
	}

	return session.Captions.Enabled(), nil

}

func (p *Pool) restartAtCurrentPosition(session *Session) error {

	position := time.Duration(p.positionMs(session)) * time.Millisecond

	_, err := p.Seek(session, position)

	return err

}