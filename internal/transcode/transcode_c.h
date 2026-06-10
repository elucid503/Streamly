#ifndef STREAMLY_TRANSCODE_C_H
#define STREAMLY_TRANSCODE_C_H

#include <stdbool.h>
#include <stdint.h>

#define STREAMLY_KIND_VIDEO 0
#define STREAMLY_KIND_AUDIO 1

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

    streamly_read_cb read_cb;   // Byte-seekable Go media source; NULL when input_url is set.
    streamly_seek_cb seek_cb;   // Byte seek into the Go media source; enables av_seek_frame.
    const char *input_url;       // Direct URL input; lets libavformat demux HLS in-process.
    const char *headers;         // HTTP headers for URL input, formatted as CRLF-separated lines.

    int64_t start_ms;           // Initial playback position; 0 plays from the beginning.

    streamly_emit_cb emit;      // Receives each encoded H264/Opus packet.
    streamly_meta_cb meta_cb;   // Receives the container duration after probing; may be NULL.
    uintptr_t emit_user;        // Opaque Go handle token passed back to all callbacks.

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
