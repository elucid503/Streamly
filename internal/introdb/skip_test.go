package introdb

import (
	"errors"
	"testing"
	"time"
)

func TestIntroSkipTarget(t *testing.T) {

	end := func(ms int64) *int64 { return &ms }

	record := &MediaRecord{
		Intro: []SegmentTimestamp{
			{StartMs: 30_000, EndMs: end(90_000)},
		},
	}

	target, err := IntroSkipTarget(record, 45*time.Second)
	if err != nil || target != 90*time.Second {
		t.Fatalf("in intro: got %v err=%v", target, err)
	}

	target, err = IntroSkipTarget(record, time.Duration(0))
	if err != nil || target != 90*time.Second {
		t.Fatalf("stream start before intro: got %v err=%v", target, err)
	}

	_, err = IntroSkipTarget(record, 95*time.Second)
	if !errors.Is(err, ErrPastIntro) {
		t.Fatalf("after intro: got %v", err)
	}

	stub := &MediaRecord{
		Intro: []SegmentTimestamp{
			{StartMs: 0, EndMs: end(5_000)},
			{StartMs: 228_694, EndMs: end(245_250)},
		},
	}

	for _, pos := range []time.Duration{0, 2 * time.Second, 5 * time.Second} {
		target, err = IntroSkipTarget(stub, pos)
		if err != nil || target != 245250*time.Millisecond {
			t.Fatalf("pos %v: got %v err=%v", pos, target, err)
		}
	}

	multi := &MediaRecord{
		Intro: []SegmentTimestamp{
			{StartMs: 0, EndMs: end(90_000)},
			{StartMs: 1_200_000, EndMs: end(1_290_000)},
		},
	}

	_, err = IntroSkipTarget(multi, 100*time.Second)
	if !errors.Is(err, ErrNotInIntro) {
		t.Fatalf("between intros: got %v", err)
	}

	target, err = IntroSkipTarget(multi, 1250*time.Second)
	if err != nil || target != 1290*time.Second {
		t.Fatalf("second intro window: got %v err=%v", target, err)
	}

	_, err = IntroSkipTarget(&MediaRecord{}, time.Second)
	if !errors.Is(err, ErrNoIntroData) {
		t.Fatalf("empty record: got %v", err)
	}

}