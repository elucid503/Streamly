package config

import (
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

const browserUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 " + "(KHTML, like Gecko) Chrome/135.0.0.0 Safari/537.36"

type Config struct {

	DiscordToken string
	AdminUserIDs []string
	GuildID string

	FebboxCookie string
	IntroDBAPIKey string
	SubDLAPIKey string

	MongoURI string

}

type StreamOptions struct {

	Width int
	Height int
	FrameRate int

	BitrateVideo int
	BitrateVideoMax int
	BitrateAudio int

	VideoCodec string
	Threads int // Cap encoder threads to trim memory; 0 lets libav decide.

}

type DownloadOptions struct {

	RequestTimeoutMs int // Abort a fetch whose headers or next chunk do not arrive in time.
	MaxRetries int

}

var (

	App Config

	Stream StreamOptions
	Download DownloadOptions

)

func init() {

	loadDotEnv()

	App = Config{

		DiscordToken: required("DISCORD_TOKEN"),
		AdminUserIDs: parseIDs(os.Getenv("ADMIN_USER_IDS")),
		GuildID: os.Getenv("GUILD_ID"),

		FebboxCookie: required("FEBBOX_UI_COOKIE"),
		IntroDBAPIKey: envString("INTRODB_API_KEY", ""),
		SubDLAPIKey: envString("SUBDL_API_KEY", ""),

		MongoURI: required("MONGO_URI"),
	}

	Stream = StreamOptions{

		Width: envInt("STREAM_WIDTH", 1280),
		Height: envInt("STREAM_HEIGHT", 720),
		FrameRate: envInt("STREAM_FPS", 30),

		BitrateVideo: envInt("STREAM_BITRATE", 3000),
		BitrateVideoMax: envInt("STREAM_MAX_BITRATE", 5000),
		BitrateAudio: envInt("STREAM_AUDIO_BITRATE", 128),

		VideoCodec: normalizeVideoCodec(envString("STREAM_CODEC", "H264")),
		Threads: envInt("STREAM_THREADS", 0),

	}

	Download = DownloadOptions{

		RequestTimeoutMs: envInt("STREAM_READ_TIMEOUT_MS", 30000),
		MaxRetries: envInt("STREAM_MAX_RESUME_ATTEMPTS", 5),

	}

}

func FebboxStreamHeaders() map[string]string {

	return map[string]string{

		"User-Agent": browserUA,
		"Accept-Language": "en-US,en;q=0.9",
		"Cookie": "ui=" + App.FebboxCookie,

	}

}

func TVBaseURL() string {

	base := strings.TrimSpace(os.Getenv("TV_BASE_URL"))

	if base == "" {

		return "https://dami-tv.pro"
	}

	return strings.TrimRight(base, "/")

}

func TVStreamAPI() string {

	return strings.TrimSpace(os.Getenv("TV_STREAM_API"))

}

func TVStreamReferer() string {

	if api := TVStreamAPI(); api != "" {

		parsed, err := url.Parse(api)

		if err == nil && parsed.Scheme != "" && parsed.Host != "" {

			return parsed.Scheme + "://" + parsed.Host + "/"

		}

	}

	return TVBaseURL() + "/"

}

func TVStreamHeaders() map[string]string {

	return TVStreamHeadersForReferer(TVStreamReferer())

}

func TVStreamHeadersForReferer(referer string) map[string]string {

	referer = strings.TrimSpace(referer)

	if referer == "" {

		referer = TVStreamReferer()
	}

	return map[string]string{

		"User-Agent": browserUA,
		"Accept-Language": "en-US,en;q=0.9",
		"Referer": referer,

	}

}

func loadDotEnv() {

	dir, err := os.Getwd()

	if err != nil {

		return
	}

	for {

		path := filepath.Join(dir, ".env")

		if _, err := os.Stat(path); err == nil {

			if err := godotenv.Load(path); err != nil {

				log.Printf("[config] could not load %s: %v", path, err)
			}

			return

		}

		parent := filepath.Dir(dir)

		if parent == dir {

			return
		}

		dir = parent

	}

}

func required(name string) string {

	value := strings.TrimSpace(os.Getenv(name))

	if value == "" {

		panic(fmt.Sprintf("missing required env var: %s", name))
	}

	return value

}

func parseIDs(raw string) []string {

	seen := make(map[string]struct{})
	var ids []string

	for _, part := range strings.Split(raw, ",") {

		id := strings.TrimSpace(part)

		if id == "" {

			continue

		}

		if _, ok := seen[id]; ok {

			continue

		}

		seen[id] = struct{}{}
		ids = append(ids, id)

	}

	return ids

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
