// Package contentdecode decodes explicitly wire-encoded HTTP response content.
// It never infers an encoding from payload magic or decodes request bodies.
package contentdecode

import (
	"bufio"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"errors"
	"io"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

const (
	MaxBytes       int64  = 64 << 20
	MaxLayers             = 4
	MaxWindowBytes uint64 = 8 << 20
	MaxMemoryBytes int64  = 256 << 20
	chunkBytes            = 32 << 10

	// Per-layer codec workspace, measured as retained heap per open reader
	// mid-stream (flate ~41 KiB, brotli ~100 KiB beyond its ring buffer, zstd
	// ~1.2 MiB beyond its window) with headroom. The former estimates reserved
	// 4-50x the real footprint and capped a generator at ~15 brotli responses.
	flateWorkspaceBytes    int64 = 96 << 10
	brotliOverheadBytes    int64 = 256 << 10
	zstdOverheadBytes      int64 = 2 << 20
	maxProcessMemoryBytes  int64 = 4 << 30
	DefaultAdmissionWait         = 10 * time.Second
	processMemoryLimitPart int64 = 8
)

type ErrorKind string

const (
	InvalidEncoding     ErrorKind = "invalid_encoding"
	UnsupportedEncoding ErrorKind = "unsupported_encoding"
	Truncated           ErrorKind = "truncated"
	InvalidData         ErrorKind = "invalid_data"
	EncodedLimit        ErrorKind = "encoded_limit"
	DecodedLimit        ErrorKind = "decoded_limit"
	WorkLimit           ErrorKind = "work_limit"
	LayerLimit          ErrorKind = "layer_limit"
	WindowLimit         ErrorKind = "window_limit"
	MemoryLimit         ErrorKind = "memory_limit"
	Canceled            ErrorKind = "canceled"
)

type Error struct{ Kind ErrorKind }

func (e *Error) Error() string {
	switch e.Kind {
	case InvalidEncoding:
		return "response content encoding is malformed"
	case UnsupportedEncoding:
		return "response content encoding is unsupported"
	case Truncated:
		return "response content is truncated"
	case InvalidData:
		return "response content is invalid"
	case EncodedLimit:
		return "encoded response content exceeded byte limit"
	case DecodedLimit:
		return "decoded response content exceeded byte limit"
	case WorkLimit:
		return "response content decoding exceeded work limit"
	case LayerLimit:
		return "response content encoding exceeded layer limit"
	case WindowLimit:
		return "response content decoder exceeded window limit"
	case MemoryLimit:
		return "load generator response decoding capacity exhausted"
	case Canceled:
		return "response content decoding canceled"
	default:
		return "response content decoding failed"
	}
}
func Kind(err error) ErrorKind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return ""
}
func failure(kind ErrorKind) error { return &Error{Kind: kind} }

// Limits applies independently to wire input and final output. MaxWorkBytes
// counts wire input and every layer's output, including discarded/intermediate
// bytes. MemoryBudget admits codec workspace and temporary Decode buffers;
// callers remain responsible for retaining returned output within their own
// capture budget. Nil uses the process-wide budget. AdmissionWait bounds how
// long a reader waits for workspace before failing with MemoryLimit; zero
// fails fast.
type Limits struct {
	MaxEncodedBytes int64
	MaxDecodedBytes int64
	MaxWorkBytes    int64
	MaxLayers       int
	MaxWindowBytes  uint64
	MemoryBudget    *MemoryBudget
	AdmissionWait   time.Duration
}

func DefaultLimits(maxBytes int64) Limits {
	if maxBytes <= 0 || maxBytes > MaxBytes {
		maxBytes = MaxBytes
	}
	return Limits{MaxEncodedBytes: maxBytes, MaxDecodedBytes: maxBytes, MaxWorkBytes: maxBytes * 8, MaxLayers: MaxLayers, MaxWindowBytes: MaxWindowBytes, AdmissionWait: DefaultAdmissionWait}
}
func (l Limits) normalize() Limits {
	defaults := DefaultLimits(l.MaxDecodedBytes)
	if l.MaxEncodedBytes <= 0 || l.MaxEncodedBytes > MaxBytes {
		l.MaxEncodedBytes = defaults.MaxEncodedBytes
	}
	if l.MaxDecodedBytes <= 0 || l.MaxDecodedBytes > MaxBytes {
		l.MaxDecodedBytes = defaults.MaxDecodedBytes
	}
	if l.MaxWorkBytes <= 0 || l.MaxWorkBytes > MaxBytes*8 {
		l.MaxWorkBytes = defaults.MaxWorkBytes
	}
	if l.MaxLayers <= 0 || l.MaxLayers > MaxLayers {
		l.MaxLayers = MaxLayers
	}
	if l.MaxWindowBytes < 1024 || l.MaxWindowBytes > MaxWindowBytes {
		l.MaxWindowBytes = MaxWindowBytes
	}
	if l.MemoryBudget == nil {
		l.MemoryBudget = processBudget()
	}
	if l.AdmissionWait < 0 {
		l.AdmissionWait = 0
	}
	return l
}

// MemoryBudget admits decoder workspace across concurrent readers. Readers
// reserve their whole multi-layer workspace at once, so a waiting reader never
// holds part of the budget while it waits.
type MemoryBudget struct {
	limit      int64
	used       atomic.Int64
	peak       atomic.Int64
	waits      atomic.Int64
	rejections atomic.Int64
	waitNanos  atomic.Int64
	mu         sync.Mutex
	released   chan struct{}
}

// BudgetStats reports one budget's capacity telemetry. Waits counts readers
// that had to queue for workspace; Rejections counts readers that gave up.
type BudgetStats struct {
	LimitBytes int64
	UsedBytes  int64
	PeakBytes  int64
	Waits      int64
	Rejections int64
	WaitTime   time.Duration
}

var (
	processMemory     MemoryBudget
	processMemoryOnce sync.Once
)

// processBudget sizes the shared budget on first use, after the generator has
// applied its container GOMEMLIMIT: one eighth of the limit, at least the
// historical 256 MiB and at most 4 GiB.
func processBudget() *MemoryBudget {
	processMemoryOnce.Do(func() {
		limit := MaxMemoryBytes
		if memoryLimit := debug.SetMemoryLimit(-1); memoryLimit > 0 && memoryLimit < 1<<62 {
			limit = max(limit, min(memoryLimit/processMemoryLimitPart, maxProcessMemoryBytes))
		}
		processMemory.limit = limit
	})
	return &processMemory
}

// ProcessStats reports the process-wide decoder budget.
func ProcessStats() BudgetStats { return processBudget().Stats() }

func NewMemoryBudget(limit int64) *MemoryBudget {
	if limit <= 0 || limit > MaxMemoryBytes {
		limit = MaxMemoryBytes
	}
	return &MemoryBudget{limit: limit}
}
func (b *MemoryBudget) Used() int64 { return b.used.Load() }
func (b *MemoryBudget) Stats() BudgetStats {
	return BudgetStats{LimitBytes: b.limit, UsedBytes: b.used.Load(), PeakBytes: b.peak.Load(), Waits: b.waits.Load(), Rejections: b.rejections.Load(), WaitTime: time.Duration(b.waitNanos.Load())}
}
func (b *MemoryBudget) reserve(n int64) bool {
	for {
		used := b.used.Load()
		if n < 0 || used > b.limit-n {
			return false
		}
		if b.used.CompareAndSwap(used, used+n) {
			for peak := b.peak.Load(); used+n > peak && !b.peak.CompareAndSwap(peak, used+n); peak = b.peak.Load() {
			}
			return true
		}
	}
}

// release returns workspace and wakes every waiting reader to retry.
func (b *MemoryBudget) release(n int64) {
	if n <= 0 {
		return
	}
	b.used.Add(-n)
	b.mu.Lock()
	if b.released != nil {
		close(b.released)
		b.released = nil
	}
	b.mu.Unlock()
}

// acquire reserves n bytes, waiting up to wait (and while ctx is live) for
// other readers to release workspace. A request larger than the whole budget
// fails immediately. The retry and the wait-channel creation share b.mu with
// release's notification, so a release between them cannot be missed.
func (b *MemoryBudget) acquire(ctx context.Context, n int64, wait time.Duration) (time.Duration, error) {
	if b.reserve(n) {
		return 0, nil
	}
	if wait <= 0 || n > b.limit {
		b.rejections.Add(1)
		return 0, failure(MemoryLimit)
	}
	start := time.Now()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	b.waits.Add(1)
	for {
		b.mu.Lock()
		if b.reserve(n) {
			b.mu.Unlock()
			waited := time.Since(start)
			b.waitNanos.Add(int64(waited))
			return waited, nil
		}
		if b.released == nil {
			b.released = make(chan struct{})
		}
		released := b.released
		b.mu.Unlock()
		select {
		case <-released:
		case <-timer.C:
			b.waitNanos.Add(int64(time.Since(start)))
			b.rejections.Add(1)
			return time.Since(start), failure(MemoryLimit)
		case <-ctx.Done():
			b.waitNanos.Add(int64(time.Since(start)))
			return time.Since(start), failure(Canceled)
		}
	}
}

type Reader struct {
	ctx         context.Context
	source      io.ReadCloser
	limits      Limits
	encodings   []string
	readMu      sync.Mutex
	reader      io.Reader
	closers     []io.Closer
	initialized bool
	err         error
	work        int64
	held        int64
	encoded     atomic.Int64
	decoded     atomic.Int64
	complete    atomic.Bool
	closed      atomic.Bool
	sourceOnce  sync.Once
	closeOnce   sync.Once
	stopContext func() bool
	waited      time.Duration
}

// NewReader takes ownership of source, including on validation failure. Header
// values are ordered exactly as received and decoded in reverse order. Read
// returns fixed errors; callers must discard previously collected output when
// a finite response fails. Complete is true only after validated stream EOF.
func NewReader(ctx context.Context, source io.ReadCloser, values []string, limits Limits) (*Reader, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if source == nil {
		return nil, failure(InvalidData)
	}
	limits = limits.normalize()
	encodings, err := parse(values, limits.MaxLayers)
	if err != nil {
		_ = source.Close()
		return nil, err
	}
	r := &Reader{ctx: ctx, source: source, limits: limits, encodings: encodings}
	r.stopContext = context.AfterFunc(ctx, func() { r.closeSource() })
	return r, nil
}
func (r *Reader) EncodedBytes() int64 { return r.encoded.Load() }
func (r *Reader) DecodedBytes() int64 { return r.decoded.Load() }
func (r *Reader) Complete() bool      { return r.complete.Load() }

// AdmissionWait reports how long the reader queued for decoder workspace.
func (r *Reader) AdmissionWait() time.Duration {
	r.readMu.Lock()
	defer r.readMu.Unlock()
	return r.waited
}
func (r *Reader) Decoding() bool {
	for _, e := range r.encodings {
		if e != "identity" {
			return true
		}
	}
	return false
}
func (r *Reader) closeSource() { r.sourceOnce.Do(func() { _ = r.source.Close() }) }

// reserve takes additional workspace without waiting. It serves growth after
// admission (Decode output buffers), where waiting would hold-and-wait.
func (r *Reader) reserve(n int64) error {
	if !r.limits.MemoryBudget.reserve(n) {
		r.limits.MemoryBudget.rejections.Add(1)
		return failure(MemoryLimit)
	}
	r.held += n
	return nil
}
func (r *Reader) release(n int64) {
	if n <= 0 || n > r.held {
		return
	}
	r.held -= n
	r.limits.MemoryBudget.release(n)
}

// layerWorkspace is the admitted cost of one decoding layer. Brotli's window is
// unknown until its first byte arrives, so admission assumes the largest
// allowed window and the layer returns the excess after reading the header.
func (r *Reader) layerWorkspace(encoding string) int64 {
	switch encoding {
	case "gzip", "deflate":
		return chunkBytes + flateWorkspaceBytes
	case "br":
		return chunkBytes + int64(r.limits.MaxWindowBytes) + brotliOverheadBytes
	case "zstd":
		return chunkBytes + int64(r.limits.MaxWindowBytes) + int64(r.limits.MaxWindowBytes)/4 + zstdOverheadBytes
	default:
		return 0
	}
}
func (r *Reader) charge(n int) error {
	if int64(n) > r.limits.MaxWorkBytes-r.work {
		return failure(WorkLimit)
	}
	r.work += int64(n)
	return nil
}
func (r *Reader) classify(err error) error {
	if err == nil || errors.Is(err, io.EOF) {
		return err
	}
	if Kind(err) != "" {
		return err
	}
	if r.ctx.Err() != nil {
		return failure(Canceled)
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return failure(Truncated)
	}
	if errors.Is(err, zstd.ErrWindowSizeExceeded) || errors.Is(err, zstd.ErrDecoderSizeExceeded) {
		return failure(WindowLimit)
	}
	return failure(InvalidData)
}
func (r *Reader) initialize() error {
	if r.initialized {
		return nil
	}
	r.initialized = true
	workspace := int64(0)
	for _, encoding := range r.encodings {
		workspace += r.layerWorkspace(encoding)
	}
	if workspace > 0 {
		waited, err := r.limits.MemoryBudget.acquire(r.ctx, workspace, r.limits.AdmissionWait)
		r.waited = waited
		if err != nil {
			return err
		}
		r.held += workspace
	}
	var current io.Reader = &boundedReader{reader: r.source, owner: r, maximum: r.limits.MaxEncodedBytes, wire: true}
	for i := len(r.encodings) - 1; i >= 0; i-- {
		encoding := r.encodings[i]
		if encoding == "identity" {
			continue
		}
		input := &countReader{reader: current}
		buffered := bufio.NewReaderSize(input, chunkBytes)
		var decoder io.Reader
		switch encoding {
		case "gzip":
			codec, err := gzip.NewReader(buffered)
			if err != nil {
				if errors.Is(err, io.EOF) {
					return failure(Truncated)
				}
				return r.classify(err)
			}
			r.closers = append(r.closers, codec)
			decoder = codec
		case "deflate":
			header, err := buffered.Peek(2)
			if err != nil {
				if errors.Is(err, io.EOF) {
					return failure(Truncated)
				}
				return r.classify(err)
			}
			// A raw stored block can share zlib's CMF byte through its ignored
			// padding bits. Require FCHECK too before selecting the wrapped codec;
			// once selected, codec/checksum failures must never retry as raw flate.
			if header[0]&15 == 8 && header[0]>>4 <= 7 && (uint16(header[0])<<8|uint16(header[1]))%31 == 0 {
				codec, err := zlib.NewReader(buffered)
				if err != nil {
					return r.classify(err)
				}
				r.closers = append(r.closers, codec)
				decoder = codec
			} else {
				codec := flate.NewReader(buffered)
				r.closers = append(r.closers, codec)
				decoder = codec
			}
		case "br":
			header, err := buffered.Peek(1)
			if err != nil {
				if errors.Is(err, io.EOF) {
					return failure(Truncated)
				}
				return r.classify(err)
			}
			window, err := brotliWindow(header[0])
			if err != nil {
				return err
			}
			if window > r.limits.MaxWindowBytes {
				return failure(WindowLimit)
			}
			r.release(int64(r.limits.MaxWindowBytes - window))
			decoder = brotli.NewReader(buffered)
		case "zstd":
			codec, err := zstd.NewReader(buffered, zstd.WithDecoderMaxMemory(r.limits.MaxWindowBytes), zstd.WithDecoderMaxWindow(r.limits.MaxWindowBytes), zstd.WithDecoderConcurrency(1), zstd.WithDecoderLowmem(true), zstd.WithDecodeBuffersBelow(0))
			if err != nil {
				return r.classify(err)
			}
			r.closers = append(r.closers, closeFunc(func() error { codec.Close(); return nil }))
			decoder = codec
		}
		current = &layerReader{reader: decoder, upstream: buffered, input: input, owner: r}
	}
	r.reader = current
	return nil
}
func (r *Reader) Read(p []byte) (int, error) {
	r.readMu.Lock()
	defer r.readMu.Unlock()
	if len(p) == 0 {
		return 0, nil
	}
	if r.err != nil {
		return 0, r.err
	}
	if r.ctx.Err() != nil {
		r.err = failure(Canceled)
		return 0, r.err
	}
	if r.closed.Load() {
		return 0, io.ErrClosedPipe
	}
	if r.complete.Load() {
		return 0, io.EOF
	}
	if err := r.initialize(); err != nil {
		r.err = r.classify(err)
		return 0, r.err
	}
	if len(p) > chunkBytes {
		p = p[:chunkBytes]
	}
	remaining := r.limits.MaxDecodedBytes - r.decoded.Load()
	if int64(len(p)) > remaining+1 {
		p = p[:remaining+1]
	}
	n, err := r.reader.Read(p)
	if r.ctx.Err() != nil {
		r.err = failure(Canceled)
		return 0, r.err
	}
	if int64(n) > remaining {
		r.err = failure(DecodedLimit)
		return 0, r.err
	}
	if err != nil && !errors.Is(err, io.EOF) {
		r.err = r.classify(err)
		return 0, r.err
	}
	r.decoded.Add(int64(n))
	if errors.Is(err, io.EOF) {
		r.complete.Store(true)
	}
	return n, err
}
func (r *Reader) Close() error {
	r.closeOnce.Do(func() {
		r.closed.Store(true)
		r.closeSource() // Unblock codec input before waiting for its Read/Close.
		if r.stopContext != nil {
			r.stopContext()
		}
		r.readMu.Lock()
		defer r.readMu.Unlock()
		for i := len(r.closers) - 1; i >= 0; i-- {
			_ = r.closers[i].Close()
		}
		r.reader = nil
		r.closers = nil
		r.limits.MemoryBudget.release(r.held)
		r.held = 0
	})
	return nil
}

type closeFunc func() error

func (f closeFunc) Close() error { return f() }

type boundedReader struct {
	reader        io.Reader
	owner         *Reader
	maximum, read int64
	wire          bool
	emptyReads    int
}

func (b *boundedReader) Read(p []byte) (int, error) {
	if b.owner.ctx.Err() != nil {
		return 0, failure(Canceled)
	}
	if len(p) > chunkBytes {
		p = p[:chunkBytes]
	}
	remaining := b.maximum - b.read
	if int64(len(p)) > remaining+1 {
		p = p[:remaining+1]
	}
	n, err := b.reader.Read(p)
	if n == 0 && err == nil {
		b.emptyReads++
		if b.emptyReads >= 100 {
			return 0, failure(InvalidData)
		}
	} else {
		b.emptyReads = 0
	}
	b.read += int64(n)
	if b.wire {
		b.owner.encoded.Add(int64(n))
	}
	if int64(n) > remaining {
		return 0, failure(EncodedLimit)
	}
	if chargeErr := b.owner.charge(n); chargeErr != nil {
		return 0, chargeErr
	}
	return n, err
}

// layerReader requires the complete upstream to end with the codec. zlib/raw
// deflate otherwise accept trailing garbage; byte-reader input prevents hidden
// codec over-read and this final check rejects the unread suffix.
type layerReader struct {
	reader   io.Reader
	upstream *bufio.Reader
	input    *countReader
	owner    *Reader
	done     bool
}

func (l *layerReader) Read(p []byte) (int, error) {
	if l.done {
		return 0, io.EOF
	}
	n, err := l.reader.Read(p)
	if chargeErr := l.owner.charge(n); chargeErr != nil {
		return 0, chargeErr
	}
	if errors.Is(err, io.EOF) {
		if l.input.read == 0 {
			return 0, failure(Truncated)
		}
		if _, tailErr := l.upstream.ReadByte(); tailErr == nil {
			return 0, failure(InvalidData)
		} else if !errors.Is(tailErr, io.EOF) {
			return 0, tailErr
		}
		l.done = true
	}
	return n, err
}
func parse(values []string, maxLayers int) ([]string, error) {
	if len(values) > maxLayers {
		return nil, failure(LayerLimit)
	}
	var out []string
	for _, value := range values {
		if len(value) > 1024 {
			return nil, failure(InvalidEncoding)
		}
		for _, part := range strings.Split(value, ",") {
			if len(out) >= maxLayers {
				return nil, failure(LayerLimit)
			}
			part = strings.ToLower(strings.TrimSpace(part))
			if part == "" {
				return nil, failure(InvalidEncoding)
			}
			for _, c := range part {
				if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
					return nil, failure(InvalidEncoding)
				}
			}
			switch part {
			case "identity", "deflate", "br", "zstd":
			case "gzip", "x-gzip":
				part = "gzip"
			case "zstandard":
				part = "zstd"
			default:
				return nil, failure(UnsupportedEncoding)
			}
			out = append(out, part)
		}
	}
	return out, nil
}
func brotliWindow(first byte) (uint64, error) {
	bits := uint(16)
	if first&1 != 0 {
		n := (first >> 1) & 7
		if n != 0 {
			bits = uint(17 + n)
		} else {
			n = (first >> 4) & 7
			if n == 1 {
				return 0, failure(InvalidData)
			}
			if n == 0 {
				bits = 17
			} else {
				bits = uint(8 + n)
			}
		}
	}
	return uint64(1) << bits, nil
}

// Decode returns no body on failure. It buffers only final decoded output;
// intermediate layers remain streaming and cumulative byte work is bounded.
func Decode(ctx context.Context, body []byte, values []string, limits Limits) ([]byte, error) {
	r, err := NewReader(ctx, io.NopCloser(bytes.NewReader(body)), values, limits)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	var out []byte
	if err := r.reserve(chunkBytes); err != nil {
		return nil, err
	}
	chunk := make([]byte, chunkBytes)
	for {
		n, readErr := r.Read(chunk)
		if n > 0 {
			if len(out)+n > cap(out) {
				capacity := cap(out) * 2
				if capacity < len(out)+n {
					capacity = len(out) + n
				}
				if int64(capacity) > r.limits.MaxDecodedBytes {
					capacity = int(r.limits.MaxDecodedBytes)
				}
				if err := r.reserve(int64(capacity)); err != nil {
					return nil, err
				}
				next := make([]byte, len(out), capacity)
				copy(next, out)
				r.release(int64(cap(out)))
				out = next
			}
			out = append(out, chunk[:n]...)
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) && r.Complete() {
				return out, nil
			}
			return nil, readErr
		}
	}
}

type countReader struct {
	reader io.Reader
	read   int64
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.reader.Read(p)
	c.read += int64(n)
	return n, err
}
