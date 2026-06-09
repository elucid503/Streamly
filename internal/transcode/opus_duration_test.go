package transcode

import (
	"testing"
	"time"
)

func TestOpusPacketDurationSingleFrame(t *testing.T) {

	// config 30 -> 10ms CELT full band, code 0 -> 1 frame
	frame := []byte{30 << 3}

	if got := opusPacketDuration(frame); got != 10*time.Millisecond {
		t.Fatalf("expected 10ms, got %v", got)
	}

}