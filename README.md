# Streamly

Educational Discord bot that streams movies and TV shows from FebAPI into a voice call.

## Architecture

- **One command bot** (`discordgo`) registers slash commands (`/stream`, `/pause`, `/resume`, `/stop`, `/stats`).
- **A pool of minimal selfbot accounts** handles Go Live streaming. Each `USER_TOKENS` entry is one concurrent stream slot.
- **No `ffmpeg` sidecar process** is spawned. HLS is opened directly by `libavformat` through CGO, then decoded, filtered, encoded, encrypted, and sent over WebRTC in-process.
- Progressive files use a small Go `Range`-resumable downloader and then enter the same libav transcode path.

| Package | Responsibility |
| --- | --- |
| `internal/config` | Env parsing, transcode targets, and download knobs. |
| `internal/febapi` | Showbox search and Febbox browsing. |
| `internal/media` | Title resolution and quality picking. |
| `internal/source` | Progressive media downloader. HLS is handled by libavformat. |
| `internal/captions` | Subtitle lookup (Febbox sidecars, SubDL) and burn-in state. |
| `internal/transcode` | CGO libav: HLS/progressive demux, decode, scale, subtitles, H264+Opus encode. |
| `internal/selfbot` | Minimal gateway client with token safety checks. |
| `internal/streamer` | Voice gateway, WebRTC, Go Live playback, and packet pacing. |
| `internal/pool` | Selfbot pool and join -> transcode -> play lifecycle. |
| `internal/bot` | Slash commands, autocomplete, pickers, and controls. |

## Selfbot Scope

The selfbot module is intentionally small. It only:

- Validates and sanitizes user tokens.
- Sends desktop-like `x-super-properties` on IDENTIFY.
- Forwards voice and stream gateway events needed for Go Live.
- Sends voice and stream gateway opcodes.

It does not implement messaging, guild caching, captcha solving, or general REST.

## Ubuntu Dependencies

Install once:

```bash
sudo apt-get update
sudo apt-get install -y \
  golang-go \
  build-essential \
  cmake \
  pkg-config \
  curl \
  unzip \
  libssl-dev \
  libavformat-dev \
  libavcodec-dev \
  libavfilter-dev \
  libavutil-dev \
  libswresample-dev \
  libx264-dev \
  libopus-dev \
  libfreetype6-dev \
  libfontconfig1-dev \
  libass-dev
```

Discord voice requires the DAVE E2EE protocol. Install libdave once:

```bash
bash "$(go env GOPATH)/pkg/mod/github.com/disgoorg/godave@v0.1.0/scripts/libdave_install.sh" v1.1.0
export LD_LIBRARY_PATH="$HOME/.local/lib:${LD_LIBRARY_PATH}"
```

Use Go 1.24+.

If `third_party/libdatachannel/build` is missing, build the static WebRTC dependency:

```bash
scripts/build-libdatachannel.sh
```

## Configuration

Create `.env` with at least:

```bash
DISCORD_TOKEN=...
USER_TOKENS=token1,token2
FEBBOX_UI_COOKIE=...
```

Optional:

```bash
GUILD_ID=...
STREAM_WIDTH=1280
STREAM_HEIGHT=720
STREAM_FPS=30
STREAM_BITRATE=3000
STREAM_MAX_BITRATE=5000
STREAM_AUDIO_BITRATE=128
STREAM_THREADS=0
SUBDL_API_KEY=...
```

Drop `assets/font.ttf` beside the binary for subtitles. `/subtitles` turns them on or off for the active stream. Subtitles are fetched from Febbox sidecar files when present, otherwise from SubDL.

## Run

```bash
CGO_ENABLED=1 go run ./cmd/streamly
```

## Pause / Resume

Pause is cooperative and in-process. It blocks libav packet emission, backpressures the transcode pipeline, and shifts playback pacing on resume so audio/video do not jump ahead or drift because of the pause interval.
