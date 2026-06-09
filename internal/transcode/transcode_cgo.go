//go:build cgo

// Package transcode re-encodes media with libav via CGO (no ffmpeg binary).
//
// Required development packages (Debian/Ubuntu names):
//   - libavformat-dev
//   - libavcodec-dev
//   - libavfilter-dev
//   - libavutil-dev
//   - libswresample-dev
//   - libx264-dev
package transcode

/*
#cgo pkg-config: libavformat libavcodec libavfilter libavutil libswresample
#cgo LDFLAGS: -lpthread

#include <stdlib.h>
#include <stdbool.h>
#include <stdint.h>

#include "transcode_c.h"

extern void streamlyTranscodeEmit(uintptr_t user, int kind, uint8_t *data, int len, int64_t pts_ms, int64_t dur_ms);
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"unsafe"

	"streamly/internal/config"
)

const inputBufferBytes = 64 * 1024 // Small bot-side jitter buffer without starving libav startup.

const videoPacketChannelCap = 180 // About 6 seconds at 30 fps; enough for encoder jitter without runaway memory.
const audioPacketChannelCap = 400 // About 8 seconds of 20 ms Opus, enough for HLS jitter without hiding pipeline drift.

// emitTarget is the live destination for one transcode's encoded packets.
type emitTarget struct {
	ctx   context.Context
	pause *pauseState
	video chan<- Packet
	audio chan<- Packet
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

	for {

		if !target.pause.Wait(target.ctx) {
			return
		}

		select {
		case channel <- packet:
			return
		case <-target.ctx.Done():
			return
		default:
			time.Sleep(5 * time.Millisecond)
		}

	}

}

func startNative(request Request) (*Session, error) {

	var videoReader, videoWriter *os.File
	var err error

	if request.InputURL == "" {

		videoReader, videoWriter, err = os.Pipe()

		if err != nil {
			return nil, err
		}

	}

	video := make(chan Packet, videoPacketChannelCap)
	audio := make(chan Packet, audioPacketChannelCap)
	done := make(chan error, 1)

	pause := newPauseState()

	feedCtx, feedCancel := context.WithCancel(request.Context)

	if request.InputURL == "" {

		go feedInput(feedCtx, pause, request.Source, videoWriter)

	}

	target := &emitTarget{ctx: request.Context, pause: pause, video: video, audio: audio}
	id := registerEmitTarget(target)

	abortFlag := (*C.bool)(C.malloc(C.size_t(unsafe.Sizeof(C.bool(false)))))

	if abortFlag == nil {
		feedCancel()
		unregisterEmitTarget(id)
		closePipes(videoReader, videoWriter)

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
		feedCancel()

	}()

	overlay := overlayAvailable()
	captionFile := ""

	if overlay && request.Caption != "" {

		file, fileErr := os.CreateTemp("", "streamly-caption-"+request.Key+"-*.txt")

		if fileErr == nil {

			if _, writeErr := file.WriteString(request.Caption); writeErr == nil {
				captionFile = file.Name()
			}

			file.Close()

		}

	}

	var captionCString, logoCString, fontCString, inputURLCString, headersCString *C.char

	if captionFile != "" {
		captionCString = C.CString(captionFile)
	}

	if overlay {
		logoCString = C.CString(config.Overlay.LogoPath)
		fontCString = C.CString(config.Overlay.FontPath)
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
		overlay:             C.bool(overlay && captionFile != ""),
		logo_path:           logoCString,
		font_path:           fontCString,
		caption_file:        captionCString,
		logo_width:          C.int(config.Overlay.LogoWidth),
		font_size:           C.int(config.Overlay.FontSize),
		opacity:             C.float(config.Overlay.Opacity),
		margin:              C.int(config.Overlay.Margin),
		video_fd:            -1,
		input_url:           inputURLCString,
		headers:             headersCString,
		emit:                C.streamly_emit_cb(C.streamlyTranscodeEmit),
		emit_user:           C.uintptr_t(id),
		abort_flag:          abortFlag,
	}

	if videoReader != nil {
		params.video_fd = C.int(videoReader.Fd())
	}

	handle := C.transcode_start(&params)

	freeCString(captionCString)
	freeCString(logoCString)
	freeCString(fontCString)
	freeCString(inputURLCString)
	freeCString(headersCString)

	if handle == nil {
		setAbort()
		feedCancel()
		unregisterEmitTarget(id)
		abortMu.Lock()
		C.free(unsafe.Pointer(abortFlag))
		abortFlag = nil
		abortMu.Unlock()
		closePipes(videoReader, videoWriter)

		if captionFile != "" {
			_ = os.Remove(captionFile)
		}

		return nil, fmt.Errorf("failed to start libav transcode")
	}

	go func() {

		exitCode := C.transcode_join(handle)

		feedCancel()

		if videoWriter != nil {
			videoWriter.Close()
		}

		if videoReader != nil {
			videoReader.Close()
		}

		close(video)
		close(audio)
		unregisterEmitTarget(id)

		if captionFile != "" {
			_ = os.Remove(captionFile)
		}

		var doneErr error

		if request.Context.Err() != nil {
			doneErr = request.Context.Err()
		} else if exitCode != 0 {
			doneErr = transcodeError(handle, int(exitCode))
		}

		C.transcode_free(handle)

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

func feedInput(ctx context.Context, pause *pauseState, reader io.Reader, writer *os.File) {

	defer writer.Close()

	buffer := make([]byte, inputBufferBytes)

	for {

		if ctx.Err() != nil {
			return
		}

		if !pause.Wait(ctx) {
			return
		}

		n, err := reader.Read(buffer)

		if n > 0 {

			if _, writeErr := writer.Write(buffer[:n]); writeErr != nil {
				return
			}

		}

		if err != nil {
			return
		}

	}

}

func freeCString(value *C.char) {

	if value != nil {
		C.free(unsafe.Pointer(value))
	}

}

func closePipes(files ...*os.File) {

	for _, file := range files {
		if file != nil {
			file.Close()
		}
	}

}

func indexZero(buf []byte) int {

	for i, b := range buf {
		if b == 0 {
			return i
		}
	}

	return -1

}
