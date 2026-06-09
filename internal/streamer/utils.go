package streamer

import (
	"fmt"
	"strings"
)

func parseStreamKey(streamKey string) (guildID, channelID, userID string, call bool, err error) {

	parts := strings.Split(streamKey, ":")

	if len(parts) < 3 {
		return "", "", "", false, fmt.Errorf("invalid stream key: %s", streamKey)
	}

	kind := parts[0]

	switch kind {
	case "guild":
		if len(parts) < 4 {
			return "", "", "", false, fmt.Errorf("invalid guild stream key: %s", streamKey)
		}
		return parts[1], parts[2], parts[3], false, nil
	case "call":
		return "", parts[1], parts[2], true, nil
	default:
		return "", "", "", false, fmt.Errorf("invalid stream key type: %s", kind)
	}

}

func generateStreamKey(call bool, guildID, channelID, userID string) string {

	if call {
		return fmt.Sprintf("call:%s:%s", channelID, userID)
	}

	return fmt.Sprintf("guild:%s:%s:%s", guildID, channelID, userID)

}

