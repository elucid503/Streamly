//go:build !cgo

package transcode

import "fmt"

func startNative(request Request) (*Session, error) {

	_ = request

	return nil, fmt.Errorf("transcode requires CGO_ENABLED=1 and libav dev libraries")

}
