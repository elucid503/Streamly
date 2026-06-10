package captions

import (
	"os"
)

// WriteBurnInSubtitle normalizes subtitle text to UTF-8 and writes the burn-in file.
func WriteBurnInSubtitle(destPath string, payload []byte) (string, error) {

	payload = normalizeSubtitleUTF8(payload)

	if err := os.WriteFile(destPath, payload, 0o600); err != nil {
		return "", err
	}

	return destPath, nil

}
