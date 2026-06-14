package media

import "strings"

func StreamFilePreference(name string) int {

	lower := strings.ToLower(name)
	score := 0

	if strings.Contains(lower, "x265") || strings.Contains(lower, "hevc") || strings.Contains(lower, "h265") {

		score -= 30

	}

	if strings.Contains(lower, "x264") || strings.Contains(lower, "h264") || strings.Contains(lower, "avc") {

		score += 20

	}

	if strings.Contains(lower, "bluray") || strings.Contains(lower, "blu-ray") {

		score += 10

	}

	if strings.Contains(lower, "1080") {

		score += 5

	}

	if strings.Contains(lower, "720") {

		score += 2

	}

	if strings.Contains(lower, "rarbg") || strings.Contains(lower, "web-dl") {

		score -= 5

	}

	return score

}
