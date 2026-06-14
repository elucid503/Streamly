package captions

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"streamly/internal/febapi"
)

var subtitleExtensions = map[string]struct{}{

	".srt": {},
	".vtt": {},
	".ass": {},
	".ssa": {},

}

type FebboxScanner struct {

	client *febapi.FebboxClient

}

func NewFebboxScanner(client *febapi.FebboxClient) *FebboxScanner {

	return &FebboxScanner{client: client}

}

type SidecarMatch struct {

	FID int
	FileName string

}

func (s *FebboxScanner) Find(ctx context.Context, shareKey string, videoFID int, videoName string) (*SidecarMatch, error) {

	parent, err := parentFolderForFile(s.client, shareKey, 0, videoFID)

	if err != nil {

		return nil, err

	}

	entries, err := s.client.ListFiles(shareKey, parent, "")

	if err != nil {

		return nil, err

	}

	base := strings.TrimSuffix(videoName, filepath.Ext(videoName))
	baseKey := normalizeName(base)

	for _, entry := range entries {

		if entry.IsDir != 0 || entry.FID == videoFID {

			continue

		}

		ext := strings.ToLower(filepath.Ext(entry.FileName))

		if _, ok := subtitleExtensions[ext]; !ok {

			continue

		}

		if hasForeignLanguageName(entry.FileName) {

			continue

		}

		match := &SidecarMatch{FID: entry.FID, FileName: entry.FileName}

		if normalizeName(strings.TrimSuffix(entry.FileName, ext)) == baseKey {

			return match, nil

		}

	}

	return nil, ErrNoSubtitle

}

func (s *FebboxScanner) Download(ctx context.Context, shareKey string, match *SidecarMatch, destPath string, headers map[string]string) (string, error) {

	qualities, err := s.client.GetLinks(shareKey, match.FID, "")

	if err != nil || len(qualities) == 0 || qualities[0].URL == "" {

		return "", ErrNoSubtitle

	}

	data, err := downloadURL(ctx, qualities[0].URL, headers)

	if err != nil {

		return "", err

	}

	payload, err := extractSubtitle(data, 0, 0)

	if err != nil {

		return "", err

	}

	if !looksEnglishSubtitle(payload) {

		return "", ErrNoSubtitle

	}

	if _, err := WriteBurnInSubtitle(destPath, payload); err != nil {

		return "", err

	}

	return fmt.Sprintf("Febbox sidecar (%s)", match.FileName), nil

}

func downloadURL(ctx context.Context, url string, headers map[string]string) ([]byte, error) {

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)

	if err != nil {

		return nil, err

	}

	for key, value := range headers {

		request.Header.Set(key, value)

	}

	response, err := http.DefaultClient.Do(request)

	if err != nil {

		return nil, err

	}

	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {

		return nil, fmt.Errorf("captions: download failed with status %d", response.StatusCode)

	}

	return io.ReadAll(response.Body)

}

func parentFolderForFile(client *febapi.FebboxClient, shareKey string, parentID int, wantedFID int) (int, error) {

	entries, err := client.ListFiles(shareKey, parentID, "")

	if err != nil {

		return 0, err

	}

	for _, entry := range entries {

		if entry.FID == wantedFID {

			return parentID, nil

		}

		if entry.IsDir == 0 {

			continue

		}

		found, err := parentFolderForFile(client, shareKey, entry.FID, wantedFID)

		if err == nil {

			return found, nil

		}

	}

	return 0, ErrNoSubtitle

}

func normalizeName(name string) string {

	name = strings.ToLower(name)
	replacer := strings.NewReplacer(".", " ", "_", " ", "-", " ")

	return strings.Join(strings.Fields(replacer.Replace(name)), " ")

}
