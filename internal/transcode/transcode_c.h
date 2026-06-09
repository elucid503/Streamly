#ifndef STREAMLY_TRANSCODE_C_H
#define STREAMLY_TRANSCODE_C_H

#include <stdbool.h>
#include <stdint.h>

#define STREAMLY_KIND_VIDEO 0
#define STREAMLY_KIND_AUDIO 1

// streamly_emit_cb hands one encoded elementary packet (Annex-B H264 or raw Opus) to Go.
typedef void (*streamly_emit_cb)(uintptr_t user, int kind, const uint8_t *data, int len, int64_t pts_ms, int64_t dur_ms);

// transcode_params_t mirrors config.Stream and config.Overlay for the libav pipeline.
typedef struct {
    int width;                  // Output frame width.
    int height;                 // Output frame height.
    int frame_rate;             // Capped output fps.
    int bitrate_video_k;        // Target video bitrate in kbps.
    int bitrate_video_max_k;    // Video maxrate in kbps.
    int bitrate_audio_k;        // Opus bitrate in kbps.
    int threads;                // Encoder thread cap; 0 lets libav decide.

    bool overlay;               // Burn logo + caption when assets exist.
    const char *logo_path;      // PNG watermark path.
    const char *font_path;      // TTF for drawtext.
    const char *caption_file;   // Temp text file for drawtext textfile=; may be NULL.
    int logo_width;             // Logo width in pixels.
    int font_size;              // Caption font size.
    float opacity;              // Shared alpha for logo and caption.
    int margin;                 // Bottom-right inset in pixels.

    int video_fd;               // Read end of the video input pipe.
    const char *input_url;       // Direct URL input; lets libavformat demux HLS in-process.
    const char *headers;         // HTTP headers for URL input, formatted as CRLF-separated lines.

    streamly_emit_cb emit;      // Receives each encoded H264/Opus packet.
    uintptr_t emit_user;        // Opaque Go handle token passed back to emit.

    volatile bool *abort_flag;  // Shared with Go; set on context cancel.
} transcode_params_t;

typedef struct transcode_handle transcode_handle_t;

// transcode_start launches the libav worker thread; returns NULL on allocation failure.
transcode_handle_t *transcode_start(const transcode_params_t *params);

// transcode_join blocks until the worker exits; returns 0 on success.
int transcode_join(transcode_handle_t *handle);

// transcode_error copies the worker's last error message into buf (may be empty on success).
void transcode_error(transcode_handle_t *handle, char *buf, int buf_size);

// transcode_free releases a handle after join (safe on NULL).
void transcode_free(transcode_handle_t *handle);

#endif
