//go:build cgo

package libdc

/*
#cgo CXXFLAGS: -std=c++17 -I${SRCDIR}/native -I${SRCDIR}/../../third_party/libdatachannel/include -DRTC_ENABLE_MEDIA -DRTC_STATIC
#cgo LDFLAGS: -L${SRCDIR}/../../third_party/libdatachannel/build -L${SRCDIR}/../../third_party/libdatachannel/build/deps/libjuice -L${SRCDIR}/../../third_party/libdatachannel/build/deps/libsrtp -L${SRCDIR}/../../third_party/libdatachannel/build/deps/usrsctp/usrsctplib -ldatachannel-static -ljuice -lsrtp2 -lusrsctp -lssl -lcrypto -lstdc++ -lpthread
#cgo windows LDFLAGS: -lws2_32 -liphlpapi -lbcrypt

#include <stdlib.h>
#include "native/streamly_dc.h"

extern void streamlyDescriptionTrampoline(uint64_t user, char *sdp, int type);
*/
import "C"

import (
	"errors"
	"sync"
	"unsafe"
)

type DescriptionHandler func(sdp string, offer bool)

type Peer struct {

	id uint64
	handle *C.StreamlyPeer

	onDesc DescriptionHandler

	audioClock uint32
	videoClock uint32

	mu sync.Mutex
	alive bool

}

func NewPeer(stunURL string) (*Peer, error) {

	cStun := C.CString(stunURL)
	defer C.free(unsafe.Pointer(cStun))

	handle := C.streamly_peer_create(cStun)

	if handle == nil {

		return nil, errors.New("libdatachannel peer create failed")
	}

	peer := &Peer{handle: handle, audioClock: 48000, videoClock: 90000, alive: true}
	id := registerPeer(peer)

	C.streamly_peer_on_local_description(handle, (C.StreamlyDescriptionCallback)(C.streamlyDescriptionTrampoline), C.uint64_t(id))

	return peer, nil

}

func (p *Peer) Destroy() {

	if p == nil {

		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.alive = false
	p.onDesc = nil

	if p.handle != nil {

		C.streamly_peer_destroy(p.handle)
		p.handle = nil

	}

	if p.id != 0 {

		unregisterPeer(p.id)
		p.id = 0

	}

}

func (p *Peer) OnLocalDescription(handler DescriptionHandler) {

	p.onDesc = handler

}

func (p *Peer) AddAudioTrack(ssrc uint32, payloadType int) error {

	if C.streamly_peer_add_audio(p.handle, C.uint32_t(ssrc), C.int(payloadType)) == 0 {

		return errors.New("libdatachannel audio track add failed")

	}

	return nil

}

func (p *Peer) AddVideoTrack(ssrc, rtxSSRC uint32, payloadType, rtxPayloadType int) error {

	if C.streamly_peer_add_video(p.handle, C.uint32_t(ssrc), C.uint32_t(rtxSSRC), C.int(payloadType), C.int(rtxPayloadType)) == 0 {

		return errors.New("libdatachannel video track add failed")

	}

	return nil

}

func (p *Peer) CreateOffer() { C.streamly_peer_create_offer(p.handle) }

func (p *Peer) SetRemoteAnswer(sdp string) error {

	cSDP := C.CString(sdp)
	defer C.free(unsafe.Pointer(cSDP))

	if C.streamly_peer_set_remote_answer(p.handle, cSDP) == 0 {

		return errors.New("libdatachannel set remote answer failed")

	}

	return nil

}

func (p *Peer) SetupPacketizers(audioSSRC uint32, audioPT int, videoSSRC uint32, videoPT int) error {

	if C.streamly_peer_setup_audio_packetizer(p.handle, C.uint32_t(audioSSRC), C.int(audioPT), 2, 8) == 0 {

		return errors.New("libdatachannel audio packetizer setup failed")

	}

	if C.streamly_peer_setup_video_packetizer(p.handle, C.uint32_t(videoSSRC), C.int(videoPT), 0, 10) == 0 {

		return errors.New("libdatachannel video packetizer setup failed")

	}

	return nil

}

func (p *Peer) Connected() bool {

	return C.streamly_peer_connected(p.handle) != 0

}

func (p *Peer) AudioOpen() bool {

	return C.streamly_peer_audio_open(p.handle) != 0

}

func (p *Peer) VideoOpen() bool {

	return C.streamly_peer_video_open(p.handle) != 0

}

func (p *Peer) MediaReady() bool {

	return C.streamly_peer_media_ready(p.handle) != 0

}

func (p *Peer) SendAudio(data []byte, durationMs float64) {

	if len(data) == 0 {

		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.alive || p.handle == nil {

		return
	}

	if C.streamly_peer_send_audio(p.handle, (*C.uint8_t)(unsafe.Pointer(&data[0])), C.size_t(len(data))) == 1 {

		C.streamly_peer_advance_audio_timestamp(p.handle, C.uint32_t(p.audioClock), C.double(durationMs))

	}

}

func (p *Peer) AdvanceAudio(durationMs float64) {

	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.alive || p.handle == nil {

		return

	}

	C.streamly_peer_advance_audio_timestamp(p.handle, C.uint32_t(p.audioClock), C.double(durationMs))

}

func (p *Peer) AdvanceVideo(durationMs float64) {

	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.alive || p.handle == nil {

		return

	}

	C.streamly_peer_advance_video_timestamp(p.handle, C.uint32_t(p.videoClock), C.double(durationMs))

}

func (p *Peer) SendVideo(data []byte, durationMs float64) {

	if len(data) == 0 {

		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.alive || p.handle == nil {

		return

	}

	if C.streamly_peer_send_video(p.handle, (*C.uint8_t)(unsafe.Pointer(&data[0])), C.size_t(len(data))) == 1 {

		C.streamly_peer_advance_video_timestamp(p.handle, C.uint32_t(p.videoClock), C.double(durationMs))

	}

}

//export streamlyDescriptionTrampoline
func streamlyDescriptionTrampoline(id C.uint64_t, sdp *C.char, descType C.int) {

	peer, ok := lookupPeer(uint64(id))

	if !ok || peer.onDesc == nil || sdp == nil {

		return

	}

	peer.onDesc(C.GoString(sdp), int(descType) == int(C.STREAMLY_DESC_OFFER))

}
