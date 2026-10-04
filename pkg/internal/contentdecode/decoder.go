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
	"strings"
	"sync"
	"sync/atomic"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

const (
	MaxBytes       int64  = 64 << 20
	MaxLayers             = 4
	MaxWindowBytes uint64 = 8 << 20
	MaxMemoryBytes int64  = 256 << 20
	chunkBytes            = 32 << 10
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
		return "response content decoder memory budget exhausted"
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
// capture budget. Nil uses the process-wide fail-fast budget.
type Limits struct {
	MaxEncodedBytes int64
	MaxDecodedBytes int64
	MaxWorkBytes    int64
	MaxLayers       int
	MaxWindowBytes  uint64
	MemoryBudget    *MemoryBudget
}

func DefaultLimits(maxBytes int64) Limits {
	if maxBytes <= 0 || maxBytes > MaxBytes {
		maxBytes = MaxBytes
	}
	return Limits{MaxEncodedBytes: maxBytes, MaxDecodedBytes: maxBytes, MaxWorkBytes: maxBytes * 8, MaxLayers: MaxLayers, MaxWindowBytes: MaxWindowBytes}
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
		l.MemoryBudget = &processMemory
	}
	return l
}

type MemoryBudget struct {
	limit int64
	used  atomic.Int64
}

var processMemory = MemoryBudget{limit: MaxMemoryBytes}

func NewMemoryBudget(limit int64) *MemoryBudget {
	if limit <= 0 || limit > MaxMemoryBytes {
		limit = MaxMemoryBytes
	}
	return &MemoryBudget{limit: limit}
}
func (b *MemoryBudget) Used() int64 { return b.used.Load() }
func (b *MemoryBudget) reserve(n int64) bool {
	for {
		used := b.used.Load()
		if n < 0 || used > b.limit-n {
			return false
		}
		if b.used.CompareAndSwap(used, used+n) {
			return true
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
func (r *Reader) Decoding() bool {
	for _, e := range r.encodings {
		if e != "identity" {
			return true
		}
	}
	return false
}
func (r *Reader) closeSource() { r.sourceOnce.Do(func() { _ = r.source.Close() }) }
func (r *Reader) reserve(n int64) error {
	if !r.limits.MemoryBudget.reserve(n) {
		return failure(MemoryLimit)
	}
	r.held += n
	return nil
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
	var current io.Reader = &boundedReader{reader: r.source, owner: r, maximum: r.limits.MaxEncodedBytes, wire: true}
	for i := len(r.encodings) - 1; i >= 0; i-- {
		encoding := r.encodings[i]
		if encoding == "identity" {
			continue
		}
		if err := r.reserve(chunkBytes); err != nil {
			return err
		}
		input := &countReader{reader: current}
		buffered := bufio.NewReaderSize(input, chunkBytes)
		var decoder io.Reader
		switch encoding {
		case "gzip":
			if err := r.reserve(1 << 20); err != nil {
				return err
			}
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
			if err := r.reserve(1 << 20); err != nil {
				return err
			}
			header, err := buffered.Peek(2)
			if err != nil {
				if errors.Is(err, io.EOF) {
					return failure(Truncated)
				}
				return r.classify(err)
			}
			if header[0]&15 == 8 && header[0]>>4 <= 7 {
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
			if err := r.reserve(int64(window)*2 + 8<<20); err != nil {
				return err
			}
			decoder = brotli.NewReader(buffered)
		case "zstd":
			if err := r.reserve(int64(r.limits.MaxWindowBytes)*4 + 8<<20); err != nil {
				return err
			}
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
		r.limits.MemoryBudget.used.Add(-r.held)
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
				r.limits.MemoryBudget.used.Add(-int64(cap(out)))
				r.held -= int64(cap(out))
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
