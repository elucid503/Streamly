#include "transcode_c.h"

extern void streamly_fill_ctas(uintptr_t user, transcode_params_t *params);

#include <errno.h>
#include <pthread.h>
#include <stdarg.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include <libavcodec/avcodec.h>
#include <libavfilter/avfilter.h>
#include <libavfilter/buffersink.h>
#include <libavfilter/buffersrc.h>
#include <libavformat/avformat.h>
#include <libavutil/audio_fifo.h>
#include <libavutil/imgutils.h>
#include <libavutil/opt.h>
#include <libavutil/time.h>
#include <libswresample/swresample.h>

#define INPUT_AVIO_SIZE (64 * 1024)
#define PACKET_QUEUE_CAP 48
#define PACKET_QUEUE_CAP_LIVE 96
#define AUDIO_DRAIN_BATCH 3
#define ERR_BUF 2048
#define INPUT_OPEN_MAX_RETRIES 5
#define INPUT_OPEN_RETRY_BASE_US 300000

// Burned-in subtitle styling (libass force_style).
#define SUBTITLE_FORCE_STYLE \
    "FontSize=11,Spacing=0.2,Alignment=2,MarginV=14,BorderStyle=1,Outline=0," \
    "Shadow=1,BackColour=&H90000000,PrimaryColour=&H00FFFFFF"

// Force 8-bit yuv420p before x264. Avoid colorspace filter: it aborts on unknown primaries.
#define VIDEO_SCALE_CHAIN "scale=%d:%d:flags=lanczos,format=yuv420p,fps=%d"
#define VIDEO_SCALE_CHAIN_LIVE "scale=%d:%d:flags=fast_bilinear,format=yuv420p,fps=%d"

static bool transient_upstream_tls_log(const char *msg) {

    return strstr(msg, "Error in the pull function") != NULL ||
           strstr(msg, "IO error: Connection timed out") != NULL ||
           strstr(msg, "Error decoding the received TLS packet") != NULL;

}

static void streamly_av_log(void *ptr, int level, const char *fmt, va_list vl) {

    if (level > av_log_get_level()) {
        return;
    }

    char msg[1024];
    int print_prefix = 1;

    av_log_format_line(ptr, level, fmt, vl, msg, sizeof(msg), &print_prefix);

    if (transient_upstream_tls_log(msg)) {
        fprintf(stdout, "[transcode] upstream network hiccup: %s", msg);
    } else {
        fprintf(stdout, "[transcode] libav: %s", msg);
    }

    size_t len = strlen(msg);

    if (len == 0 || msg[len - 1] != '\n') {
        fputc('\n', stdout);
    }

    fflush(stdout);

}

// Route libav logs to stdout so transient upstream TLS noise stays out of pm2's error log.
// The per-session av_log_set_level in transcode_run is a belt-and-suspenders fallback.
__attribute__((constructor)) static void init_av_log(void) {
    av_log_set_level(AV_LOG_ERROR);
    av_log_set_callback(streamly_av_log);
}

typedef struct PacketNode {
    AVPacket *pkt;
    struct PacketNode *next;
} PacketNode;

typedef struct PacketQueue {
    pthread_mutex_t lock;
    pthread_cond_t cond;
    PacketNode *head;
    PacketNode *tail;
    int count;
    int capacity;
    bool done;
    int exit_code;
    bool initialized;
} PacketQueue;

typedef struct CbIO {
    streamly_read_cb read_cb;
    streamly_seek_cb seek_cb;
    uintptr_t user;
    volatile bool *abort_flag;
} CbIO;

typedef struct InterruptState {
    volatile bool *abort_flag;
} InterruptState;

typedef struct StreamPipeline {
    AVFormatContext *fmt;
    AVIOContext *avio; // Custom IO context when reading via Go callbacks; NULL for URL input.
    AVCodecContext *dec;
    int stream_index;
    AVRational tb; // Demuxer stream time base, for the post-seek discard window.
    int64_t skip_until_us; // Decoded frames before this input timestamp are dropped; -1 disables.
    PacketQueue queue;

    AVFrame *frame;
    AVPacket *pkt;
} StreamPipeline;

typedef struct ReaderState {
    AVFormatContext *fmt;
    int video_stream;
    int audio_stream;
    PacketQueue *video_queue;
    PacketQueue *audio_queue;
    volatile bool *abort_flag;
} ReaderState;

typedef struct OutputPipeline {
    int kind; // STREAMLY_KIND_VIDEO or STREAMLY_KIND_AUDIO.
    AVCodecContext *enc;
    AVFrame *frame;
    AVFrame *swr_frame; // Resampler scratch; NULL for video.
    AVPacket *pkt;
    int64_t next_pts;
    int frame_rate; // Video fps for the duration fallback; 0 for audio.
    SwrContext *swr; // Audio resampler; NULL for video.
    AVAudioFifo *fifo; // Buffers to Opus frame_size; NULL for video.

    streamly_emit_cb emit; // Hands each encoded packet to Go.
    uintptr_t emit_user; // Opaque Go handle token.
} OutputPipeline;

// FREEZE_RING_SLOTS frames at FREEZE_INTERVAL_MS cover the encoder's read-ahead
// over the Go packet channel (~4s), so the pause card can freeze on the frame the
// viewer actually saw. Slots hold refs into the filter graph's buffer pool: no copies.
#define FREEZE_RING_SLOTS 8
#define FREEZE_INTERVAL_MS 500

struct transcode_handle {
    pthread_t thread;
    transcode_params_t params;
    InterruptState interrupt;
    int exit_code;
    char error[ERR_BUF];

    pthread_mutex_t freeze_lock;
    AVFrame *freeze_ring[FREEZE_RING_SLOTS];
    int64_t freeze_pts_ms[FREEZE_RING_SLOTS];
    int freeze_next;
};

// freeze_capture refs one post-filter frame into the ring; called from the worker thread only.
static void freeze_capture(transcode_handle_t *handle, const AVFrame *frame, int64_t pts_ms) {

    pthread_mutex_lock(&handle->freeze_lock);

    int slot = handle->freeze_next % FREEZE_RING_SLOTS;

    if (!handle->freeze_ring[slot]) {
        handle->freeze_ring[slot] = av_frame_alloc();
    }

    AVFrame *dst = handle->freeze_ring[slot];

    if (dst) {

        av_frame_unref(dst);

        if (av_frame_ref(dst, frame) >= 0) {
            handle->freeze_pts_ms[slot] = pts_ms;
            handle->freeze_next++;
        }
    }

    pthread_mutex_unlock(&handle->freeze_lock);

}

// freeze_pick returns a new ref to the ring frame nearest target_pts_ms (newest when
// target is negative), or NULL when nothing has been captured yet.
static AVFrame *freeze_pick(transcode_handle_t *handle, int64_t target_pts_ms) {

    AVFrame *out = av_frame_alloc();

    if (!out) {
        return NULL;
    }

    pthread_mutex_lock(&handle->freeze_lock);

    int count = handle->freeze_next < FREEZE_RING_SLOTS ? handle->freeze_next : FREEZE_RING_SLOTS;
    int best = -1;
    int64_t best_score = INT64_MAX;

    for (int i = 0; i < count; i++) {

        if (!handle->freeze_ring[i] || !handle->freeze_ring[i]->data[0]) {
            continue;
        }

        int64_t score;

        if (target_pts_ms < 0) {
            score = INT64_MAX - handle->freeze_pts_ms[i];
        } else {
            score = handle->freeze_pts_ms[i] - target_pts_ms;

            if (score < 0) {
                score = -score;
            }
        }

        if (score < best_score) {
            best_score = score;
            best = i;
        }
    }

    int ret = best >= 0 ? av_frame_ref(out, handle->freeze_ring[best]) : AVERROR(ENOENT);

    pthread_mutex_unlock(&handle->freeze_lock);

    if (ret < 0) {
        av_frame_free(&out);
        return NULL;
    }

    return out;

}

static void set_error(struct transcode_handle *handle, const char *msg) {

    if (!handle || !msg) {
        return;
    }

    snprintf(handle->error, sizeof(handle->error), "%s", msg);

}

static int cb_read_packet(void *opaque, uint8_t *buf, int buf_size) {

    CbIO *io = opaque;

    if (io->abort_flag && *io->abort_flag) {
        return AVERROR_EOF;
    }

    int n = io->read_cb(io->user, buf, buf_size);

    if (n > 0) {
        return n;
    }

    if (n == 0) {
        return AVERROR_EOF;
    }

    return AVERROR(EIO);
}

static int64_t cb_seek(void *opaque, int64_t offset, int whence) {

    CbIO *io = opaque;

    if (io->abort_flag && *io->abort_flag) {
        return AVERROR(EIO);
    }

    if (whence & AVSEEK_SIZE) {

        int64_t size = io->seek_cb(io->user, 0, STREAMLY_SEEK_SIZE);

        return size >= 0 ? size : AVERROR(ENOSYS);
    }

    whence &= ~AVSEEK_FORCE;

    if (whence != SEEK_SET && whence != SEEK_CUR && whence != SEEK_END) {
        return AVERROR(EINVAL);
    }

    int64_t pos = io->seek_cb(io->user, offset, whence);

    return pos >= 0 ? pos : AVERROR(EIO);
}

static int interrupt_callback(void *opaque) {

    InterruptState *state = opaque;

    return state && state->abort_flag && *state->abort_flag;
}

static int open_input_cb(const transcode_params_t *params, AVFormatContext **fmt_out, AVIOContext **avio_out) {

    CbIO *io = av_mallocz(sizeof(CbIO));

    if (!io) {
        return AVERROR(ENOMEM);
    }

    io->read_cb = params->read_cb;
    io->seek_cb = params->seek_cb;
    io->user = params->emit_user;
    io->abort_flag = params->abort_flag;

    uint8_t *buffer = av_malloc(INPUT_AVIO_SIZE);

    if (!buffer) {
        av_free(io);
        return AVERROR(ENOMEM);
    }

    AVIOContext *avio = avio_alloc_context(buffer, INPUT_AVIO_SIZE, 0, io, cb_read_packet, NULL,
                                           params->seek_cb ? cb_seek : NULL);

    if (!avio) {
        av_free(buffer);
        av_free(io);
        return AVERROR(ENOMEM);
    }

    AVFormatContext *fmt = avformat_alloc_context();

    if (!fmt) {
        avio_context_free(&avio);
        av_free(io);
        return AVERROR(ENOMEM);
    }

    fmt->pb = avio;
    fmt->flags |= AVFMT_FLAG_CUSTOM_IO;

    AVDictionary *opts = NULL;
    av_dict_set(&opts, "fflags", "+genpts+discardcorrupt", 0);
    av_dict_set(&opts, "err_detect", "ignore_err", 0);

    int ret = avformat_open_input(&fmt, NULL, NULL, &opts);
    av_dict_free(&opts);

    if (ret < 0) {
        avformat_close_input(&fmt);
        av_freep(&avio->buffer);
        avio_context_free(&avio);
        av_free(io);
        return ret;
    }

    ret = avformat_find_stream_info(fmt, NULL);

    if (ret < 0) {
        avformat_close_input(&fmt);
        av_freep(&avio->buffer);
        avio_context_free(&avio);
        av_free(io);
        return ret;
    }

    *fmt_out = fmt;
    *avio_out = avio;
    return 0;
}

static int open_input_url_once(const char *url, const char *headers, bool live, volatile bool *abort_flag,
                               InterruptState *interrupt, AVFormatContext **fmt_out) {

    AVFormatContext *fmt = avformat_alloc_context();

    if (!fmt) {
        return AVERROR(ENOMEM);
    }

    interrupt->abort_flag = abort_flag;
    fmt->interrupt_callback.callback = interrupt_callback;
    fmt->interrupt_callback.opaque = interrupt;

    AVDictionary *opts = NULL;
    av_dict_set(&opts, "fflags", "+genpts+discardcorrupt", 0);
    av_dict_set(&opts, "err_detect", "ignore_err", 0);
    av_dict_set(&opts, "reconnect", "1", 0);
    av_dict_set(&opts, "reconnect_streamed", "1", 0);
    av_dict_set(&opts, "reconnect_on_network_error", "1", 0);
    av_dict_set(&opts, "reconnect_on_http_error", "4xx,5xx", 0);
    av_dict_set(&opts, "reconnect_delay_max", "8", 0);
    av_dict_set(&opts, "allowed_extensions", "ALL", 0);
    av_dict_set(&opts, "seg_format_options", "f=mpegts", 0);

    if (live) {
        // Start a few segments behind the live edge so short CDN stalls do not underrun.
        av_dict_set(&opts, "live_start_index", "-3", 0);
        av_dict_set(&opts, "seg_max_retry", "8", 0);
        // Keep reloading a static playlist longer before treating it as EOF.
        av_dict_set(&opts, "m3u8_hold_counters", "10", 0);
        av_dict_set(&opts, "http_multiple", "1", 0);
        // Fresh connections avoid sticky TLS desyncs seen on flaky live CDNs.
        av_dict_set(&opts, "http_persistent", "0", 0);
        av_dict_set(&opts, "rw_timeout", "25000000", 0);
        av_dict_set(&opts, "timeout", "25000000", 0);
        fmt->probesize = 8000000;
        fmt->max_analyze_duration = 8000000;
    } else {
        av_dict_set(&opts, "seg_max_retry", "3", 0);
        av_dict_set(&opts, "rw_timeout", "30000000", 0);
        av_dict_set(&opts, "timeout", "30000000", 0);
    }

    if (headers && headers[0] != '\0') {
        av_dict_set(&opts, "headers", headers, 0);
    }

    int ret = avformat_open_input(&fmt, url, NULL, &opts);
    av_dict_free(&opts);

    if (ret < 0) {
        avformat_close_input(&fmt);
        return ret;
    }

    ret = avformat_find_stream_info(fmt, NULL);

    if (ret < 0) {
        avformat_close_input(&fmt);
        return ret;
    }

    *fmt_out = fmt;
    return 0;
}

static int open_input_url(const char *url, const char *headers, bool live, volatile bool *abort_flag,
                          InterruptState *interrupt, AVFormatContext **fmt_out) {

    if (!url || url[0] == '\0') {
        return AVERROR(EINVAL);
    }

    int last_ret = AVERROR(EIO);

    for (int attempt = 0; attempt < INPUT_OPEN_MAX_RETRIES; attempt++) {

        if (abort_flag && *abort_flag) {
            return AVERROR(EIO);
        }

        if (attempt > 0) {
            av_usleep((unsigned)INPUT_OPEN_RETRY_BASE_US * (unsigned)attempt);
        }

        int ret = open_input_url_once(url, headers, live, abort_flag, interrupt, fmt_out);

        if (ret >= 0) {
            return 0;
        }

        last_ret = ret;
    }

    return last_ret;
}

static void queue_init_cap(PacketQueue *q, int capacity) {

    pthread_mutex_init(&q->lock, NULL);
    pthread_cond_init(&q->cond, NULL);
    q->head = NULL;
    q->tail = NULL;
    q->count = 0;
    q->capacity = capacity > 0 ? capacity : PACKET_QUEUE_CAP;
    q->done = false;
    q->exit_code = 0;
    q->initialized = true;

}

static void queue_init(PacketQueue *q) {

    queue_init_cap(q, PACKET_QUEUE_CAP);

}

static void queue_destroy(PacketQueue *q) {

    if (!q || !q->initialized) {
        return;
    }

    pthread_mutex_lock(&q->lock);

    while (q->head) {

        PacketNode *node = q->head;
        q->head = node->next;
        av_packet_free(&node->pkt);
        av_free(node);
    }

    pthread_mutex_unlock(&q->lock);
    pthread_mutex_destroy(&q->lock);
    pthread_cond_destroy(&q->cond);
    q->initialized = false;

}

static int queue_push(PacketQueue *q, AVPacket *pkt) {

    pthread_mutex_lock(&q->lock);

    int cap = q->capacity > 0 ? q->capacity : PACKET_QUEUE_CAP;

    while (q->count >= cap && !q->done) {
        pthread_cond_wait(&q->cond, &q->lock);
    }

    if (q->done) {
        pthread_mutex_unlock(&q->lock);
        return AVERROR_EOF;
    }

    PacketNode *node = av_mallocz(sizeof(PacketNode));

    if (!node) {
        pthread_mutex_unlock(&q->lock);
        return AVERROR(ENOMEM);
    }

    node->pkt = pkt;
    node->next = NULL;

    if (q->tail) {
        q->tail->next = node;
    } else {
        q->head = node;
    }

    q->tail = node;
    q->count++;
    pthread_cond_signal(&q->cond);
    pthread_mutex_unlock(&q->lock);

    return 0;
}

static AVPacket *queue_try_pop(PacketQueue *q) {

    pthread_mutex_lock(&q->lock);

    if (!q->head) {
        pthread_mutex_unlock(&q->lock);
        return NULL;
    }

    PacketNode *node = q->head;
    q->head = node->next;

    if (!q->head) {
        q->tail = NULL;
    }

    q->count--;
    pthread_cond_signal(&q->cond);
    pthread_mutex_unlock(&q->lock);

    AVPacket *pkt = node->pkt;
    av_free(node);

    return pkt;
}

static bool queue_empty_done(PacketQueue *q) {

    pthread_mutex_lock(&q->lock);
    bool done = q->done && q->head == NULL;
    pthread_mutex_unlock(&q->lock);

    return done;
}

static void queue_finish(PacketQueue *q, int exit_code) {

    pthread_mutex_lock(&q->lock);
    q->done = true;
    q->exit_code = exit_code;
    pthread_cond_broadcast(&q->cond);
    pthread_mutex_unlock(&q->lock);

}

static void filter_escape(char *dst, size_t dst_size, const char *src) {

    size_t j = 0;

    for (size_t i = 0; src[i] != '\0' && j + 2 < dst_size; i++) {

        char c = src[i];

        if (c == '\\' || c == ':' || c == '\'' || c == '%' || c == '[' || c == ']') {
            dst[j++] = '\\';
        }

        dst[j++] = c;
    }

    dst[j] = '\0';

}

static void drawtext_escape(char *dst, size_t dst_size, const char *src) {

    size_t j = 0;

    for (size_t i = 0; src[i] != '\0' && j + 2 < dst_size; i++) {

        char c = src[i];

        if (c == '\\' || c == ':' || c == '\'' || c == '%') {
            dst[j++] = '\\';
        }

        dst[j++] = c;
    }

    dst[j] = '\0';

}

static int append_cta_drawtext(char *chain, size_t chain_size, const char *input_pad,
                             const char *output_pad, const transcode_params_t *params) {

    chain[0] = '\0';

    if (!params->cta_font_path || params->cta_font_path[0] == '\0' || params->cta_count <= 0) {
        snprintf(chain, chain_size, "[%s]copy[%s]", input_pad, output_pad);
        return 0;
    }

    char font[1024];
    filter_escape(font, sizeof(font), params->cta_font_path);

    char current[32];
    snprintf(current, sizeof(current), "%s", input_pad);

    // The final emitted segment must write output_pad, so find the last drawable
    // CTA up front; trailing empty entries would otherwise leave the chain dangling.
    int last_valid = -1;

    for (int i = 0; i < params->cta_count && i < STREAMLY_MAX_CTA; i++) {
        if (params->ctas[i].text[0]) {
            last_valid = i;
        }
    }

    if (last_valid < 0) {
        snprintf(chain, chain_size, "[%s]copy[%s]", input_pad, output_pad);
        return 0;
    }

    for (int i = 0; i <= last_valid; i++) {

        const streamly_cta_t *cta = &params->ctas[i];

        if (!cta->text[0]) {
            continue;
        }

        char text[256];
        drawtext_escape(text, sizeof(text), cta->text);

        double start_s = (double)cta->start_ms / 1000.0;
        double end_s = (double)cta->end_ms / 1000.0;

        if (end_s <= start_s) {
            end_s = start_s + 8.0;
        }

        char next[32];
        bool is_last = (i == last_valid);

        if (is_last) {
            snprintf(next, sizeof(next), "%s", output_pad);
        } else {
            snprintf(next, sizeof(next), "cta%d", i);
        }

        char segment[2048];
        int written = snprintf(segment, sizeof(segment),
                 "[%s]drawtext=fontfile='%s':text='%s':fontsize=16:fontcolor=white@0.9:"
                 "shadowcolor=black@0.6:shadowx=1:shadowy=1:box=1:boxcolor=black@0.45:boxborderw=6:"
                 "x=20:y=h-th-22:enable='between(t,%.3f,%.3f)'[%s]",
                 current, font, text, start_s, end_s, next);

        if (written < 0 || (size_t)written >= sizeof(segment)) {
            return AVERROR(ENOMEM);
        }

        if (strlen(chain) + strlen(segment) + 2 >= chain_size) {
            return AVERROR(ENOMEM);
        }

        if (chain[0] != '\0') {
            strncat(chain, ";", chain_size - strlen(chain) - 1);
        }

        strncat(chain, segment, chain_size - strlen(chain) - 1);
        snprintf(current, sizeof(current), "%s", next);
    }

    return 0;

}

static void ensure_decoder_time_base(AVCodecContext *dec, AVStream *stream) {

    if (dec->time_base.num > 0 && dec->time_base.den > 0) {
        return;
    }

    if (stream->time_base.num > 0 && stream->time_base.den > 0) {
        dec->time_base = stream->time_base;
        return;
    }

    if (stream->codecpar->codec_type == AVMEDIA_TYPE_VIDEO) {

        AVRational frame_rate = stream->avg_frame_rate;

        if (frame_rate.num <= 0 || frame_rate.den <= 0) {
            frame_rate = stream->r_frame_rate;
        }

        if (frame_rate.num > 0 && frame_rate.den > 0) {
            dec->time_base = av_inv_q(frame_rate);
            return;
        }

        dec->time_base = (AVRational){1, 25};
        return;
    }

    int sample_rate = dec->sample_rate;

    if (sample_rate <= 0) {
        sample_rate = stream->codecpar->sample_rate;
    }

    if (sample_rate > 0) {
        dec->time_base = (AVRational){1, sample_rate};
        return;
    }

    dec->time_base = (AVRational){1, 48000};

}

static int build_filter_graph(const transcode_params_t *params, AVCodecContext *dec,
                              AVFilterGraph **graph_out, AVFilterContext **src_out,
                              AVFilterContext **sink_out) {

    AVFilterGraph *graph = avfilter_graph_alloc();

    if (!graph) {
        return AVERROR(ENOMEM);
    }

    AVRational time_base = dec->time_base;
    AVRational sample_aspect = dec->sample_aspect_ratio;

    if (time_base.num <= 0 || time_base.den <= 0) {
        time_base = (AVRational){1, params->frame_rate > 0 ? params->frame_rate : 30};
    }

    if (sample_aspect.num <= 0 || sample_aspect.den <= 0) {
        sample_aspect = (AVRational){1, 1};
    }

    char args[512];
    snprintf(args, sizeof(args),
             "video_size=%dx%d:pix_fmt=%d:time_base=%d/%d:pixel_aspect=%d/%d",
             dec->width, dec->height, dec->pix_fmt,
             time_base.num, time_base.den,
             sample_aspect.num, sample_aspect.den);

    const AVFilter *buffersrc = avfilter_get_by_name("buffer");
    const AVFilter *buffersink = avfilter_get_by_name("buffersink");

    AVFilterContext *src = NULL;
    AVFilterContext *sink = NULL;

    int ret = avfilter_graph_create_filter(&src, buffersrc, "in", args, NULL, graph);

    if (ret < 0) {
        avfilter_graph_free(&graph);
        return ret;
    }

    ret = avfilter_graph_create_filter(&sink, buffersink, "out", NULL, NULL, graph);

    if (ret < 0) {
        avfilter_graph_free(&graph);
        return ret;
    }

    char chain[8192];
    char scale_pad[16];
    char post_pad[16];
    char cta_chain[4096];

    chain[0] = '\0';
    cta_chain[0] = '\0';

    snprintf(scale_pad, sizeof(scale_pad), "scaled");

    if (params->live) {
        snprintf(chain, sizeof(chain), "[in]" VIDEO_SCALE_CHAIN_LIVE "[%s]",
                 params->width, params->height, params->frame_rate, scale_pad);
    } else {
        snprintf(chain, sizeof(chain), "[in]" VIDEO_SCALE_CHAIN "[%s]",
                 params->width, params->height, params->frame_rate, scale_pad);
    }

    snprintf(post_pad, sizeof(post_pad), "%s", scale_pad);

    if (params->subtitle_path && params->subtitle_path[0] != '\0') {

        char subs[1024];
        char fonts[1024];
        char sub_segment[4096];
        int sub_written;

        filter_escape(subs, sizeof(subs), params->subtitle_path);

        snprintf(post_pad, sizeof(post_pad), "subbed");

        if (params->fonts_dir && params->fonts_dir[0] != '\0') {
            filter_escape(fonts, sizeof(fonts), params->fonts_dir);
            sub_written = snprintf(sub_segment, sizeof(sub_segment),
                     "[%s]subtitles=filename='%s':charenc=UTF-8:fontsdir='%s':"
                     "force_style='" SUBTITLE_FORCE_STYLE "'[%s]",
                     scale_pad, subs, fonts, post_pad);
        } else {
            sub_written = snprintf(sub_segment, sizeof(sub_segment),
                     "[%s]subtitles=filename='%s':charenc=UTF-8:"
                     "force_style='" SUBTITLE_FORCE_STYLE "'[%s]",
                     scale_pad, subs, post_pad);
        }

        if (sub_written < 0 || (size_t)sub_written >= sizeof(sub_segment)) {
            avfilter_graph_free(&graph);
            return AVERROR(ENOMEM);
        }

        strncat(chain, ";", sizeof(chain) - strlen(chain) - 1);
        strncat(chain, sub_segment, sizeof(chain) - strlen(chain) - 1);

    }

    if (append_cta_drawtext(cta_chain, sizeof(cta_chain), post_pad, "out", params) != 0) {
        avfilter_graph_free(&graph);
        return AVERROR(ENOMEM);
    }

    if (cta_chain[0] != '\0') {
        strncat(chain, ";", sizeof(chain) - strlen(chain) - 1);
        strncat(chain, cta_chain, sizeof(chain) - strlen(chain) - 1);
    } else {
        char copy_segment[64];
        snprintf(copy_segment, sizeof(copy_segment), "[%s]copy[out]", post_pad);
        strncat(chain, ";", sizeof(chain) - strlen(chain) - 1);
        strncat(chain, copy_segment, sizeof(chain) - strlen(chain) - 1);
    }

    AVFilterInOut *inputs = NULL;
    AVFilterInOut *outputs = NULL;

    outputs = avfilter_inout_alloc();
    inputs = avfilter_inout_alloc();

    if (!outputs || !inputs) {
        avfilter_inout_free(&inputs);
        avfilter_inout_free(&outputs);
        avfilter_graph_free(&graph);
        return AVERROR(ENOMEM);
    }

    outputs->name = av_strdup("in");
    outputs->filter_ctx = src;
    outputs->pad_idx = 0;
    outputs->next = NULL;

    inputs->name = av_strdup("out");
    inputs->filter_ctx = sink;
    inputs->pad_idx = 0;
    inputs->next = NULL;

    ret = avfilter_graph_parse_ptr(graph, chain, &inputs, &outputs, NULL);
    avfilter_inout_free(&inputs);
    avfilter_inout_free(&outputs);

    if (ret < 0) {
        avfilter_graph_free(&graph);
        return ret;
    }

    ret = avfilter_graph_config(graph, NULL);

    if (ret < 0) {
        avfilter_graph_free(&graph);
        return ret;
    }

    *graph_out = graph;
    *src_out = src;
    *sink_out = sink;

    return 0;
}

static int open_decoder(AVFormatContext *fmt, int wanted_type, AVCodecContext **dec_out, int *stream_out) {

    int stream_index = av_find_best_stream(fmt, wanted_type, -1, -1, NULL, 0);

    if (stream_index < 0) {
        return stream_index;
    }

    AVStream *stream = fmt->streams[stream_index];
    const AVCodec *dec = avcodec_find_decoder(stream->codecpar->codec_id);

    if (!dec) {
        return AVERROR_DECODER_NOT_FOUND;
    }

    AVCodecContext *dec_ctx = avcodec_alloc_context3(dec);

    if (!dec_ctx) {
        return AVERROR(ENOMEM);
    }

    int ret = avcodec_parameters_to_context(dec_ctx, stream->codecpar);

    if (ret < 0) {
        avcodec_free_context(&dec_ctx);
        return ret;
    }

    dec_ctx->thread_count = 0;
    ret = avcodec_open2(dec_ctx, dec, NULL);

    if (ret < 0) {
        avcodec_free_context(&dec_ctx);
        return ret;
    }

    ensure_decoder_time_base(dec_ctx, stream);

    if (dec_ctx->sample_aspect_ratio.num <= 0 || dec_ctx->sample_aspect_ratio.den <= 0) {
        dec_ctx->sample_aspect_ratio = (AVRational){1, 1};
    }

    *dec_out = dec_ctx;
    *stream_out = stream_index;

    return 0;
}

static int open_video_encoder(const transcode_params_t *params, AVCodecContext *filt,
                              OutputPipeline *out) {

    const AVCodec *enc = avcodec_find_encoder_by_name("libx264");

    if (!enc) {
        return AVERROR_ENCODER_NOT_FOUND;
    }

    AVCodecContext *enc_ctx = avcodec_alloc_context3(enc);

    if (!enc_ctx) {
        return AVERROR(ENOMEM);
    }

    enc_ctx->width = params->width;
    enc_ctx->height = params->height;
    enc_ctx->pix_fmt = AV_PIX_FMT_YUV420P;
    enc_ctx->time_base = (AVRational){1, params->frame_rate};
    enc_ctx->framerate = (AVRational){params->frame_rate, 1};
    enc_ctx->bit_rate = (int64_t)params->bitrate_video_k * 1000;
    enc_ctx->rc_max_rate = (int64_t)params->bitrate_video_max_k * 1000;
    enc_ctx->rc_buffer_size = (int)((params->bitrate_video_k / 2) * 1000);
    enc_ctx->gop_size = params->frame_rate;
    enc_ctx->max_b_frames = 0;
    enc_ctx->sample_aspect_ratio = filt->sample_aspect_ratio;

    if (params->live) {
        enc_ctx->thread_count = params->threads > 0 ? params->threads : 2;
    } else if (params->threads > 0) {
        enc_ctx->thread_count = params->threads;
    }

    // No muxer: keep SPS/PPS in-band Annex-B so the WebRTC H264 packetizer can ship frames directly.
    AVDictionary *opts = NULL;

    if (params->live) {
        av_dict_set(&opts, "preset", "ultrafast", 0);
        av_dict_set(&opts, "tune", "zerolatency", 0);
    } else {
        av_dict_set(&opts, "preset", "superfast", 0);
        av_dict_set(&opts, "tune", "film", 0);
    }

    av_dict_set(&opts, "profile", "baseline", 0);
    av_dict_set(&opts, "level", "3.1", 0);
    av_dict_set(&opts, "forced-idr", "1", 0);
    av_dict_set(&opts, "bf", "0", 0);
    av_dict_set(&opts, "repeat-headers", "1", 0);

    int ret = avcodec_open2(enc_ctx, enc, &opts);
    av_dict_free(&opts);

    if (ret < 0) {
        avcodec_free_context(&enc_ctx);
        return ret;
    }

    out->kind = STREAMLY_KIND_VIDEO;
    out->enc = enc_ctx;
    out->frame = av_frame_alloc();
    out->pkt = av_packet_alloc();
    out->next_pts = 0;
    out->frame_rate = params->frame_rate;
    out->emit = params->emit;
    out->emit_user = params->emit_user;

    if (!out->frame || !out->pkt) {
        return AVERROR(ENOMEM);
    }

    return 0;
}

static int open_audio_encoder(const transcode_params_t *params, AVCodecContext *dec,
                              OutputPipeline *out, SwrContext **swr_out) {

    const AVCodec *enc = avcodec_find_encoder_by_name("libopus");

    if (!enc) {
        return AVERROR_ENCODER_NOT_FOUND;
    }

    AVCodecContext *enc_ctx = avcodec_alloc_context3(enc);

    if (!enc_ctx) {
        return AVERROR(ENOMEM);
    }

    enc_ctx->sample_rate = 48000;
    av_channel_layout_default(&enc_ctx->ch_layout, 2);
    enc_ctx->sample_fmt = enc->sample_fmts ? enc->sample_fmts[0] : AV_SAMPLE_FMT_FLTP;
    enc_ctx->bit_rate = (int64_t)params->bitrate_audio_k * 1000;
    enc_ctx->time_base = (AVRational){1, enc_ctx->sample_rate};

    int ret = avcodec_open2(enc_ctx, enc, NULL);

    if (ret < 0) {
        avcodec_free_context(&enc_ctx);
        return ret;
    }

    SwrContext *swr = NULL;

    ret = swr_alloc_set_opts2(&swr,
                              &enc_ctx->ch_layout, enc_ctx->sample_fmt, enc_ctx->sample_rate,
                              &dec->ch_layout, dec->sample_fmt, dec->sample_rate,
                              0, NULL);

    if (ret < 0) {
        avcodec_free_context(&enc_ctx);
        return ret;
    }

    ret = swr_init(swr);

    if (ret < 0) {
        swr_free(&swr);
        avcodec_free_context(&enc_ctx);
        return ret;
    }

    int frame_size = enc_ctx->frame_size > 0 ? enc_ctx->frame_size : 960;
    AVAudioFifo *fifo = av_audio_fifo_alloc(enc_ctx->sample_fmt, enc_ctx->ch_layout.nb_channels, frame_size * 16);

    if (!fifo) {
        swr_free(&swr);
        avcodec_free_context(&enc_ctx);
        return AVERROR(ENOMEM);
    }

    out->kind = STREAMLY_KIND_AUDIO;
    out->enc = enc_ctx;
    out->frame = av_frame_alloc();
    out->swr_frame = av_frame_alloc();
    out->pkt = av_packet_alloc();
    out->next_pts = 0;
    out->frame_rate = 0;
    out->emit = params->emit;
    out->emit_user = params->emit_user;
    out->swr = swr;
    out->fifo = fifo;

    if (!out->frame || !out->swr_frame || !out->pkt) {
        av_audio_fifo_free(fifo);
        out->fifo = NULL;
        swr_free(&swr);
        out->swr = NULL;
        return AVERROR(ENOMEM);
    }

    if (swr_out) {
        *swr_out = swr;
    }

    return 0;
}

static void *muxed_reader_thread(void *arg) {

    ReaderState *state = arg;
    int ret = 0;

    while (!(state->abort_flag && *state->abort_flag)) {

        AVPacket *pkt = av_packet_alloc();

        if (!pkt) {
            ret = AVERROR(ENOMEM);
            break;
        }

        ret = av_read_frame(state->fmt, pkt);

        if (ret < 0) {
            av_packet_free(&pkt);
            break;
        }

        if (pkt->stream_index == state->video_stream) {
            ret = queue_push(state->video_queue, pkt);
        } else if (state->audio_stream >= 0 && pkt->stream_index == state->audio_stream) {
            ret = queue_push(state->audio_queue, pkt);
        } else {
            av_packet_free(&pkt);
            continue;
        }

        if (ret < 0) {
            av_packet_free(&pkt);
            break;
        }
    }

    queue_finish(state->video_queue, ret < 0 ? ret : 0);

    if (state->audio_queue) {
        queue_finish(state->audio_queue, ret < 0 ? ret : 0);
    }

    return NULL;
}

static int encode_write_frame(OutputPipeline *out, AVFrame *frame);

static int audio_encoder_frame_size(const OutputPipeline *aout) {

    int frame_size = aout->enc->frame_size;

    if (frame_size <= 0) {
        frame_size = 960;
    }

    return frame_size;
}

static int prepare_audio_encode_frame(OutputPipeline *aout, int nb_samples) {

    AVFrame *frame = aout->frame;

    av_frame_unref(frame);

    frame->format = aout->enc->sample_fmt;
    av_channel_layout_copy(&frame->ch_layout, &aout->enc->ch_layout);
    frame->sample_rate = aout->enc->sample_rate;
    frame->nb_samples = nb_samples;

    return av_frame_get_buffer(frame, 0);
}

static int drain_audio_fifo(OutputPipeline *aout, volatile bool *abort_flag) {

    int frame_size = audio_encoder_frame_size(aout);

    while (av_audio_fifo_size(aout->fifo) >= frame_size) {

        int ret = prepare_audio_encode_frame(aout, frame_size);

        if (ret < 0) {
            return ret;
        }

        ret = av_audio_fifo_read(aout->fifo, (void **)aout->frame->data, frame_size);

        if (ret < frame_size) {
            return AVERROR(EIO);
        }

        aout->frame->pts = aout->next_pts;
        aout->next_pts += frame_size;

        ret = encode_write_frame(aout, aout->frame);
        av_frame_unref(aout->frame);

        if (ret < 0) {
            return ret;
        }

        if (abort_flag && *abort_flag) {
            return AVERROR(EIO);
        }
    }

    return 0;
}

static int flush_audio_pipeline(OutputPipeline *aout) {

    int ret = drain_audio_fifo(aout, NULL);

    if (ret < 0) {
        return ret;
    }

    int frame_size = audio_encoder_frame_size(aout);
    int remaining = av_audio_fifo_size(aout->fifo);

    if (remaining > 0) {

        ret = prepare_audio_encode_frame(aout, frame_size);

        if (ret < 0) {
            return ret;
        }

        ret = av_audio_fifo_read(aout->fifo, (void **)aout->frame->data, remaining);

        if (ret < 0) {
            return ret;
        }

        int bytes_per_sample = av_get_bytes_per_sample(aout->frame->format);
        int channels = aout->frame->ch_layout.nb_channels;

        if (av_sample_fmt_is_planar(aout->frame->format)) {

            for (int ch = 0; ch < channels; ch++) {
                memset(aout->frame->data[ch] + ret * bytes_per_sample, 0, (frame_size - ret) * bytes_per_sample);
            }

        } else {

            memset(aout->frame->data[0] + ret * channels * bytes_per_sample, 0,
                   (frame_size - ret) * channels * bytes_per_sample);
        }

        aout->frame->pts = aout->next_pts;
        aout->next_pts += frame_size;

        ret = encode_write_frame(aout, aout->frame);
        av_frame_unref(aout->frame);

        if (ret < 0) {
            return ret;
        }
    }

    return encode_write_frame(aout, NULL);
}

static int encode_write_frame(OutputPipeline *out, AVFrame *frame) {

    int ret = avcodec_send_frame(out->enc, frame);

    if (ret < 0) {
        return ret;
    }

    const AVRational ms = (AVRational){1, 1000};

    while (ret >= 0) {

        ret = avcodec_receive_packet(out->enc, out->pkt);

        if (ret == AVERROR(EAGAIN) || ret == AVERROR_EOF) {
            return 0;
        }

        if (ret < 0) {
            return ret;
        }

        int64_t pts_ms = out->pkt->pts == AV_NOPTS_VALUE ? 0 : av_rescale_q(out->pkt->pts, out->enc->time_base, ms);
        int64_t dur_ms = out->pkt->duration > 0 ? av_rescale_q(out->pkt->duration, out->enc->time_base, ms) : 0;

        if (dur_ms <= 0 && out->kind == STREAMLY_KIND_VIDEO && out->frame_rate > 0) {
            dur_ms = (int64_t)(1000.0 / out->frame_rate + 0.5);
        }

        if (out->emit) {
            out->emit(out->emit_user, out->kind, out->pkt->data, out->pkt->size, pts_ms, dur_ms);
        }

        av_packet_unref(out->pkt);
    }

    return 0;
}

static int process_video_packet(transcode_handle_t *handle, StreamPipeline *video, OutputPipeline *vout,
                                AVFilterContext *filt_src, AVFilterContext *filt_sink,
                                volatile bool *abort_flag, bool live) {

    int freeze_stride = 1;

    if (vout->frame_rate > 0) {

        freeze_stride = vout->frame_rate * FREEZE_INTERVAL_MS / 1000;

        if (freeze_stride < 1) {
            freeze_stride = 1;
        }
    }

    int ret = avcodec_send_packet(video->dec, video->pkt);

    if (ret < 0) {
        // Live HLS regularly ships partial/corrupt segments; skip rather than kill the session.
        return live ? 0 : ret;
    }

    while (ret >= 0) {

        ret = avcodec_receive_frame(video->dec, video->frame);

        if (ret == AVERROR(EAGAIN) || ret == AVERROR_EOF) {
            return 0;
        }

        if (ret < 0) {
            return live ? 0 : ret;
        }

        if (video->skip_until_us >= 0) {

            int64_t ts = video->frame->best_effort_timestamp;

            if (ts != AV_NOPTS_VALUE && av_rescale_q(ts, video->tb, AV_TIME_BASE_Q) < video->skip_until_us) {
                av_frame_unref(video->frame);
                continue;
            }

            video->skip_until_us = -1;
            video->dec->skip_frame = AVDISCARD_DEFAULT;
        }

        ret = av_buffersrc_add_frame_flags(filt_src, video->frame, AV_BUFFERSRC_FLAG_KEEP_REF);

        if (ret < 0) {
            return ret;
        }

        while (ret >= 0) {

            ret = av_buffersink_get_frame(filt_sink, vout->frame);

            if (ret == AVERROR(EAGAIN) || ret == AVERROR_EOF) {
                break;
            }

            if (ret < 0) {
                return ret;
            }

            if (handle && vout->next_pts % freeze_stride == 0 && vout->frame_rate > 0) {
                freeze_capture(handle, vout->frame, vout->next_pts * 1000 / vout->frame_rate);
            }

            vout->frame->pts = vout->next_pts++;
            ret = encode_write_frame(vout, vout->frame);
            av_frame_unref(vout->frame);

            if (ret < 0) {
                return ret;
            }

            if (abort_flag && *abort_flag) {
                return AVERROR(EIO);
            }
        }
    }

    return 0;
}

static int process_audio_packet(StreamPipeline *audio, OutputPipeline *aout,
                                volatile bool *abort_flag, bool live) {

    SwrContext *swr = aout->swr;

    int ret = avcodec_send_packet(audio->dec, audio->pkt);

    if (ret < 0) {
        return live ? 0 : ret;
    }

    while (ret >= 0) {

        ret = avcodec_receive_frame(audio->dec, audio->frame);

        if (ret == AVERROR(EAGAIN) || ret == AVERROR_EOF) {
            return 0;
        }

        if (ret < 0) {
            return live ? 0 : ret;
        }

        if (audio->skip_until_us >= 0) {

            int64_t ts = audio->frame->best_effort_timestamp;

            if (ts != AV_NOPTS_VALUE && av_rescale_q(ts, audio->tb, AV_TIME_BASE_Q) < audio->skip_until_us) {
                av_frame_unref(audio->frame);
                continue;
            }

            audio->skip_until_us = -1;
        }

        if (!swr) {
            av_frame_unref(audio->frame);
            continue;
        }

        AVFrame *converted = aout->swr_frame;
        av_frame_unref(converted);

        converted->format = aout->enc->sample_fmt;
        av_channel_layout_copy(&converted->ch_layout, &aout->enc->ch_layout);
        converted->sample_rate = aout->enc->sample_rate;
        converted->nb_samples = av_rescale_rnd(swr_get_delay(swr, audio->frame->sample_rate) + audio->frame->nb_samples,
                                                 aout->enc->sample_rate, audio->frame->sample_rate, AV_ROUND_UP);

        ret = av_frame_get_buffer(converted, 0);

        if (ret < 0) {
            av_frame_unref(audio->frame);
            return ret;
        }

        ret = swr_convert(swr,
                          (uint8_t **)converted->data, converted->nb_samples,
                          (const uint8_t **)audio->frame->data, audio->frame->nb_samples);

        av_frame_unref(audio->frame);

        if (ret < 0) {
            return ret;
        }

        if (ret > 0) {

            ret = av_audio_fifo_write(aout->fifo, (void **)converted->data, ret);

            if (ret < 0) {
                av_frame_unref(converted);
                return ret;
            }
        }

        av_frame_unref(converted);

        ret = drain_audio_fifo(aout, abort_flag);

        if (ret < 0) {
            return ret;
        }
    }

    return 0;
}

static int drain_available_audio(StreamPipeline *audio, OutputPipeline *aout, bool *audio_done,
                                 volatile bool *abort_flag, bool *progressed, bool live) {

    if (*audio_done) {
        return 0;
    }

    for (int i = 0; i < AUDIO_DRAIN_BATCH; i++) {

        AVPacket *apkt = queue_try_pop(&audio->queue);

        if (!apkt) {

            if (queue_empty_done(&audio->queue)) {
                *audio_done = true;
            }

            return 0;
        }

        *progressed = true;

        av_packet_unref(audio->pkt);
        av_packet_free(&audio->pkt);
        audio->pkt = apkt;

        int ret = process_audio_packet(audio, aout, abort_flag, live);

        if (ret < 0) {
            return ret;
        }
    }

    return 0;
}

static void cleanup_output(OutputPipeline *out) {

    if (!out) {
        return;
    }

    if (out->fifo) {
        av_audio_fifo_free(out->fifo);
        out->fifo = NULL;
    }
    swr_free(&out->swr);
    av_frame_free(&out->swr_frame);
    av_frame_free(&out->frame);
    av_packet_free(&out->pkt);
    avcodec_free_context(&out->enc);

}

static void cleanup_stream(StreamPipeline *pipe) {

    if (!pipe) {
        return;
    }

    queue_destroy(&pipe->queue);
    av_frame_free(&pipe->frame);
    av_packet_free(&pipe->pkt);
    avcodec_free_context(&pipe->dec);

    if (pipe->fmt) {
        avformat_close_input(&pipe->fmt);
    }

    if (pipe->avio) {
        av_freep(&pipe->avio->buffer);
        av_freep(&pipe->avio->opaque);
        avio_context_free(&pipe->avio);
    }

}

static int transcode_run(struct transcode_handle *handle) {

    transcode_params_t params = handle->params;
    int ret = 0;

    av_log_set_level(AV_LOG_ERROR);

    StreamPipeline video = {0};
    StreamPipeline audio = {0};
    OutputPipeline vout = {0};
    OutputPipeline aout = {0};
    AVFilterGraph *graph = NULL;
    AVFilterContext *filt_src = NULL;
    AVFilterContext *filt_sink = NULL;
    bool have_audio = false;

    if (params.input_url && params.input_url[0] != '\0') {
        ret = open_input_url(params.input_url, params.headers, params.live, params.abort_flag, &handle->interrupt, &video.fmt);
    } else if (params.read_cb) {
        ret = open_input_cb(&params, &video.fmt, &video.avio);
    } else {
        ret = AVERROR(EINVAL);
    }

    if (ret < 0) {
        set_error(handle, "failed to open video input");
        goto done;
    }

    if (params.meta_cb) {

        int64_t duration_ms = -1;

        if (video.fmt->duration != AV_NOPTS_VALUE && video.fmt->duration > 0) {
            duration_ms = video.fmt->duration / 1000;
        }

        params.meta_cb(params.emit_user, duration_ms);
    }

    streamly_fill_ctas(params.emit_user, &handle->params);

    // Jump to the requested start by container index, then discard decoded frames
    // up to the exact target so audio and video both begin precisely there.
    int64_t skip_until_us = -1;

    if (params.start_ms > 0) {

        int64_t target_us = params.start_ms * 1000;

        if (video.fmt->start_time != AV_NOPTS_VALUE) {
            target_us += video.fmt->start_time;
        }

        ret = av_seek_frame(video.fmt, -1, target_us, AVSEEK_FLAG_BACKWARD);

        if (ret < 0) {
            set_error(handle, "failed to seek input");
            goto done;
        }

        skip_until_us = target_us;
    }

    ret = open_decoder(video.fmt, AVMEDIA_TYPE_VIDEO, &video.dec, &video.stream_index);

    if (ret < 0) {
        set_error(handle, "failed to open video decoder");
        goto done;
    }

    video.tb = video.fmt->streams[video.stream_index]->time_base;
    video.skip_until_us = skip_until_us;

    // During seek-in, skip non-reference (B) frames: they're the source of
    // "co located POCs unavailable" / "mmco: unref short failure" warnings because
    // their co-located reference frames were never decoded. I/P frames are still
    // decoded normally to rebuild the DPB before we start encoding.
    if (skip_until_us >= 0) {
        video.dec->skip_frame = AVDISCARD_NONREF;
    }

    ret = open_decoder(video.fmt, AVMEDIA_TYPE_AUDIO, &audio.dec, &audio.stream_index);

    if (ret >= 0) {
        audio.fmt = NULL;
        audio.tb = video.fmt->streams[audio.stream_index]->time_base;
        audio.skip_until_us = skip_until_us;
        have_audio = true;
    }

    ret = build_filter_graph(&params, video.dec, &graph, &filt_src, &filt_sink);

    if (ret < 0) {
        set_error(handle, "failed to build filter graph");
        goto done;
    }

    ret = open_video_encoder(&params, video.dec, &vout);

    if (ret < 0) {
        set_error(handle, "failed to open video encoder");
        goto done;
    }

    if (have_audio) {

        ret = open_audio_encoder(&params, audio.dec, &aout, NULL);

        if (ret < 0) {
            set_error(handle, "failed to open audio encoder");
            goto done;
        }

    }

    video.frame = av_frame_alloc();
    video.pkt = av_packet_alloc();
    audio.frame = av_frame_alloc();
    audio.pkt = av_packet_alloc();

    if (!video.frame || !video.pkt || !audio.frame || !audio.pkt) {
        ret = AVERROR(ENOMEM);
        set_error(handle, "allocation failed");
        goto done;
    }

    ReaderState muxed_reader = {0};
    pthread_t muxed_thread;
    bool muxed_reader_started = false;

    int queue_cap = params.live ? PACKET_QUEUE_CAP_LIVE : PACKET_QUEUE_CAP;

    queue_init_cap(&video.queue, queue_cap);
    queue_init_cap(&audio.queue, queue_cap);

    muxed_reader.fmt = video.fmt;
    muxed_reader.video_stream = video.stream_index;
    muxed_reader.audio_stream = have_audio ? audio.stream_index : -1;
    muxed_reader.video_queue = &video.queue;
    muxed_reader.audio_queue = have_audio ? &audio.queue : NULL;
    muxed_reader.abort_flag = params.abort_flag;

    pthread_create(&muxed_thread, NULL, muxed_reader_thread, &muxed_reader);
    muxed_reader_started = true;

    bool video_done = false;
    bool audio_done = !have_audio;

    while (!video_done || !audio_done) {

        if (params.abort_flag && *params.abort_flag) {
            ret = AVERROR(EIO);
            break;
        }

        bool progressed = false;

        if (!video_done) {

            AVPacket *vpkt = queue_try_pop(&video.queue);

            if (!vpkt && queue_empty_done(&video.queue)) {
                video_done = true;
            } else if (vpkt) {

                progressed = true;

                av_packet_unref(video.pkt);
                av_packet_free(&video.pkt);
                video.pkt = vpkt;

                ret = process_video_packet(handle, &video, &vout, filt_src, filt_sink, params.abort_flag,
                                           params.live);

                if (ret < 0) {
                    break;
                }
            }
        }

        if (have_audio) {

            ret = drain_available_audio(&audio, &aout, &audio_done, params.abort_flag, &progressed,
                                        params.live);

            if (ret < 0) {
                break;
            }
        }

        if (!progressed && (!video_done || !audio_done)) {
            av_usleep(1000);
        }
    }

    if (ret >= 0 && !(params.abort_flag && *params.abort_flag)) {

        ret = av_buffersrc_add_frame_flags(filt_src, NULL, 0);

        if (ret >= 0) {

            while (ret >= 0) {

                ret = av_buffersink_get_frame(filt_sink, vout.frame);

                if (ret == AVERROR(EAGAIN) || ret == AVERROR_EOF) {
                    break;
                }

                if (ret < 0) {
                    break;
                }

                vout.frame->pts = vout.next_pts++;
                encode_write_frame(&vout, vout.frame);
                av_frame_unref(vout.frame);
            }
        }

        encode_write_frame(&vout, NULL);

        if (have_audio) {
            flush_audio_pipeline(&aout);
        }
    }

    // Draining legitimately ends in EOF/EAGAIN; only real errors should fail the job.
    if (ret == AVERROR_EOF || ret == AVERROR(EAGAIN)) {
        ret = 0;
    }

done:

    if (muxed_reader_started) {
        queue_finish(&video.queue, ret);
        queue_finish(&audio.queue, ret);
        pthread_join(muxed_thread, NULL);
        muxed_reader_started = false;
    }

    if (graph) {
        avfilter_graph_free(&graph);
    }

    cleanup_output(&vout);
    cleanup_output(&aout);
    cleanup_stream(&video);
    cleanup_stream(&audio);

    if (ret < 0 && !(params.abort_flag && *params.abort_flag)) {

        char errbuf[AV_ERROR_MAX_STRING_SIZE] = {0};
        av_strerror(ret, errbuf, sizeof(errbuf));

        if (handle->error[0] == '\0') {
            set_error(handle, errbuf);
        }

    }

    handle->exit_code = (params.abort_flag && *params.abort_flag) ? 0 : (ret < 0 ? ret : 0);

    return handle->exit_code;
}

static void *transcode_thread(void *arg) {

    struct transcode_handle *handle = arg;
    transcode_run(handle);
    return NULL;
}

static AVFrame *alloc_black_frame(int width, int height) {

    AVFrame *frame = av_frame_alloc();

    if (!frame) {
        return NULL;
    }

    frame->format = AV_PIX_FMT_YUV420P;
    frame->width = width;
    frame->height = height;

    if (av_frame_get_buffer(frame, 0) < 0) {
        av_frame_free(&frame);
        return NULL;
    }

    memset(frame->data[0], 16, (size_t)frame->linesize[0] * height);
    memset(frame->data[1], 128, (size_t)frame->linesize[1] * (height / 2));
    memset(frame->data[2], 128, (size_t)frame->linesize[2] * (height / 2));

    return frame;

}

// append_pause_drawtext adds one horizontally centered drawtext to the pause card chain.
static int append_pause_drawtext(char *chain, size_t chain_size, char *current, size_t current_size,
                                 int *pad_index, const char *font, const char *raw_text,
                                 int fontsize, int y, const char *color) {

    if (!raw_text || raw_text[0] == '\0') {
        return 0;
    }

    char text[1024];
    drawtext_escape(text, sizeof(text), raw_text);

    char next[32];
    snprintf(next, sizeof(next), "pt%d", (*pad_index)++);

    char segment[2048];
    int written = snprintf(segment, sizeof(segment),
             ";[%s]drawtext=fontfile='%s':text='%s':fontsize=%d:fontcolor=%s:"
             "x=(w-text_w)/2:y=%d[%s]",
             current, font, text, fontsize, color, y, next);

    if (written < 0 || (size_t)written >= sizeof(segment)) {
        return AVERROR(ENOMEM);
    }

    if (strlen(chain) + strlen(segment) + 1 >= chain_size) {
        return AVERROR(ENOMEM);
    }

    strcat(chain, segment);
    snprintf(current, current_size, "%s", next);

    return 0;

}

// build_pause_card_chain composes: full-frame dim, large centered panel, then the
// title / episode / description / CTA lines. Always terminates the chain at [out].
static int build_pause_card_chain(const transcode_params_t *params, const streamly_pause_card_t *card,
                                  char *chain, size_t chain_size) {

    int w = params->width;
    int h = params->height;

    int panel_x = w * 10 / 100;
    int panel_y = h * 18 / 100;
    int panel_w = w * 80 / 100;
    int panel_h = h * 64 / 100;

    int written = snprintf(chain, chain_size,
             "[in]drawbox=x=0:y=0:w=iw:h=ih:color=black@0.35:t=fill[dim];"
             "[dim]drawbox=x=%d:y=%d:w=%d:h=%d:color=black@0.78:t=fill[panel]",
             panel_x, panel_y, panel_w, panel_h);

    if (written < 0 || (size_t)written >= chain_size) {
        return AVERROR(ENOMEM);
    }

    char current[32] = "panel";
    int pad_index = 0;
    int ret = 0;

    if (card->font_path && card->font_path[0] != '\0') {

        char font[1024];
        filter_escape(font, sizeof(font), card->font_path);

        int title_size = h / 16;
        int subtitle_size = h / 26;
        int body_size = h / 32;
        int cta_size = h / 30;

        int cursor = panel_y + h * 6 / 100;

        if (card->title && card->title[0] != '\0') {

            ret = append_pause_drawtext(chain, chain_size, current, sizeof(current), &pad_index,
                                        font, card->title, title_size, cursor, "white@0.95");

            if (ret < 0) {
                return ret;
            }

            cursor += title_size + h * 1 / 100;
        }

        if (card->subtitle && card->subtitle[0] != '\0') {

            ret = append_pause_drawtext(chain, chain_size, current, sizeof(current), &pad_index,
                                        font, card->subtitle, subtitle_size, cursor, "white@0.85");

            if (ret < 0) {
                return ret;
            }

            cursor += subtitle_size + h * 5 / 100;
        } else {
            cursor += h * 2 / 100;
        }

        for (int i = 0; i < card->body_count && i < STREAMLY_PAUSE_BODY_LINES; i++) {

            ret = append_pause_drawtext(chain, chain_size, current, sizeof(current), &pad_index,
                                        font, card->body[i], body_size, cursor, "white@0.75");

            if (ret < 0) {
                return ret;
            }

            cursor += body_size * 3 / 2;
        }

        int cta_y = panel_y + panel_h - cta_size - h * 5 / 100;

        ret = append_pause_drawtext(chain, chain_size, current, sizeof(current), &pad_index,
                                    font, card->cta, cta_size, cta_y, "white@0.9");

        if (ret < 0) {
            return ret;
        }
    }

    char tail[64];
    written = snprintf(tail, sizeof(tail), ";[%s]copy[out]", current);

    if (written < 0 || (size_t)written >= sizeof(tail) ||
        strlen(chain) + strlen(tail) + 1 >= chain_size) {
        return AVERROR(ENOMEM);
    }

    strcat(chain, tail);

    return 0;

}

// run_pause_card_filter draws the card over base, returning a new filtered frame.
static int run_pause_card_filter(const transcode_params_t *params, const streamly_pause_card_t *card,
                                 AVFrame *base, AVFrame **out_frame) {

    char chain[8192];

    int ret = build_pause_card_chain(params, card, chain, sizeof(chain));

    if (ret < 0) {
        return ret;
    }

    AVFilterGraph *graph = avfilter_graph_alloc();

    if (!graph) {
        return AVERROR(ENOMEM);
    }

    int fps = params->frame_rate > 0 ? params->frame_rate : 30;

    char args[256];
    snprintf(args, sizeof(args),
             "video_size=%dx%d:pix_fmt=%d:time_base=1/%d:pixel_aspect=1/1",
             params->width, params->height, AV_PIX_FMT_YUV420P, fps);

    AVFilterContext *src = NULL;
    AVFilterContext *sink = NULL;

    ret = avfilter_graph_create_filter(&src, avfilter_get_by_name("buffer"), "in", args, NULL, graph);

    if (ret >= 0) {
        ret = avfilter_graph_create_filter(&sink, avfilter_get_by_name("buffersink"), "out", NULL, NULL, graph);
    }

    if (ret >= 0) {

        AVFilterInOut *outputs = avfilter_inout_alloc();
        AVFilterInOut *inputs = avfilter_inout_alloc();

        if (!outputs || !inputs) {
            avfilter_inout_free(&inputs);
            avfilter_inout_free(&outputs);
            ret = AVERROR(ENOMEM);
        } else {

            outputs->name = av_strdup("in");
            outputs->filter_ctx = src;
            outputs->pad_idx = 0;
            outputs->next = NULL;

            inputs->name = av_strdup("out");
            inputs->filter_ctx = sink;
            inputs->pad_idx = 0;
            inputs->next = NULL;

            ret = avfilter_graph_parse_ptr(graph, chain, &inputs, &outputs, NULL);
            avfilter_inout_free(&inputs);
            avfilter_inout_free(&outputs);
        }
    }

    if (ret >= 0) {
        ret = avfilter_graph_config(graph, NULL);
    }

    AVFrame *filtered = NULL;

    if (ret >= 0) {

        base->pts = 0;
        ret = av_buffersrc_add_frame_flags(src, base, AV_BUFFERSRC_FLAG_KEEP_REF);

        if (ret >= 0) {
            ret = av_buffersrc_add_frame_flags(src, NULL, 0);
        }

        if (ret >= 0) {

            filtered = av_frame_alloc();

            if (!filtered) {
                ret = AVERROR(ENOMEM);
            } else {

                ret = av_buffersink_get_frame(sink, filtered);

                if (ret < 0) {
                    av_frame_free(&filtered);
                }
            }
        }
    }

    avfilter_graph_free(&graph);

    if (ret < 0) {
        return ret;
    }

    *out_frame = filtered;

    return 0;

}

// encode_pause_idr encodes one frame as a self-contained Annex-B IDR (in-band SPS/PPS).
static int encode_pause_idr(const transcode_params_t *params, AVFrame *frame,
                            uint8_t **out_data, int *out_len) {

    const AVCodec *enc = avcodec_find_encoder_by_name("libx264");

    if (!enc) {
        return AVERROR_ENCODER_NOT_FOUND;
    }

    AVCodecContext *ctx = avcodec_alloc_context3(enc);

    if (!ctx) {
        return AVERROR(ENOMEM);
    }

    int fps = params->frame_rate > 0 ? params->frame_rate : 30;

    ctx->width = params->width;
    ctx->height = params->height;
    ctx->pix_fmt = AV_PIX_FMT_YUV420P;
    ctx->time_base = (AVRational){1, fps};
    ctx->framerate = (AVRational){fps, 1};
    ctx->bit_rate = (int64_t)params->bitrate_video_k * 1000;
    ctx->gop_size = 1;
    ctx->max_b_frames = 0;
    ctx->thread_count = 1;
    ctx->sample_aspect_ratio = (AVRational){1, 1};

    AVDictionary *opts = NULL;
    av_dict_set(&opts, "preset", "ultrafast", 0);
    av_dict_set(&opts, "tune", "zerolatency", 0);
    av_dict_set(&opts, "profile", "baseline", 0);
    av_dict_set(&opts, "level", "3.1", 0);
    av_dict_set(&opts, "forced-idr", "1", 0);
    av_dict_set(&opts, "repeat-headers", "1", 0);

    int ret = avcodec_open2(ctx, enc, &opts);
    av_dict_free(&opts);

    if (ret < 0) {
        avcodec_free_context(&ctx);
        return ret;
    }

    AVPacket *pkt = av_packet_alloc();

    if (!pkt) {
        avcodec_free_context(&ctx);
        return AVERROR(ENOMEM);
    }

    frame->pts = 0;
    frame->pict_type = AV_PICTURE_TYPE_I;

    ret = avcodec_send_frame(ctx, frame);

    if (ret >= 0) {
        ret = avcodec_send_frame(ctx, NULL);
    }

    uint8_t *data = NULL;
    int len = 0;

    while (ret >= 0) {

        ret = avcodec_receive_packet(ctx, pkt);

        if (ret < 0) {
            break;
        }

        if (!data && pkt->size > 0) {

            data = malloc((size_t)pkt->size);

            if (data) {
                memcpy(data, pkt->data, (size_t)pkt->size);
                len = pkt->size;
            }
        }

        av_packet_unref(pkt);
    }

    av_packet_free(&pkt);
    avcodec_free_context(&ctx);

    if (!data) {
        return ret == AVERROR_EOF || ret == AVERROR(EAGAIN) ? AVERROR(EIO) : ret;
    }

    *out_data = data;
    *out_len = len;

    return 0;

}

int transcode_pause_frame(transcode_handle_t *handle, const streamly_pause_card_t *card,
                          uint8_t **out_data, int *out_len) {

    if (!handle || !card || !out_data || !out_len) {
        return AVERROR(EINVAL);
    }

    *out_data = NULL;
    *out_len = 0;

    const transcode_params_t *params = &handle->params;

    AVFrame *base = freeze_pick(handle, card->target_pts_ms);

    // No frame yet (paused before first decode) or unexpected geometry: plain card.
    if (base && (base->format != AV_PIX_FMT_YUV420P ||
                 base->width != params->width || base->height != params->height)) {
        av_frame_free(&base);
    }

    if (!base) {
        base = alloc_black_frame(params->width, params->height);
    }

    if (!base) {
        return AVERROR(ENOMEM);
    }

    AVFrame *filtered = NULL;

    int ret = run_pause_card_filter(params, card, base, &filtered);

    if (ret >= 0) {
        ret = encode_pause_idr(params, filtered, out_data, out_len);
    }

    av_frame_free(&filtered);
    av_frame_free(&base);

    return ret;

}

void transcode_buffer_free(uint8_t *data) {

    free(data);

}

static char *dup_opt(const char *value) {

    if (!value || value[0] == '\0') {
        return NULL;
    }

    return strdup(value);
}

transcode_handle_t *transcode_start(const transcode_params_t *params) {

    struct transcode_handle *handle = calloc(1, sizeof(*handle));

    if (!handle) {
        return NULL;
    }

    handle->params = *params;
    handle->params.subtitle_path = dup_opt(params->subtitle_path);
    handle->params.fonts_dir = dup_opt(params->fonts_dir);
    handle->params.cta_font_path = dup_opt(params->cta_font_path);
    handle->params.input_url = dup_opt(params->input_url);
    handle->params.headers = dup_opt(params->headers);
    handle->exit_code = 0;
    handle->error[0] = '\0';
    pthread_mutex_init(&handle->freeze_lock, NULL);

    if (pthread_create(&handle->thread, NULL, transcode_thread, handle) != 0) {
        transcode_free(handle);
        return NULL;
    }

    return handle;
}

int transcode_join(transcode_handle_t *handle) {

    if (!handle) {
        return EINVAL;
    }

    pthread_join(handle->thread, NULL);

    return handle->exit_code;
}

void transcode_error(transcode_handle_t *handle, char *buf, int buf_size) {

    if (!handle || !buf || buf_size <= 0) {
        return;
    }

    snprintf(buf, (size_t)buf_size, "%s", handle->error);
}

void transcode_free(transcode_handle_t *handle) {

    if (!handle) {
        return;
    }

    for (int i = 0; i < FREEZE_RING_SLOTS; i++) {
        av_frame_free(&handle->freeze_ring[i]);
    }

    pthread_mutex_destroy(&handle->freeze_lock);

    free((void *)handle->params.subtitle_path);
    free((void *)handle->params.fonts_dir);
    free((void *)handle->params.cta_font_path);
    free((void *)handle->params.input_url);
    free((void *)handle->params.headers);
    free(handle);
}
