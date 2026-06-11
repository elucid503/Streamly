#ifndef STREAMLY_TRANSCODE_C_H
#define STREAMLY_TRANSCODE_C_H

#include <stdbool.h>
#include <stdint.h>

#define STREAMLY_KIND_VIDEO 0
#define STREAMLY_KIND_AUDIO 1
#define STREAMLY_MAX_CTA 4
#define STREAMLY_PAUSE_BODY_LINES 4

// Whence values for streamly_seek_cb; 0..2 match SEEK_SET/SEEK_CUR/SEEK_END.
#define STREAMLY_SEEK_SIZE 3

// streamly_emit_cb hands one encoded elementary packet (Annex-B H264 or raw Opus) to Go.
typedef void (*streamly_emit_cb)(uintptr_t user, int kind, const uint8_t *data, int len, int64_t pts_ms, int64_t dur_ms);

// streamly_read_cb fills buf from the Go media source; returns bytes read, 0 on EOF, negative on error.
typedef int (*streamly_read_cb)(uintptr_t user, uint8_t *buf, int len);

// streamly_seek_cb repositions the Go media source by bytes (HTTP Range under the hood).
// Returns the new position, the total size for STREAMLY_SEEK_SIZE, or negative when unsupported.
typedef int64_t (*streamly_seek_cb)(uintptr_t user, int64_t offset, int whence);

// streamly_meta_cb reports the container duration in ms (or -1 when unknown) once probing finishes.
typedef void (*streamly_meta_cb)(uintptr_t user, int64_t duration_ms);

typedef struct {
    char text[192];
    int64_t start_ms;
    int64_t end_ms;
} streamly_cta_t;

// transcode_params_t mirrors config.Stream for the libav pipeline.
typedef struct {
    int width;                  // Output frame width.
    int height;                 // Output frame height.
    int frame_rate;             // Capped output fps.
    int bitrate_video_k;        // Target video bitrate in kbps.
    int bitrate_video_max_k;    // Video maxrate in kbps.
    int bitrate_audio_k;        // Opus bitrate in kbps.
    int threads;                // Encoder thread cap; 0 lets libav decide.

    const char *subtitle_path;  // External SRT/VTT/ASS for the subtitles filter; NULL disables burn-in.
    const char *fonts_dir;      // Directory passed to libass fontsdir=; NULL uses libass defaults.
    const char *cta_font_path;  // Font file for drawtext overlays; NULL disables CTAs.

    int cta_count;
    streamly_cta_t ctas[STREAMLY_MAX_CTA];

    streamly_read_cb read_cb;   // Byte-seekable Go media source; NULL when input_url is set.
    streamly_seek_cb seek_cb;   // Byte seek into the Go media source; enables av_seek_frame.
    const char *input_url;       // Direct URL input; lets libavformat demux HLS in-process.
    const char *headers;         // HTTP headers for URL input, formatted as CRLF-separated lines.

    int64_t start_ms;           // Initial playback position; 0 plays from the beginning.
    bool live;                  // Live HLS input; shorter network timeouts and fresh HTTP connections.

    streamly_emit_cb emit;      // Receives each encoded H264/Opus packet.
    streamly_meta_cb meta_cb;   // Receives the container duration after probing; may be NULL.
    uintptr_t emit_user;        // Opaque Go handle token passed back to all callbacks.

    volatile bool *abort_flag;  // Shared with Go; set on context cancel.
} transcode_params_t;

// streamly_pause_card_t is the text content of the on-stream pause screen overlay.
// All strings are borrowed for the duration of the call; empty/NULL fields are skipped.
typedef struct {
    const char *font_path;  // drawtext font; without it the card is frame + dim only.
    const char *title;
    const char *subtitle;   // "Season X - Episode Y" line for TV; NULL for movies.
    const char *body[STREAMLY_PAUSE_BODY_LINES]; // Pre-wrapped description lines.
    int body_count;
    const char *cta;        // Bottom call-to-action line.
    int64_t target_pts_ms;  // Output PTS of the last frame the viewer saw; -1 takes the newest.
} streamly_pause_card_t;

typedef struct transcode_handle transcode_handle_t;

// transcode_start launches the libav worker thread; returns NULL on allocation failure.
transcode_handle_t *transcode_start(const transcode_params_t *params);

// transcode_pause_frame composes the pause screen over the frozen frame nearest
// card->target_pts_ms and encodes it as one self-contained Annex-B IDR frame.
// Returns 0 and a malloc'd buffer in *out_data (free with transcode_buffer_free),
// or a negative AVERROR. Safe to call from any thread while the worker runs.
int transcode_pause_frame(transcode_handle_t *handle, const streamly_pause_card_t *card,
                          uint8_t **out_data, int *out_len);

// transcode_buffer_free releases a buffer returned by transcode_pause_frame.
void transcode_buffer_free(uint8_t *data);

// transcode_join blocks until the worker exits; returns 0 on success.
int transcode_join(transcode_handle_t *handle);

// transcode_error copies the worker's last error message into buf (may be empty on success).
void transcode_error(transcode_handle_t *handle, char *buf, int buf_size);

// transcode_free releases a handle after join (safe on NULL).
void transcode_free(transcode_handle_t *handle);

#endif
