package captions

import "testing"

func TestHasForeignLanguageName(t *testing.T) {

	cases := map[string]bool{
		"The.Sopranos.S02E10.BluRay.srt":     false,
		"The.Sopranos.S02E10.ar.srt":         true,
		"show.en.srt":                        false,
		"show.hi.srt":                        false,
		"show.hindi.srt":                     true,
		"the-sopranos-second-season_2_HI_english-3219271.zip": false,
	}

	for name, want := range cases {
		if got := hasForeignLanguageName(name); got != want {
			t.Fatalf("%q: got %v want %v", name, got, want)
		}
	}

}

func TestLooksEnglishSubtitleArabic(t *testing.T) {

	arabic := []byte(`1
00:00:01,000 --> 00:00:04,000
لذا، أكرر، نظموا كل وثائقكم الأكاديمية

2
00:00:04,500 --> 00:00:07,000
والأنشطة الإضافية
`)

	if looksEnglishSubtitle(arabic) {
		t.Fatal("expected arabic subtitle to be rejected")
	}

	english := []byte(`1
00:00:01,000 --> 00:00:04,000
So I repeat, organize all your academic documents.
`)

	if !looksEnglishSubtitle(english) {
		t.Fatal("expected english subtitle to be accepted")
	}

}