package introdb

import (
	"errors"
	"fmt"
	"strings"
)

var (

	ErrUnauthorized = errors.New("introdb: unauthorized")
	ErrRateLimited = errors.New("introdb: rate limited")
	ErrBlocked = errors.New("introdb: blocked by edge protection")
	ErrNotFound = errors.New("introdb: media not found")
	ErrAccountLocked = errors.New("introdb: account locked")
	ErrNoIntroData = errors.New("no intro timing recorded")
	ErrPastIntro = errors.New("already past the intro")
	ErrNotInIntro = errors.New("not in the intro")

)

type APIError struct {

	Status int
	Message string

	Details string
	Code string

	Body string

}

func (e *APIError) Error() string {

	if e.Message != "" {

		return fmt.Sprintf("introdb: %s (status %d)", e.Message, e.Status)
	}

	return fmt.Sprintf("introdb: request failed with status %d", e.Status)

}

func apiErrorFromStatus(status int, body string) error {

	if isCloudflareChallenge(body) {

		return fmt.Errorf("%w: %s", ErrBlocked, fmt.Sprintf("request failed with status %d", status))
	}

	parsed := parseAPIErrorBody(body)

	err := &APIError{

		Status: status,
		Message: parsed.Error,

		Details: parsed.Details,
		Code: parsed.Code,

		Body: body,
	}

	switch status {

		case 401:

			return fmt.Errorf("%w: %s", ErrUnauthorized, err.Error())

		case 403:

			return fmt.Errorf("%w: %s", ErrAccountLocked, err.Error())

		case 404:

			return fmt.Errorf("%w: %s", ErrNotFound, err.Error())

		case 429:

			return fmt.Errorf("%w: %s", ErrRateLimited, err.Error())

		default:

			return err

	}

}

type apiErrorBody struct {

	Error string `json:"error"`

	Details string `json:"details"`
	Code string `json:"code"`
}

func isCloudflareChallenge(body string) bool {

	return strings.Contains(body, "Just a moment") || strings.Contains(body, "<!DOCTYPE html>")

}
