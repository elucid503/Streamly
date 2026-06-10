package media

import "testing"

func TestStreamFilePreference(t *testing.T) {

	h264 := "The.Sopranos.S02E02.1080p.5.1Ch.BluRay.ReEnc-DeeJayAhmed.mkv"
	hevc := "The.Sopranos.S02E02.1080p.BluRay.x265-RARBG.mp4"

	if StreamFilePreference(h264) <= StreamFilePreference(hevc) {
		t.Fatalf("expected h264 bluray (%d) to beat hevc rarbg (%d)",
			StreamFilePreference(h264), StreamFilePreference(hevc))
	}

}
