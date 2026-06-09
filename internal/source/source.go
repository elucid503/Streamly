package source

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"streamly/internal/config"
)

const sourceChunkBytes = 32 * 1024 // Small writes keep playback responsive while large media chunks are fetched.

// UrlResolver re-resolves a fresh, IP-bound media URL; called again whenever the previous one dies.
type UrlResolver func() (string, error)

// MediaSourceStats are lightweight counters surfaced by /stats.
type MediaSourceStats struct {
	BytesRead  int64
	DurationMs *int64
	PositionMs int64
}

// MediaSource downloads progressive media in-process and exposes it to the transcoder.
type MediaSource struct {
	Stream io.Reader

	streamWriter *io.PipeWriter

	ctx    context.Context
	cancel context.CancelFunc

	resolve        UrlResolver
	stats          *MediaSourceStats
	progressiveURL string
}

type openReader struct {
	body    io.ReadCloser
	partial bool

	cancel   context.CancelFunc
	timeout  time.Duration
	timedOut atomic.Bool

	timerMu sync.Mutex
	timer   *time.Timer
}

// Create resolves a progressive URL and builds a source that can Range-resume it.
func Create(resolve UrlResolver, headers map[string]string, initialURL string, stats *MediaSourceStats) (*MediaSource, error) {

	if stats == nil {
		stats = &MediaSourceStats{}
	}

	url := initialURL

	if url == "" {

		var err error
		url, err = resolve()

		if err != nil || url == "" {
			return nil, errors.New("could not resolve a media URL")
		}

	}

	if IsHlsURL(url) {
		return nil, fmt.Errorf("HLS URLs are handled directly by libavformat")
	}

	ctx, cancel := context.WithCancel(context.Background())

	source := &MediaSource{
		ctx:            ctx,
		cancel:         cancel,
		resolve:        resolve,
		stats:          stats,
		progressiveURL: url,
	}

	streamReader, streamWriter := io.Pipe()
	source.streamWriter = streamWriter
	source.Stream = newCancelOnCloseReader(streamReader, cancel)
	source.stats.PositionMs = 0
	go source.progressiveLoop(headers)

	return source, nil

}

// Destroy tears the source down so the download loops observe the abort and run their cleanup.
func (m *MediaSource) Destroy() {

	m.cancel()

	if m.streamWriter != nil {
		m.streamWriter.Close()
	}

}

func (m *MediaSource) aborted() bool {

	select {
	case <-m.ctx.Done():
		return true
	default:
		return false
	}

}

func (m *MediaSource) emit(writer *io.PipeWriter, chunk []byte) error {

	for offset := 0; offset < len(chunk); offset += sourceChunkBytes {

		if m.aborted() {
			return m.ctx.Err()
		}

		end := offset + sourceChunkBytes

		if end > len(chunk) {
			end = len(chunk)
		}

		slice := chunk[offset:end]
		m.stats.BytesRead += int64(len(slice))

		if _, err := writer.Write(slice); err != nil {
			return err
		}

	}

	return nil

}

func (m *MediaSource) progressiveLoop(headers map[string]string) {

	writer := m.streamWriter

	defer writer.Close()

	url := m.progressiveURL
	offset := int64(0)
	retries := 0

	for {

		if m.aborted() {
			return
		}

		extra := map[string]string{}

		if offset > 0 {
			extra["Range"] = fmt.Sprintf("bytes=%d-", offset)
		}

		open, err := m.open(url, headers, extra)

		if err != nil {

			if m.aborted() {
				return
			}

			retries++

			if retries > config.Download.MaxRetries {
				writer.CloseWithError(err)

				return
			}

			if fresh, resolveErr := m.resolve(); resolveErr == nil && fresh != "" {
				url = fresh
			}

			continue

		}

		skip := int64(0)

		if !open.partial {
			skip = offset // A server that ignores Range replays from byte 0, so drop what we've already emitted.
		}

		readErr := func() error {

			for {

				if m.aborted() {
					return m.ctx.Err()
				}

				chunk, readErr := open.readChunk()

				if len(chunk) > 0 {

					retries = 0

					if skip > 0 {

						if int64(len(chunk)) <= skip {
							skip -= int64(len(chunk))
							offset += int64(len(chunk))
						} else {

							chunk = chunk[skip:]
							offset += skip
							skip = 0
							offset += int64(len(chunk))

							if err := m.emit(writer, chunk); err != nil {
								return err
							}

						}

					} else {

						offset += int64(len(chunk))

						if err := m.emit(writer, chunk); err != nil {
							return err
						}

					}

				}

				if readErr == io.EOF {
					return nil
				}

				if readErr != nil {
					return readErr
				}

			}

		}()

		open.dispose()
		_ = open.body.Close()

		if readErr == nil {
			return
		}

		if m.aborted() {
			return
		}

		retries++

		if retries > config.Download.MaxRetries {
			writer.CloseWithError(readErr)

			return
		}

		if fresh, resolveErr := m.resolve(); resolveErr == nil && fresh != "" {
			url = fresh
		}

	}

}

func (m *MediaSource) open(url string, headers, extra map[string]string) (*openReader, error) {

	reqCtx, cancel := context.WithCancel(m.ctx)

	go func() {

		<-m.ctx.Done()
		cancel()

	}()

	reader := &openReader{
		cancel:  cancel,
		timeout: time.Duration(config.Download.RequestTimeoutMs) * time.Millisecond,
	}

	reader.arm()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)

	if err != nil {
		reader.dispose()

		return nil, err
	}

	for key, value := range headers {
		req.Header.Set(key, value)
	}

	for key, value := range extra {
		req.Header.Set(key, value)
	}

	response, err := http.DefaultClient.Do(req)

	reader.disarm()

	if err != nil {
		reader.dispose()

		return nil, err
	}

	if (response.StatusCode < 200 || response.StatusCode >= 300) && response.StatusCode != http.StatusPartialContent {

		_ = response.Body.Close()
		reader.dispose()

		return nil, fmt.Errorf("HTTP %d for %s", response.StatusCode, url)

	}

	if response.Body == nil {

		reader.dispose()

		return nil, fmt.Errorf("HTTP %d for %s", response.StatusCode, url)

	}

	reader.body = response.Body
	reader.partial = response.StatusCode == http.StatusPartialContent

	return reader, nil

}

func (o *openReader) arm() {

	o.timerMu.Lock()
	defer o.timerMu.Unlock()

	if o.timer != nil {
		o.timer.Stop()
	}

	o.timer = time.AfterFunc(o.timeout, func() {
		o.timedOut.Store(true)
		o.cancel()
	})

}

func (o *openReader) disarm() {

	o.timerMu.Lock()
	defer o.timerMu.Unlock()

	if o.timer != nil {
		o.timer.Stop()
		o.timer = nil
	}

}

func (o *openReader) dispose() {

	o.disarm()

}

func (o *openReader) readChunk() ([]byte, error) {

	o.arm()

	buffer := make([]byte, sourceChunkBytes)
	n, err := o.body.Read(buffer)

	o.disarm()

	if err != nil && err != io.EOF {
		return nil, err
	}

	if n == 0 && err == io.EOF {
		return nil, io.EOF
	}

	if o.timedOut.Load() {
		return nil, errors.New("media read timed out")
	}

	return buffer[:n], err

}

type cancelOnCloseReader struct {
	*io.PipeReader
	onClose func()
}

func newCancelOnCloseReader(reader *io.PipeReader, onClose context.CancelFunc) *cancelOnCloseReader {

	return &cancelOnCloseReader{
		PipeReader: reader,
		onClose:    onClose,
	}

}

func (r *cancelOnCloseReader) Close() error {

	r.onClose()

	return r.PipeReader.Close()

}
