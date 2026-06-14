package selfbot

import (
	"fmt"
	"strings"
)

func sanitizeToken(token string) (string, error) {

	token = strings.TrimSpace(token)

	if token == "" {

		return "", fmt.Errorf("TOKEN_INVALID")
	}

	token = strings.TrimPrefix(token, "Bot ")
	token = strings.TrimPrefix(token, "bot ")
	token = strings.TrimPrefix(token, "Bearer ")
	token = strings.TrimPrefix(token, "bearer ")

	if token == "" {

		return "", fmt.Errorf("TOKEN_INVALID")
	}

	return token, nil

}
