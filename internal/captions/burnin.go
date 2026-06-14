package captions

import (
	"os"
)

func WriteBurnInSubtitle(destPath string, payload []byte) (string, error) {

	payload = normalizeSubtitleUTF8(payload)

	if err := os.WriteFile(destPath, payload, 0o600); err != nil {

		return "", err

	}

	return destPath, nil

}
