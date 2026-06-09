#pragma once

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

#define STREAMLY_DESC_OFFER 0
#define STREAMLY_DESC_ANSWER 1

typedef struct StreamlyPeer StreamlyPeer;

typedef void (*StreamlyDescriptionCallback)(uint64_t user, const char *sdp, int type);

StreamlyPeer *streamly_peer_create(const char *stun_url);
void streamly_peer_destroy(StreamlyPeer *peer);
void streamly_peer_close(StreamlyPeer *peer);

void streamly_peer_on_local_description(StreamlyPeer *peer, StreamlyDescriptionCallback cb, uint64_t user);

int streamly_peer_add_audio(StreamlyPeer *peer, uint32_t ssrc, int payload_type);
int streamly_peer_add_video(StreamlyPeer *peer, uint32_t ssrc, uint32_t rtx_ssrc, int payload_type, int rtx_payload_type);

void streamly_peer_create_offer(StreamlyPeer *peer);
int streamly_peer_set_remote_answer(StreamlyPeer *peer, const char *sdp);
int streamly_peer_connected(StreamlyPeer *peer);
int streamly_peer_media_ready(StreamlyPeer *peer);

int streamly_peer_setup_audio_packetizer(StreamlyPeer *peer, uint32_t ssrc, int payload_type, uint16_t playout_min, uint16_t playout_max);
int streamly_peer_setup_video_packetizer(StreamlyPeer *peer, uint32_t ssrc, int payload_type, uint16_t playout_min, uint16_t playout_max);

int streamly_peer_send_audio(StreamlyPeer *peer, const uint8_t *data, size_t len); /** 1 sent, 0 not ready */
int streamly_peer_send_video(StreamlyPeer *peer, const uint8_t *data, size_t len); /** 1 sent, 0 not ready */
void streamly_peer_advance_audio_timestamp(StreamlyPeer *peer, uint32_t clock_rate, double duration_ms);
void streamly_peer_advance_video_timestamp(StreamlyPeer *peer, uint32_t clock_rate, double duration_ms);

int streamly_peer_audio_open(StreamlyPeer *peer);
int streamly_peer_video_open(StreamlyPeer *peer);

#ifdef __cplusplus
}
#endif
