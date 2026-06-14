package captions

import "context"

type RemoteProvider interface {

	Name() string

	Configured() bool

	Download(ctx context.Context, query Query, destPath string) (string, error)

}
