package introdb

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetMediaSuccess(t *testing.T) {

	end := int64(90_000)

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {

		if request.URL.Path != "/media" {
			t.Fatalf("path: %s", request.URL.Path)
		}

		if request.URL.Query().Get("tmdb_id") != "1396" {
			t.Fatalf("tmdb_id: %s", request.URL.Query().Get("tmdb_id"))
		}

		if request.URL.Query().Get("season") != "1" || request.URL.Query().Get("episode") != "1" {
			t.Fatalf("season/episode: %s %s", request.URL.Query().Get("season"), request.URL.Query().Get("episode"))
		}

		if request.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("authorization: %q", request.Header.Get("Authorization"))
		}

		_ = json.NewEncoder(writer).Encode(map[string]any{
			"tmdb_id": 1396,
			"type":    "tv",
			"intro": []map[string]any{
				{"start_ms": nil, "end_ms": end},
			},
		})

	}))

	defer server.Close()

	client := testClient(server.URL, "test-key")

	record, err := client.GetMedia(MediaQuery{TMDBId: 1396, Season: 1, Episode: 1})
	if err != nil {
		t.Fatalf("GetMedia: %v", err)
	}

	if len(record.Intro) != 1 || record.Intro[0].StartMs != 0 || record.Intro[0].EndMs == nil || *record.Intro[0].EndMs != end {
		t.Fatalf("intro: %+v", record.Intro)
	}

}

func TestGetMediaAPIErrors(t *testing.T) {

	tests := []struct {
		name    string
		status  int
		body    string
		wantErr error
	}{
		{
			name:    "unauthorized json",
			status:  http.StatusUnauthorized,
			body:    `{"error":"Unauthorized"}`,
			wantErr: ErrUnauthorized,
		},
		{
			name:    "account locked",
			status:  http.StatusForbidden,
			body:    `{"error":"Your account has been locked"}`,
			wantErr: ErrAccountLocked,
		},
		{
			name:    "not found",
			status:  http.StatusNotFound,
			body:    `{"error":"media not found"}`,
			wantErr: ErrNotFound,
		},
		{
			name:    "rate limited",
			status:  http.StatusTooManyRequests,
			body:    `{"error":"Too Many Requests"}`,
			wantErr: ErrRateLimited,
		},
		{
			name:    "cloudflare challenge",
			status:  http.StatusForbidden,
			body:    `<html><title>Just a moment...</title></html>`,
			wantErr: ErrBlocked,
		},
	}

	for _, test := range tests {

		t.Run(test.name, func(t *testing.T) {

			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.WriteHeader(test.status)
				_, _ = writer.Write([]byte(test.body))
			}))

			defer server.Close()

			client := testClient(server.URL, "")
			_, err := client.GetMedia(MediaQuery{TMDBId: 1})

			if !errors.Is(err, test.wantErr) {
				t.Fatalf("expected %v, got %v", test.wantErr, err)
			}

		})

	}

}

func TestMapGetMediaError(t *testing.T) {

	if !errors.Is(MapGetMediaError(ErrNotFound), ErrNoIntroData) {
		t.Fatal("404 should map to no intro data")
	}

}

func testClient(baseURL, apiKey string) *Client {

	client := NewClient(ClientOptions{BaseURL: baseURL, APIKey: apiKey})
	client.http = reqPlainClient()
	return client

}