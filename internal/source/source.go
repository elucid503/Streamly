package source

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"streamly/internal/config"
)

const readChunkBytes = 32 * 1024

// UrlResolver re-resolves a fresh, IP-bound media URL; called again whenever the previous one dies.
type UrlResolver func() (string, error)

// MediaSourceStats are lightweight counters surfaced by /stats.
type MediaSourceStats struct {
	BytesRead  int64
	DurationMs *int64
	PositionMs int64
}

// MediaSource exposes a progressive HTTP download as a byte-seekable reader. libavformat
// pulls from it directly, so it can probe trailing indexes and honor /seek with one
// Range request instead of re-downloading from the start.
//
// Reads and seeks come from libav's single demuxer thread; only Destroy runs concurrently.
type MediaSource struct {
	ctx    context.Context
	cancel context.CancelFunc

	resolve UrlResolver
	headers map[string]string
	stats   *MediaSourceStats
	timeout time.Duration

	url    string
	offset int64
	size   int64 // -1 until the first response reveals it.

	bodyMu sync.Mutex
	body   io.ReadCloser
}

// Create resolves a progressive URL and builds a seekable source over it.
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

	return &MediaSource{
		ctx:     ctx,
		cancel:  cancel,
		resolve: resolve,
		headers: headers,
		stats:   stats,
		timeout: time.Duration(config.Download.RequestTimeoutMs) * time.Millisecond,
		url:     url,
		size:    -1,
	}, nil

}

// Destroy tears the source down and unblocks any in-flight network read.
func (m *MediaSource) Destroy() {

	m.cancel()
	m.closeBody()

}

func (m *MediaSource) Read(p []byte) (int, error) {

	retries := 0

	for {

		if err := m.ctx.Err(); err != nil {
			return 0, err
		}

		if err := m.ensureBody(); err != nil {

			if m.ctx.Err() != nil {
				return 0, m.ctx.Err()
			}

			retries++

			if retries > config.Download.MaxRetries {
				return 0, err
			}

			m.refreshURL()

			continue

		}

		n, err := m.readBody(p)

		if n > 0 {
			m.offset += int64(n)
			m.stats.BytesRead += int64(n)

			return n, nil
		}

		if err == io.EOF {

			if m.size >= 0 && m.offset < m.size {

				// The server closed the connection early; resume at the current offset.
				m.closeBody()
				retries++

				if retries > config.Download.MaxRetries {
					return 0, io.ErrUnexpectedEOF
				}

				m.refreshURL()

				continue

			}

			return 0, io.EOF

		}

		if err != nil {

			m.closeBody()

			if m.ctx.Err() != nil {
				return 0, m.ctx.Err()
			}

			retries++

			if retries > config.Download.MaxRetries {
				return 0, err
			}

			m.refreshURL()

		}

	}

}

// Seek repositions the source by bytes; the next Read issues a Range request from there.
func (m *MediaSource) Seek(offset int64, whence int) (int64, error) {

	var target int64

	switch whence {
	case io.SeekStart:
		target = offset
	case io.SeekCurrent:
		target = m.offset + offset
	case io.SeekEnd:

		if m.Size() < 0 {
			return 0, errors.New("media size unknown")
		}

		target = m.size + offset

	default:
		return 0, fmt.Errorf("unsupported whence %d", whence)
	}

	if target < 0 {
		return 0, errors.New("negative seek position")
	}

	if target != m.offset {
		m.closeBody()
		m.offset = target
	}

	return target, nil

}

// Size returns the media size in bytes, or -1 when unknown. The first request reveals it.
func (m *MediaSource) Size() int64 {

	if m.size < 0 {
		_ = m.ensureBody()
	}

	return m.size

}

func (m *MediaSource) refreshURL() {

	if m.resolve == nil {
		return
	}

	if fresh, err := m.resolve(); err == nil && fresh != "" {
		m.url = fresh
	}

}

func (m *MediaSource) currentBody() io.ReadCloser {

	m.bodyMu.Lock()
	defer m.bodyMu.Unlock()

	return m.body

}

func (m *MediaSource) setBody(body io.ReadCloser) {

	m.bodyMu.Lock()
	defer m.bodyMu.Unlock()

	m.body = body

}

func (m *MediaSource) closeBody() {

	m.bodyMu.Lock()
	defer m.bodyMu.Unlock()

	if m.body != nil {
		m.body.Close()
		m.body = nil
	}

}

func (m *MediaSource) ensureBody() error {

	if m.currentBody() != nil {
		return nil
	}

	return m.open()

}

// readBody reads from the open body under a watchdog that closes it on stall,
// which unblocks the read and lets the retry path reopen at the current offset.
func (m *MediaSource) readBody(p []byte) (int, error) {

	body := m.currentBody()

	if body == nil {
		return 0, errors.New("media connection closed")
	}

	watchdog := time.AfterFunc(m.timeout, func() { body.Close() })
	defer watchdog.Stop()

	return body.Read(p)

}

// httpBody ties the response body to its request context so closing cancels both.
type httpBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *httpBody) Close() error {

	b.cancel()

	return b.ReadCloser.Close()

}

func (m *MediaSource) open() error {

	reqCtx, cancelReq := context.WithCancel(m.ctx)

	watchdog := time.AfterFunc(m.timeout, cancelReq)

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, m.url, nil)

	if err != nil {
		watchdog.Stop()
		cancelReq()

		return err
	}

	for key, value := range m.headers {
		req.Header.Set(key, value)
	}

	if m.offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", m.offset))
	}

	response, err := http.DefaultClient.Do(req)

	watchdog.Stop()

	if err != nil {
		cancelReq()

		return err
	}

	if (response.StatusCode < 200 || response.StatusCode >= 300) && response.StatusCode != http.StatusPartialContent {

		_ = response.Body.Close()
		cancelReq()

		return fmt.Errorf("HTTP %d for %s", response.StatusCode, m.url)

	}

	m.noteSize(response)

	if response.StatusCode == http.StatusOK && m.offset > 0 {

		// The server ignored Range and replayed from byte 0; drop the prefix.
		if err := m.discard(response.Body, m.offset, cancelReq); err != nil {

			_ = response.Body.Close()
			cancelReq()

			return err

		}

	}

	m.setBody(&httpBody{ReadCloser: response.Body, cancel: cancelReq})

	return nil

}

func (m *MediaSource) noteSize(response *http.Response) {

	if response.StatusCode == http.StatusPartialContent {

		if total := contentRangeTotal(response.Header.Get("Content-Range")); total >= 0 {
			m.size = total
			return
		}

		if response.ContentLength >= 0 {
			m.size = m.offset + response.ContentLength
		}

		return

	}

	if response.ContentLength >= 0 {
		m.size = response.ContentLength
	}

}

func (m *MediaSource) discard(body io.Reader, count int64, cancelReq context.CancelFunc) error {

	buffer := make([]byte, readChunkBytes)

	for count > 0 {

		if err := m.ctx.Err(); err != nil {
			return err
		}

		chunk := int64(len(buffer))

		if chunk > count {
			chunk = count
		}

		watchdog := time.AfterFunc(m.timeout, cancelReq)
		n, err := io.ReadFull(body, buffer[:chunk])
		watchdog.Stop()

		count -= int64(n)

		if err != nil {
			return err
		}

	}

	return nil

}

func contentRangeTotal(value string) int64 {

	idx := strings.LastIndexByte(value, '/')

	if idx < 0 {
		return -1
	}

	total, err := strconv.ParseInt(strings.TrimSpace(value[idx+1:]), 10, 64)

	if err != nil {
		return -1
	}

	return total

}
