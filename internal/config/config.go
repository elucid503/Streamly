package config

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

const browserUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/135.0.0.0 Safari/537.36"

// Config holds bot-wide settings sourced from the environment.
type Config struct {
	DiscordToken string   // Token for the real, command-handling bot.
	UserTokens   []string // Selfbot tokens; one streaming slot each.
	GuildID      string   // When set, slash commands register instantly to this guild.
	FebboxCookie  string // The `ui` cookie used to fetch Febbox media.
	MongoURI      string // MongoDB connection string for Streamly persistence.
	IntroDBAPIKey          string // Optional bearer token for TheIntroDB reads.
	SubDLAPIKey string // Subtitle provider; free tier allows 2,000 searches/day.
}

// StreamOptions holds libav transcode targets for every stream.
type StreamOptions struct {
	Width           int
	Height          int
	FrameRate       int
	BitrateVideo    int
	BitrateVideoMax int
	BitrateAudio    int
	VideoCodec      string
	Threads         int // Cap encoder threads to trim memory; 0 lets libav decide.
}

// DownloadOptions tunes the in-process media download that feeds the transcoder.
type DownloadOptions struct {
	RequestTimeoutMs int // Abort a fetch whose headers or next chunk do not arrive in time.
	MaxRetries       int // Consecutive failed re-resolves before the source gives up.
	LiveBufferSec    int // Live HLS cushion kept ahead of playback; 0 disables throttling.
}

var (
	App      Config
	Stream   StreamOptions
	Download DownloadOptions
)

func init() {

	loadDotEnv()

	App = Config{
		DiscordToken: required("DISCORD_TOKEN"),
		UserTokens:   parseTokens(os.Getenv("USER_TOKENS")),
		GuildID:      os.Getenv("GUILD_ID"),
		FebboxCookie:  required("FEBBOX_UI_COOKIE"),
		MongoURI:      required("MONGO_URI"),
		IntroDBAPIKey:         envString("INTRODB_API_KEY", ""),
		SubDLAPIKey: envString("SUBDL_API_KEY", ""),
	}

	Stream = StreamOptions{
		Width:           envInt("STREAM_WIDTH", 1280),
		Height:          envInt("STREAM_HEIGHT", 720),
		FrameRate:       envInt("STREAM_FPS", 30),
		BitrateVideo:    envInt("STREAM_BITRATE", 3000),
		BitrateVideoMax: envInt("STREAM_MAX_BITRATE", 5000),
		BitrateAudio:    envInt("STREAM_AUDIO_BITRATE", 128),
		VideoCodec:      normalizeVideoCodec(envString("STREAM_CODEC", "H264")),
		Threads:         envInt("STREAM_THREADS", 0),
	}

	Download = DownloadOptions{
		RequestTimeoutMs: envInt("STREAM_READ_TIMEOUT_MS", 30000),
		MaxRetries:       envInt("STREAM_MAX_RESUME_ATTEMPTS", 5),
		LiveBufferSec:    envInt("LIVE_HLS_BUFFER_SEC", 10),
	}

}

// FebboxStreamHeaders authenticates a raw Febbox media fetch as a logged-in browser tab.
func FebboxStreamHeaders() map[string]string {

	return map[string]string{
		"User-Agent":      browserUA,
		"Accept-Language": "en-US,en;q=0.9",
		"Cookie":          "ui=" + App.FebboxCookie,
	}

}

// TVBaseURL returns the live TV site origin used for API calls and Referer headers.
func TVBaseURL() string {

	base := strings.TrimSpace(os.Getenv("TV_BASE_URL"))

	if base == "" {
		return "https://dami-tv.pro"
	}

	return strings.TrimRight(base, "/")

}

// TVStreamHeaders presents a browser tab for proxied live TV HLS playlists.
func TVStreamHeaders() map[string]string {

	return map[string]string{
		"User-Agent":      browserUA,
		"Accept-Language": "en-US,en;q=0.9",
		"Referer":         TVBaseURL() + "/",
	}

}

// loadDotEnv reads a .env file into the process environment before config init.
func loadDotEnv() {

	candidates := []string{".env"}

	for _, path := range candidates {

		if _, err := os.Stat(path); err != nil {
			continue
		}

		if err := godotenv.Load(path); err != nil {
			log.Printf("[config] could not load %s: %v", path, err)
			continue
		}

		return

	}

}

func required(name string) string {

	value := strings.TrimSpace(os.Getenv(name))

	if value == "" {
		panic(fmt.Sprintf("missing required env var: %s", name))
	}

	return value

}

func parseTokens(raw string) []string {

	seen := make(map[string]struct{})
	var tokens []string

	for _, part := range strings.Split(raw, ",") {

		token := strings.TrimSpace(part)

		if token == "" {
			continue
		}

		if _, ok := seen[token]; ok {
			continue
		}

		seen[token] = struct{}{}
		tokens = append(tokens, token)

	}

	return tokens

}

func envString(name, fallback string) string {

	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}

	return fallback

}

func envInt(name string, fallback int) int {

	raw := strings.TrimSpace(os.Getenv(name))

	if raw == "" {
		return fallback
	}

	value, err := strconv.Atoi(raw)

	if err != nil {
		return fallback
	}

	return value

}

func normalizeVideoCodec(codec string) string {

	switch strings.ToUpper(codec) {
	case "H264", "H.264", "AVC":
		return "H264"
	case "H265", "H.265", "HEVC":
		return "H265"
	case "VP8", "VP9", "AV1":
		return strings.ToUpper(codec)
	default:
		return "H264"
	}

}
