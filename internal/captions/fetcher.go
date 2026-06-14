package captions

import (
	"context"
	"os"

	"streamly/internal/febapi"
)

type Fetcher struct {

	febbox *FebboxScanner
	providers []RemoteProvider

	headers map[string]string

}

func NewFetcher(febbox *febapi.FebboxClient, providers []RemoteProvider, headers map[string]string) *Fetcher {

	filtered := make([]RemoteProvider, 0, len(providers))

	for _, provider := range providers {

		if provider != nil {

			filtered = append(filtered, provider)

		}

	}

	return &Fetcher{

		febbox: NewFebboxScanner(febbox),

		providers: filtered,
		headers: headers,

	}

}

func (f *Fetcher) Fetch(ctx context.Context, query Query, destPath string) (string, error) {

	if _, err := FontPath(); err != nil {

		return "", err

	}

	if query.ShareKey != "" && query.VideoFID > 0 {

		if label, err := f.tryFebbox(ctx, query, destPath); err == nil {

			return label, nil

		}

	}

	for _, provider := range f.providers {

		if !provider.Configured() {

			continue

		}

		label, err := provider.Download(ctx, query, destPath)

		if err == nil {

			return label, nil

		}

		if err != ErrNoSubtitle && err != ErrUnconfigured {

			return "", err

		}

	}

	if len(f.providers) == 0 || !anyProviderConfigured(f.providers) {

		return "", ErrUnconfigured

	}

	return "", ErrNoSubtitle

}

func anyProviderConfigured(providers []RemoteProvider) bool {

	for _, provider := range providers {

		if provider.Configured() {

			return true

		}

	}

	return false

}

func (f *Fetcher) tryFebbox(ctx context.Context, query Query, destPath string) (string, error) {

	match, err := f.febbox.Find(ctx, query.ShareKey, query.VideoFID, query.VideoName)

	if err != nil {

		return "", err

	}

	label, err := f.febbox.Download(ctx, query.ShareKey, match, destPath, f.headers)

	if err != nil {

		return "", err

	}

	data, readErr := os.ReadFile(destPath)

	if readErr != nil || !looksEnglishSubtitle(data) {

		_ = os.Remove(destPath)
		return "", ErrNoSubtitle

	}

	return label, nil

}
