package introdb

import (
	"os"
	"testing"

	"github.com/joho/godotenv"
)

func TestGetMediaIntegration(t *testing.T) {

	_ = godotenv.Load("../../.env")
	_ = godotenv.Load(".env")

	apiKey := os.Getenv("INTRODB_API_KEY")
	if apiKey == "" {
		t.Skip("set INTRODB_API_KEY to run TheIntroDB integration tests")
	}

	client := NewClient(ClientOptions{APIKey: apiKey})

	record, err := client.GetMedia(MediaQuery{
		TMDBId:  1396,
		Season:  1,
		Episode: 1,
	})

	if err != nil {
		t.Fatalf("GetMedia breaking bad s01e01: %v", err)
	}

	if record == nil {
		t.Fatal("expected media record")
	}

	if len(record.Intro) == 0 {
		t.Fatal("expected intro data for breaking bad s01e01")
	}

	for _, segment := range record.Intro {
		if segment.EndMs == nil {
			t.Fatalf("intro segment missing end: %+v", segment)
		}
	}

	movie, err := client.GetMedia(MediaQuery{TMDBId: 27205})
	if err != nil {
		t.Fatalf("GetMedia inception: %v", err)
	}

	if movie == nil {
		t.Fatal("expected movie record")
	}

}

func TestValidateAPIKeyIntegration(t *testing.T) {

	_ = godotenv.Load("../../.env")
	_ = godotenv.Load(".env")

	apiKey := os.Getenv("INTRODB_API_KEY")
	if apiKey == "" {
		t.Skip("set INTRODB_API_KEY to run TheIntroDB integration tests")
	}

	client := NewClient(ClientOptions{APIKey: apiKey})

	response, err := client.http.R().
		SetHeader("Accept", "application/json").
		SetBearerAuthToken(apiKey).
		Get(client.baseURL + "/user/stats")

	if err != nil {
		t.Fatalf("user stats: %v", err)
	}

	if response.StatusCode != 200 {
		t.Fatalf("user stats status %d body %s", response.StatusCode, response.String())
	}

}