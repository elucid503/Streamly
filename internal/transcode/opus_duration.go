package transcode

import "time"

var opusFrameSizesMs = []float64{
	10, 20, 40, 60,
	10, 20, 40, 60,
	10, 20, 40, 60,
	10, 20,
	10, 20,
	2.5, 5, 10, 20,
	2.5, 5, 10, 20,
	2.5, 5, 10, 20,
	2.5, 5, 10, 20,
}

// opusPacketDuration parses Opus TOC duration the way discord-video-stream LibavDemuxer does.
func opusPacketDuration(frame []byte) time.Duration {

	if len(frame) == 0 {
		return 0
	}

	config := int(frame[0] >> 3)

	if config < 0 || config >= len(opusFrameSizesMs) {
		return 0
	}

	frameSizeMs := opusFrameSizesMs[config]
	frameCount := 1

	switch frame[0] & 0b11 {
	case 1, 2:
		frameCount = 2
	case 3:
		if len(frame) < 2 {
			return 0
		}

		frameCount = int(frame[1] & 0b111111)
	}

	if frameCount <= 0 {
		return 0
	}

	ms := frameSizeMs * float64(frameCount)

	return time.Duration(ms*float64(time.Millisecond) + 0.5)

}
