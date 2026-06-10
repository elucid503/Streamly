package captions

import "context"

// RemoteProvider fetches subtitles from an online catalog.
type RemoteProvider interface {
	Name() string
	Configured() bool
	Download(ctx context.Context, query Query, destPath string) (string, error)
}
