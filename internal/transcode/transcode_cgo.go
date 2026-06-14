//go:build cgo

package transcode

/*
#cgo pkg-config: libavformat libavcodec libavfilter libavutil libswresample
#cgo LDFLAGS: -lpthread

#include <stdlib.h>
#include <stdbool.h>
#include <stdint.h>

#include "transcode_c.h"

extern void streamlyTranscodeEmit(uintptr_t user, int kind, uint8_t *data, int len, int64_t pts_ms, int64_t dur_ms);
extern int streamlyInputRead(uintptr_t user, uint8_t *buf, int len);
extern int64_t streamlyInputSeek(uintptr_t user, int64_t offset, int whence);
extern void streamlyTranscodeMeta(uintptr_t user, int64_t duration_ms);
extern void streamly_fill_ctas(uintptr_t user, transcode_params_t *params);
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
	"unsafe"

	"streamly/internal/config"
)

const videoPacketChannelCap = 120
const audioPacketChannelCap = 200

const videoPacketChannelCapLive = 150
const audioPacketChannelCapLive = 250

type emitTarget struct {

	ctx context.Context
	pause *pauseState

	video chan<- Packet
	audio chan<- Packet

	input InputReader
	onDuration func(durationMs int64)
	probedDuration int64
	supplyCTAs func(probedDurationMs int64, startMs int64) (fontPath string, windows []CTAWindow)

}

var (
	emitMu sync.Mutex
	emitTargets = map[uintptr]*emitTarget{}
	emitNextID uintptr = 1
)

func registerEmitTarget(target *emitTarget) uintptr {

	emitMu.Lock()
	defer emitMu.Unlock()

	id := emitNextID
	emitNextID++
	emitTargets[id] = target

	return id

}

func emitTargetByID(id uintptr) *emitTarget {

	emitMu.Lock()
	defer emitMu.Unlock()

	return emitTargets[id]

}

func unregisterEmitTarget(id uintptr) {

	emitMu.Lock()
	defer emitMu.Unlock()

	delete(emitTargets, id)

}

//export streamlyTranscodeEmit
func streamlyTranscodeEmit(user C.uintptr_t, kind C.int, data *C.uint8_t, length C.int, ptsMs C.int64_t, durMs C.int64_t) {

	target := emitTargetByID(uintptr(user))

	if target == nil {

		return

	}

	n := int(length)
	payload := C.GoBytes(unsafe.Pointer(data), C.int(n))

	packet := Packet{

		Kind: KindVideo,
		Data: payload,

		PTS: time.Duration(int64(ptsMs)) * time.Millisecond,
		Duration: time.Duration(int64(durMs)) * time.Millisecond,

	}

	channel := target.video

	if kind == C.STREAMLY_KIND_AUDIO {

		packet.Kind = KindAudio
		channel = target.audio

		if packet.Duration <= 0 {

			packet.Duration = opusPacketDuration(payload)

		}

	}

	if !target.pause.Wait(target.ctx) {

		return

	}

	select {

	case channel <- packet:
	case <-target.ctx.Done():

	}

}

//export streamlyInputRead
func streamlyInputRead(user C.uintptr_t, buf *C.uint8_t, size C.int) C.int {

	target := emitTargetByID(uintptr(user))

	if target == nil || target.input == nil || size <= 0 {

		return 0

	}

	out := unsafe.Slice((*byte)(buf), int(size))

	for {

		if target.ctx.Err() != nil {

			return 0

		}

		n, err := target.input.Read(out)

		if n > 0 {

			return C.int(n)

		}

		if err == io.EOF {

			return 0

		}

		if err != nil {

			return -1

		}

	}

}

//export streamlyInputSeek
func streamlyInputSeek(user C.uintptr_t, offset C.int64_t, whence C.int) C.int64_t {

	target := emitTargetByID(uintptr(user))

	if target == nil || target.input == nil {

		return -1

	}

	if whence == C.STREAMLY_SEEK_SIZE {

		return C.int64_t(target.input.Size())

	}

	position, err := target.input.Seek(int64(offset), int(whence))

	if err != nil {

		return -1

	}

	return C.int64_t(position)

}

//export streamlyTranscodeMeta
func streamlyTranscodeMeta(user C.uintptr_t, durationMs C.int64_t) {

	target := emitTargetByID(uintptr(user))

	if target == nil {

		return

	}

	target.probedDuration = int64(durationMs)

	if durationMs > 0 && target.onDuration != nil {

		target.onDuration(int64(durationMs))

	}

}

//export streamly_fill_ctas
func streamly_fill_ctas(user C.uintptr_t, params *C.transcode_params_t) {

	target := emitTargetByID(uintptr(user))

	if target == nil || target.supplyCTAs == nil || params == nil {

		return

	}

	fontPath, windows := target.supplyCTAs(target.probedDuration, int64(params.start_ms))

	if fontPath != "" {

		if params.cta_font_path != nil {

			C.free(unsafe.Pointer(params.cta_font_path))

		}

		params.cta_font_path = C.CString(fontPath)

	}

	fillCTAWindows(params, windows)

}

func fillCTAWindows(params *C.transcode_params_t, windows []CTAWindow) {

	ctaCount := 0

	for _, window := range windows {

		if ctaCount >= int(C.STREAMLY_MAX_CTA) || window.Text == "" {

			continue

		}

		copyCTAText(&params.ctas[ctaCount].text[0], truncateCTAText(window.Text, ctaTextLimit))

		params.ctas[ctaCount].start_ms = C.int64_t(window.StartMs)
		params.ctas[ctaCount].end_ms = C.int64_t(window.EndMs)
		ctaCount++

	}

	params.cta_count = C.int(ctaCount)

}

type nativeState struct {

	mu sync.Mutex
	handle *C.transcode_handle_t

}

func (n *nativeState) release() *C.transcode_handle_t {

	n.mu.Lock()
	defer n.mu.Unlock()

	handle := n.handle
	n.handle = nil

	return handle

}

func (n *nativeState) encodePauseFrame(card *PauseCard, targetPTSMs int64) ([]byte, error) {

	n.mu.Lock()
	defer n.mu.Unlock()

	if n.handle == nil {

		return nil, fmt.Errorf("transcode already finished")

	}

	cCard := C.streamly_pause_card_t{

		font_path: C.CString(card.FontPath),
		title: C.CString(card.Title),
		subtitle: C.CString(card.Subtitle),
		cta: C.CString(card.CTA),

		target_pts_ms: C.int64_t(targetPTSMs),

	}

	defer freeCString(cCard.font_path)
	defer freeCString(cCard.title)
	defer freeCString(cCard.subtitle)
	defer freeCString(cCard.cta)

	bodyCount := 0

	for _, line := range card.BodyLines {

		if bodyCount >= int(C.STREAMLY_PAUSE_BODY_LINES) || line == "" {

			continue

		}

		cCard.body[bodyCount] = C.CString(line)
		bodyCount++

	}

	cCard.body_count = C.int(bodyCount)

	defer func() {

		for i := 0; i < bodyCount; i++ {

			freeCString(cCard.body[i])

		}

	}()

	var data *C.uint8_t
	var length C.int

	if code := C.transcode_pause_frame(n.handle, &cCard, &data, &length); code != 0 {

		return nil, fmt.Errorf("pause frame encode failed (%d)", int(code))

	}

	frame := C.GoBytes(unsafe.Pointer(data), length)
	C.transcode_buffer_free(data)

	return frame, nil

}

func startNative(request Request) (*Session, error) {

	videoCap := videoPacketChannelCap
	audioCap := audioPacketChannelCap

	if request.Live {

		videoCap = videoPacketChannelCapLive
		audioCap = audioPacketChannelCapLive

	}

	video := make(chan Packet, videoCap)
	audio := make(chan Packet, audioCap)
	done := make(chan error, 1)

	pause := newPauseState()

	target := &emitTarget{

		ctx: request.Context,
		pause: pause,

		video: video,
		audio: audio,

		input: request.Source,
		onDuration: request.OnDuration,
		supplyCTAs: request.SupplyCTAs,

	}

	id := registerEmitTarget(target)

	abortFlag := (*C.bool)(C.malloc(C.size_t(unsafe.Sizeof(C.bool(false)))))

	if abortFlag == nil {

		unregisterEmitTarget(id)

		return nil, fmt.Errorf("failed to allocate abort flag")

	}

	*abortFlag = C.bool(false)

	var abortMu sync.Mutex

	setAbort := func() {

		abortMu.Lock()
		defer abortMu.Unlock()

		if abortFlag != nil {

			*abortFlag = C.bool(true)

		}

	}

	go func() {

		<-request.Context.Done()
		setAbort()

	}()

	var inputURLCString, headersCString, subtitleCString, fontsCString, ctaFontCString *C.char

	if request.SubtitlePath != "" {

		subtitleCString = C.CString(request.SubtitlePath)

	}

	if request.FontsDir != "" {

		fontsCString = C.CString(request.FontsDir)

	}

	if request.CTAFontPath != "" {

		ctaFontCString = C.CString(request.CTAFontPath)

	}

	if request.InputURL != "" {

		inputURLCString = C.CString(request.InputURL)
		headersCString = C.CString(formatHTTPHeaders(request.Headers))

	}

	params := C.transcode_params_t{

		width: C.int(config.Stream.Width),
		height: C.int(config.Stream.Height),
		frame_rate: C.int(config.Stream.FrameRate),
		bitrate_video_k: C.int(config.Stream.BitrateVideo),
		bitrate_video_max_k: C.int(config.Stream.BitrateVideoMax),
		bitrate_audio_k: C.int(config.Stream.BitrateAudio),
		threads: C.int(config.Stream.Threads),

		subtitle_path: subtitleCString,
		fonts_dir: fontsCString,
		cta_font_path: ctaFontCString,

		input_url: inputURLCString,
		headers: headersCString,
		start_ms: C.int64_t(request.Start.Milliseconds()),
		live: C.bool(request.Live),

		emit: C.streamly_emit_cb(C.streamlyTranscodeEmit),
		meta_cb: C.streamly_meta_cb(C.streamlyTranscodeMeta),
		emit_user: C.uintptr_t(id),
		abort_flag: abortFlag,

	}

	fillCTAWindows(&params, request.CTAs)

	if request.Source != nil {

		params.read_cb = C.streamly_read_cb(C.streamlyInputRead)
		params.seek_cb = C.streamly_seek_cb(C.streamlyInputSeek)

	}

	handle := C.transcode_start(&params)

	freeCString(subtitleCString)
	freeCString(fontsCString)
	freeCString(ctaFontCString)
	freeCString(inputURLCString)
	freeCString(headersCString)

	if handle == nil {

		setAbort()
		unregisterEmitTarget(id)
		abortMu.Lock()
		C.free(unsafe.Pointer(abortFlag))
		abortFlag = nil
		abortMu.Unlock()

		return nil, fmt.Errorf("failed to start libav transcode")

	}

	native := &nativeState{handle: handle}

	go func() {

		exitCode := C.transcode_join(handle)

		close(video)
		close(audio)
		unregisterEmitTarget(id)

		var doneErr error

		if request.Context.Err() != nil {

			doneErr = request.Context.Err()

		} else if exitCode != 0 {

			doneErr = transcodeError(handle, int(exitCode))

		}

		// Waits for any in-flight pause-card encode before the handle is freed.
		native.release()

		C.transcode_free(handle)
		trimNativeHeap()

		abortMu.Lock()
		C.free(unsafe.Pointer(abortFlag))
		abortFlag = nil
		abortMu.Unlock()

		done <- doneErr

	}()

	session := &Session{

		Video: video,
		Audio: audio,
		Done: done,
		pause: pause,

	}

	if request.PauseCard != nil {

		session.card = request.PauseCard
		session.encoder = native

	}

	return session, nil

}

func formatHTTPHeaders(headers map[string]string) string {

	if len(headers) == 0 {

		return ""

	}

	lines := make([]string, 0, len(headers))

	for key, value := range headers {

		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)

		if key == "" || value == "" {

			continue

		}

		lines = append(lines, key+": "+value)

	}

	if len(lines) == 0 {

		return ""

	}

	return strings.Join(lines, "\r\n") + "\r\n"

}

func transcodeError(handle *C.transcode_handle_t, exitCode int) error {

	errBuf := make([]byte, 2048)
	C.transcode_error(handle, (*C.char)(unsafe.Pointer(&errBuf[0])), C.int(len(errBuf)))

	msg := string(errBuf)

	if idx := indexZero(errBuf); idx >= 0 {

		msg = string(errBuf[:idx])

	}

	if msg == "" {

		msg = fmt.Sprintf("libav transcode failed (%d)", exitCode)

	}

	return errors.New(msg)

}

func freeCString(value *C.char) {

	if value != nil {

		C.free(unsafe.Pointer(value))

	}

}

// ctaTextLimit matches the C drawtext buffer size minus the required NUL terminator.
const ctaTextLimit = 191

func copyCTAText(dest *C.char, text string) {

	text = truncateCTAText(text, ctaTextLimit)

	out := unsafe.Slice((*byte)(unsafe.Pointer(dest)), len(text)+1)
	copy(out, text)
	out[len(text)] = 0

}

// truncateCTAText avoids splitting UTF-8 runes, which would render replacement glyphs in drawtext.
func truncateCTAText(text string, max int) string {

	if len(text) <= max {

		return text

	}

	cut := max

	for cut > 0 && !utf8.RuneStart(text[cut]) {

		cut--

	}

	return text[:cut]

}

func indexZero(buf []byte) int {

	for i, b := range buf {

		if b == 0 {

			return i

		}

	}

	return -1

}
