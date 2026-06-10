package captions

import (
	"archive/zip"
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

func extractSubtitle(data []byte, season, episode int) ([]byte, error) {

	var payload []byte
	var err error

	if len(data) >= 4 && data[0] == 'P' && data[1] == 'K' {
		payload, err = extractFromZip(data, season, episode)
	} else if looksLikeSubtitle(data) && looksEnglishSubtitle(data) {
		payload = data
	} else {
		return nil, ErrNoSubtitle
	}

	if err != nil {
		return nil, err
	}

	return normalizeSubtitleUTF8(payload), nil

}

// normalizeSubtitleUTF8 re-encodes subtitle text as UTF-8 for libav's SRT reader.
func normalizeSubtitleUTF8(data []byte) []byte {

	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})

	if utf8.Valid(data) {
		return data
	}

	runes := make([]rune, len(data))

	for i, b := range data {
		runes[i] = rune(b)
	}

	return []byte(string(runes))

}

type zipSubtitleCandidate struct {
	Name    string
	Payload []byte
}

func extractFromZip(data []byte, season, episode int) ([]byte, error) {

	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))

	if err != nil {
		return nil, err
	}

	var episodeMatches []zipSubtitleCandidate
	var fallback []zipSubtitleCandidate

	for _, file := range reader.File {

		ext := strings.ToLower(filepath.Ext(file.Name))

		if ext != ".srt" && ext != ".vtt" && ext != ".ass" && ext != ".ssa" {
			continue
		}

		if !looksEnglishName(file.Name) {
			continue
		}

		opened, err := file.Open()

		if err != nil {
			continue
		}

		payload, err := io.ReadAll(opened)
		opened.Close()

		if err != nil || len(payload) == 0 {
			continue
		}

		candidate := zipSubtitleCandidate{Name: file.Name, Payload: payload}

		if season > 0 && episode > 0 && nameMatchesEpisode(file.Name, season, episode) {
			episodeMatches = append(episodeMatches, candidate)
			continue
		}

		if episode > 0 && nameMatchesEpisode(file.Name, 0, episode) {
			episodeMatches = append(episodeMatches, candidate)
			continue
		}

		fallback = append(fallback, candidate)

	}

	if payload := pickZipSubtitleCandidate(episodeMatches); payload != nil {
		return payload, nil
	}

	if payload := pickZipSubtitleCandidate(fallback); payload != nil {
		return payload, nil
	}

	return nil, ErrNoSubtitle

}

func pickZipSubtitleCandidate(candidates []zipSubtitleCandidate) []byte {

	for _, candidate := range candidates {
		if looksEnglishSubtitle(candidate.Payload) {
			return candidate.Payload
		}
	}

	return nil

}

func looksLikeSubtitle(data []byte) bool {

	text := strings.ToLower(string(data[:min(len(data), 512)]))

	return strings.Contains(text, "-->") || strings.HasPrefix(strings.TrimSpace(text), "webvtt") || strings.HasPrefix(strings.TrimSpace(text), "[script info]")

}

func min(a, b int) int {

	if a < b {
		return a
	}

	return b

}
