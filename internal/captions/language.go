package captions

import (
	"os"
	"strconv"
	"strings"
	"unicode"
)

var foreignSubtitleTokens = []string{
	".ar.", "_ar_", ".ara.", ".arabic.",
	".fr.", "_fr_", ".fre.", ".french.",
	".es.", "_es_", ".spa.", ".spanish.",
	".de.", "_de_", ".ger.", ".german.",
	".it.", "_it_", ".ita.", ".italian.",
	".pt.", "_pt_", ".por.", ".portuguese.",
	".ru.", "_ru_", ".rus.", ".russian.",
	".tr.", "_tr_", ".tur.", ".turkish.",
	".pl.", "_pl_", ".pol.", ".polish.",
	".nl.", "_nl_", ".dut.", ".dutch.",
	".sv.", "_sv_", ".swe.", ".swedish.",
	".no.", "_no_", ".nor.", ".norwegian.",
	".da.", "_da_", ".dan.", ".danish.",
	".fi.", "_fi_", ".fin.", ".finnish.",
	".el.", "_el_", ".gre.", ".greek.",
	".he.", "_he_", ".heb.", ".hebrew.",
	".fa.", "_fa_", ".per.", ".persian.",
	".hin.", ".hindi.",
	".ja.", "_ja_", ".jpn.", ".japanese.",
	".ko.", "_ko_", ".kor.", ".korean.",
	".zh.", "_zh_", ".chi.", ".chinese.",
	".vi.", "_vi_", ".vie.", ".vietnamese.",
	".th.", "_th_", ".tha.", ".thai.",
	".uk.", "_uk_", ".ukr.", ".ukrainian.",
	".cs.", "_cs_", ".cze.", ".czech.",
	".ro.", "_ro_", ".rum.", ".romanian.",
	".hu.", "_hu_", ".hun.", ".hungarian.",
	".bg.", "_bg_", ".bul.", ".bulgarian.",
}

var englishSubtitleTokens = []string{
	"english", ".en.", "_en_", ".eng.", " eng", ".english.",
}

// hasForeignLanguageName reports whether a subtitle filename names a non-English track.
func hasForeignLanguageName(name string) bool {

	lower := strings.ToLower(name)

	for _, token := range foreignSubtitleTokens {
		if strings.Contains(lower, token) {
			return true
		}
	}

	return false

}

// looksEnglishName reports whether a subtitle filename may be English (not explicitly foreign).
func looksEnglishName(name string) bool {

	return !hasForeignLanguageName(name)

}

func looksEnglishLanguageTag(language string) bool {

	language = strings.ToLower(strings.TrimSpace(language))

	if language == "" {
		return true
	}

	switch language {
	case "en", "eng", "english", "en-us", "en-gb", "en_us", "en_gb":
		return true
	}

	for _, token := range foreignSubtitleTokens {
		if strings.Contains(language, strings.Trim(strings.Trim(token, "."), "_")) {
			return false
		}
	}

	return false

}

// looksEnglishSubtitle reports whether subtitle text appears to be English.
func looksEnglishSubtitle(data []byte) bool {

	sample := subtitleDialogueSample(data)

	if sample == "" {
		return true
	}

	var latin, arabic, cyrillic, cjk int

	for _, r := range sample {

		switch {
		case isLatinLetter(r):
			latin++
		case r >= 0x0600 && r <= 0x06FF:
			arabic++
		case r >= 0x0400 && r <= 0x04FF:
			cyrillic++
		case unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul):
			cjk++
		}

	}

	if arabic > 0 && arabic >= latin/3 {
		return false
	}

	if cyrillic > 0 && cyrillic >= latin/3 {
		return false
	}

	if cjk > 0 && cjk >= latin/3 {
		return false
	}

	return latin > 0

}

func subtitleDialogueSample(data []byte) string {

	lines := strings.Split(string(data), "\n")
	var parts []string

	for _, line := range lines {
		line = strings.TrimSpace(line)

		if line == "" || strings.Contains(line, "-->") {
			continue
		}

		if _, err := strconv.Atoi(line); err == nil {
			continue
		}

		parts = append(parts, line)

		if len(parts) >= 12 {
			break
		}

	}

	return strings.Join(parts, " ")

}

func isLatinLetter(r rune) bool {

	return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r < 128 && unicode.IsLetter(r) && !unicode.Is(unicode.Greek, r))

}

// ValidateSubtitleFile reports whether a cached subtitle file is usable English dialogue.
func ValidateSubtitleFile(path string) bool {

	data, err := os.ReadFile(path)

	if err != nil {
		return false
	}

	return looksEnglishSubtitle(data)

}