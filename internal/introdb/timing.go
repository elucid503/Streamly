package introdb

import "time"

const creditsStartThreshold = 0.75

func IntroWindow(record *MediaRecord) (startMs, endMs int64, ok bool) {

	if record == nil {

		return 0, 0, false

	}

	viable := viableIntros(record.Intro)

	if len(viable) == 0 {

		return 0, 0, false

	}

	intro := viable[0]

	if intro.EndMs == nil {

		return 0, 0, false

	}

	return intro.StartMs, *intro.EndMs, true

}

func CreditsStart(record *MediaRecord, durationMs int64) (time.Duration, bool) {

	if record == nil || durationMs <= 0 || len(record.Intro) == 0 {

		return 0, false
	}

	threshold := int64(float64(durationMs) * creditsStartThreshold)

	var best *SegmentTimestamp

	for index := range record.Intro {

		segment := &record.Intro[index]

		if segment.EndMs == nil || segment.StartMs < threshold {

			continue
		}

		if best == nil || segment.StartMs > best.StartMs {

			best = segment
		}

	}

	if best != nil {

		return time.Duration(best.StartMs) * time.Millisecond, true
	}

	lateThreshold := int64(float64(durationMs) * 0.65) 	// Fallback: last segment that starts in the final quarter of the runtime.

	for index := len(record.Intro) - 1; index >= 0; index-- {

		segment := &record.Intro[index]

		if segment.EndMs == nil || segment.StartMs < lateThreshold {

			continue
		}

		return time.Duration(segment.StartMs) * time.Millisecond, true

	}

	return 0, false

}
