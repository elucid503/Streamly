//go:build cgo

// Package transcode re-encodes media with libav via CGO (no ffmpeg binary).
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
	"unsafe"

	"streamly/internal/config"
)

const videoPacketChannelCap = 120 // About 4 seconds at 30 fps; balances jitter cushion vs Go heap.
const audioPacketChannelCap = 200 // About 4 seconds of 20 ms Opus.

const videoPacketChannelCapLive = 150 // About 5 seconds at 30 fps for live HLS cushion.
const audioPacketChannelCapLive = 250 // About 5 seconds of 20 ms Opus for live HLS cushion.

// emitTarget is the live destination and input for one transcode's callbacks.
type emitTarget struct {
	ctx        context.Context
	pause      *pauseState
	video      chan<- Packet
	audio      chan<- Packet
	input      InputReader
	onDuration     func(durationMs int64)
	probedDuration int64
	supplyCTAs     func(probedDurationMs int64, startMs int64) (fontPath string, windows []CTAWindow)
}

var (
	emitMu      sync.Mutex
	emitTargets         = map[uintptr]*emitTarget{}
	emitNextID  uintptr = 1
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
		Kind:     KindVideo,
		Data:     payload,
		PTS:      time.Duration(int64(ptsMs)) * time.Millisecond,
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

	ctaCount := 0

	for _, window := range windows {

		if ctaCount >= int(C.STREAMLY_MAX_CTA) || window.Text == "" {
			continue
		}

		copyCTAText(&params.ctas[ctaCount].text[0], truncateCTAText(window.Text, 191))

		params.ctas[ctaCount].start_ms = C.int64_t(window.StartMs)
		params.ctas[ctaCount].end_ms = C.int64_t(window.EndMs)
		ctaCount++

	}

	params.cta_count = C.int(ctaCount)

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
		ctx:        request.Context,
		pause:      pause,
		video:      video,
		audio:      audio,
		input:      request.Source,
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
		width:               C.int(config.Stream.Width),
		height:              C.int(config.Stream.Height),
		frame_rate:          C.int(config.Stream.FrameRate),
		bitrate_video_k:     C.int(config.Stream.BitrateVideo),
		bitrate_video_max_k: C.int(config.Stream.BitrateVideoMax),
		bitrate_audio_k:     C.int(config.Stream.BitrateAudio),
		threads:             C.int(config.Stream.Threads),
		subtitle_path:       subtitleCString,
		fonts_dir:           fontsCString,
		cta_font_path:       ctaFontCString,
		input_url:           inputURLCString,
		headers:             headersCString,
		start_ms:            C.int64_t(request.Start.Milliseconds()),
		live:                C.bool(request.Live),
		emit:                C.streamly_emit_cb(C.streamlyTranscodeEmit),
		meta_cb:             C.streamly_meta_cb(C.streamlyTranscodeMeta),
		emit_user:           C.uintptr_t(id),
		abort_flag:          abortFlag,
	}

	ctaCount := 0

	for _, window := range request.CTAs {

		if ctaCount >= int(C.STREAMLY_MAX_CTA) || window.Text == "" {
			continue
		}

		copyCTAText(&params.ctas[ctaCount].text[0], truncateCTAText(window.Text, 191))

		params.ctas[ctaCount].start_ms = C.int64_t(window.StartMs)
		params.ctas[ctaCount].end_ms = C.int64_t(window.EndMs)
		ctaCount++

	}

	params.cta_count = C.int(ctaCount)

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

		C.transcode_free(handle)
		trimNativeHeap()

		abortMu.Lock()
		C.free(unsafe.Pointer(abortFlag))
		abortFlag = nil
		abortMu.Unlock()

		done <- doneErr

	}()

	return &Session{
		Video: video,
		Audio: audio,
		Done:  done,
		pause: pause,
	}, nil

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

func copyCTAText(dest *C.char, text string) {

	limit := len(text)

	if limit > 191 {
		limit = 191
	}

	for index := 0; index < limit; index++ {
		*(*C.char)(unsafe.Pointer(uintptr(unsafe.Pointer(dest)) + uintptr(index))) = C.char(text[index])
	}

	*(*C.char)(unsafe.Pointer(uintptr(unsafe.Pointer(dest)) + uintptr(limit))) = 0

}

func truncateCTAText(text string, max int) string {

	if len(text) <= max {
		return text
	}

	return text[:max]

}

func indexZero(buf []byte) int {

	for i, b := range buf {
		if b == 0 {
			return i
		}
	}

	return -1

}
