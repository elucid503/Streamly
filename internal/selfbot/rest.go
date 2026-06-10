package selfbot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// validateToken performs a lightweight GET /users/@me with desktop client headers before opening the gateway.
func validateToken(ctx context.Context, token string, props Properties) error {

	props.authToken = token
	props.BrowserUserAgent = userAgent

	transport := &http.Transport{TLSClientConfig: chromeTLSConfig()}
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase+"/users/@me", nil)

	if err != nil {
		return err
	}

	request.Header = restHeaders(props)

	response, err := client.Do(request)

	if err != nil {
		return fmt.Errorf("token validation request: %w", err)
	}

	defer response.Body.Close()

	if response.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("TOKEN_INVALID")
	}

	if response.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 512))
		return fmt.Errorf("token validation failed: HTTP %d: %s", response.StatusCode, string(body))
	}

	var me struct {
		ID string `json:"id"`
	}

	if err := json.NewDecoder(response.Body).Decode(&me); err != nil {
		return fmt.Errorf("token validation decode: %w", err)
	}

	if me.ID == "" {
		return fmt.Errorf("token validation returned no user id")
	}

	return nil

}
