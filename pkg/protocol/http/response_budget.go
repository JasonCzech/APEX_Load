package http

import (
	"bytes"
	"fmt"
	"io"
	"sync/atomic"
)

// maxHTTPResponseCaptureBytes bounds response bytes concurrently retained by
// all HTTP VUsers in this process. Per-response limits alone do not prevent
// thousands of individually valid buffers from exhausting a generator pod.
const maxHTTPResponseCaptureBytes int64 = 256 << 20

var processHTTPResponseCaptureBudget = responseCaptureBudget{limit: maxHTTPResponseCaptureBytes}

type responseCaptureBudget struct {
	limit int64
	used  atomic.Int64
}

type responseCaptureLease struct {
	budget *responseCaptureBudget
	held   int64
}

func (b *responseCaptureBudget) lease() *responseCaptureLease {
	return &responseCaptureLease{budget: b}
}

func (l *responseCaptureLease) reserve(bytes int64) bool {
	if bytes <= 0 || l == nil || l.budget == nil || l.budget.limit <= 0 {
		return true
	}
	for {
		used := l.budget.used.Load()
		if used > l.budget.limit-bytes {
			return false
		}
		if l.budget.used.CompareAndSwap(used, used+bytes) {
			l.held += bytes
			return true
		}
	}
}

func (l *responseCaptureLease) releaseBytes(bytes int64) {
	if bytes <= 0 || l == nil || l.budget == nil {
		return
	}
	if bytes > l.held {
		bytes = l.held
	}
	l.held -= bytes
	l.budget.used.Add(-bytes)
}

func (l *responseCaptureLease) release() {
	if l == nil {
		return
	}
	l.releaseBytes(l.held)
}

// responseCaptureWriter admits backing-buffer growth before allocating it. It
// intentionally does not block: blocking thousands of response readers can
// retain sockets and partial buffers indefinitely, whereas a failed sample is
// bounded, observable, and immediately releases its lease.
type responseCaptureWriter struct {
	buffer *bytes.Buffer
	lease  *responseCaptureLease
}

func (l *responseCaptureLease) exhaustedError() error {
	limit := maxHTTPResponseCaptureBytes
	if l != nil && l.budget != nil && l.budget.limit > 0 {
		limit = l.budget.limit
	}
	return fmt.Errorf("HTTP response capture memory budget (%d bytes) exhausted", limit)
}

func (w *responseCaptureWriter) Write(content []byte) (int, error) {
	if w == nil || w.buffer == nil {
		return 0, fmt.Errorf("response capture buffer is unavailable")
	}
	if !w.lease.reserve(int64(len(content))) {
		return 0, w.lease.exhaustedError()
	}
	written, err := w.buffer.Write(content)
	if unwritten := len(content) - written; unwritten > 0 {
		w.lease.releaseBytes(int64(unwritten))
	}
	return written, err
}

// responseCaptureReader accounts bytes before a parser such as bufio.Reader
// retains or copies them. The caller-provided read buffer is small and fixed;
// rejecting after the underlying read therefore cannot itself create a large
// unaccounted allocation.
type responseCaptureReader struct {
	reader io.Reader
	lease  *responseCaptureLease
}

func (r *responseCaptureReader) Read(content []byte) (int, error) {
	if r == nil || r.reader == nil {
		return 0, io.EOF
	}
	read, err := r.reader.Read(content)
	if read > 0 && !r.lease.reserve(int64(read)) {
		return 0, r.lease.exhaustedError()
	}
	return read, err
}
