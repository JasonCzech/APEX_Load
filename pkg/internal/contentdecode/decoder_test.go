package contentdecode

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"encoding/binary"
	"hash/adler32"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

func encoded(t *testing.T, input []byte, encoding string) []byte {
	t.Helper()
	var out bytes.Buffer
	var writer io.WriteCloser
	switch encoding {
	case "gzip":
		writer = gzip.NewWriter(&out)
	case "deflate":
		writer = zlib.NewWriter(&out)
	case "raw-deflate":
		var err error
		writer, err = flate.NewWriter(&out, flate.DefaultCompression)
		if err != nil {
			t.Fatal(err)
		}
	case "br":
		writer = brotli.NewWriter(&out)
	case "zstd":
		var err error
		writer, err = zstd.NewWriter(&out, zstd.WithEncoderConcurrency(1), zstd.WithWindowSize(1<<20))
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := writer.Write(input); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
func TestDecodeWholeOrderedStack(t *testing.T) {
	payload := []byte(`{"value":"synthetic response"}`)
	for _, sequence := range [][]string{{}, {"gzip"}, {"deflate"}, {"raw-deflate"}, {"br"}, {"zstd"}, {"gzip", "br", "zstd"}, {"zstd", "deflate", "gzip"}} {
		t.Run(strings.Join(sequence, "_"), func(t *testing.T) {
			body := payload
			var headers []string
			for _, encoding := range sequence {
				body = encoded(t, body, encoding)
				if encoding == "raw-deflate" {
					encoding = "deflate"
				}
				headers = append(headers, encoding)
			}
			result, err := Decode(context.Background(), body, headers, DefaultLimits(1<<20))
			if err != nil || !bytes.Equal(result, payload) {
				t.Fatalf("decode result mismatch, reason=%s", Kind(err))
			}
		})
	}
	body := encoded(t, encoded(t, payload, "gzip"), "br")
	if result, err := Decode(context.Background(), body, []string{" GZip, br "}, DefaultLimits(1<<20)); err != nil || !bytes.Equal(result, payload) {
		t.Fatalf("ordered single-field decode failed: %s", Kind(err))
	}
}
func TestDecodeRejectsMalformedTruncatedAndTrailingDataWithoutPartialBody(t *testing.T) {
	payload := bytes.Repeat([]byte("synthetic finite data"), 4096)
	for _, encoding := range []string{"gzip", "deflate", "raw-deflate", "br", "zstd"} {
		t.Run(encoding, func(t *testing.T) {
			body := encoded(t, payload, encoding)
			header := encoding
			if header == "raw-deflate" {
				header = "deflate"
			}
			for _, invalid := range [][]byte{nil, body[:len(body)-1], append(append([]byte{}, body...), 0xff, 0xff)} {
				result, err := Decode(context.Background(), invalid, []string{header}, DefaultLimits(1<<20))
				if err == nil || len(result) != 0 {
					t.Fatal("invalid finite content returned a successful or partial body")
				}
			}
		})
	}
	// The final layer may finish, but a missing outer footer invalidates the stack.
	body := encoded(t, encoded(t, payload, "gzip"), "deflate")
	body = body[:len(body)-2]
	if result, err := Decode(context.Background(), body, []string{"gzip, deflate"}, DefaultLimits(1<<20)); err == nil || len(result) != 0 {
		t.Fatal("intermediate decoder trailer was not validated")
	}
}
func TestDecodeRejectsEncodingShapeAndLayers(t *testing.T) {
	for _, tc := range []struct {
		values []string
		kind   ErrorKind
	}{{[]string{""}, InvalidEncoding}, {[]string{"gzip,"}, InvalidEncoding}, {[]string{"gzip;param=secret"}, InvalidEncoding}, {[]string{"private-unknown-value"}, UnsupportedEncoding}, {[]string{"identity, identity, identity, identity, identity"}, LayerLimit}} {
		result, err := Decode(context.Background(), []byte("data"), tc.values, DefaultLimits(1024))
		if Kind(err) != tc.kind || result != nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "private") {
			t.Fatalf("encoding shape reason=%s, want %s", Kind(err), tc.kind)
		}
	}
}
func TestDecodeIndependentLimitsAndWorkspaceAdmission(t *testing.T) {
	body := encoded(t, bytes.Repeat([]byte("x"), 10000), "gzip")
	for _, tc := range []struct {
		name   string
		limits Limits
		kind   ErrorKind
	}{
		{"encoded", Limits{MaxEncodedBytes: int64(len(body) - 1), MaxDecodedBytes: 20000, MaxWorkBytes: 100000}, EncodedLimit},
		{"decoded", Limits{MaxEncodedBytes: 1000, MaxDecodedBytes: 100, MaxWorkBytes: 100000}, DecodedLimit},
		{"work", Limits{MaxEncodedBytes: 1000, MaxDecodedBytes: 20000, MaxWorkBytes: 100}, WorkLimit},
		{"workspace", Limits{MaxEncodedBytes: 1000, MaxDecodedBytes: 20000, MaxWorkBytes: 100000, MemoryBudget: NewMemoryBudget(100)}, MemoryLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := Decode(context.Background(), body, []string{"gzip"}, tc.limits)
			if Kind(err) != tc.kind || result != nil {
				t.Fatalf("reason=%s, want %s", Kind(err), tc.kind)
			}
			if tc.limits.MemoryBudget != nil && tc.limits.MemoryBudget.Used() != 0 {
				t.Fatal("failed decoder retained workspace")
			}
		})
	}
	for _, encoding := range []string{"br", "zstd"} {
		body := encoded(t, bytes.Repeat([]byte("window"), 10000), encoding)
		limits := DefaultLimits(1 << 20)
		limits.MaxWindowBytes = 1024
		if result, err := Decode(context.Background(), body, []string{encoding}, limits); Kind(err) != WindowLimit || result != nil {
			t.Fatalf("%s advertised-window limit reason=%s", encoding, Kind(err))
		}
	}
	limits := Limits{MaxEncodedBytes: 1 << 62, MaxDecodedBytes: 1 << 62, MaxWorkBytes: 1 << 62, MaxLayers: 100, MaxWindowBytes: 1 << 62}.normalize()
	if limits.MaxEncodedBytes != MaxBytes || limits.MaxDecodedBytes != MaxBytes || limits.MaxWorkBytes != MaxBytes*8 || limits.MaxLayers != MaxLayers || limits.MaxWindowBytes != MaxWindowBytes {
		t.Fatal("untrusted limit overflow bypassed hard ceilings")
	}
}
func TestReaderCancellationUnblocksCodecInputAndReleasesWorkspace(t *testing.T) {
	for _, encoding := range []string{"gzip", "deflate", "br", "zstd"} {
		t.Run(encoding, func(t *testing.T) {
			input, output := io.Pipe()
			ctx, cancel := context.WithCancel(context.Background())
			limits := DefaultLimits(1 << 20)
			limits.MemoryBudget = NewMemoryBudget(MaxMemoryBytes)
			started := make(chan struct{})
			reader, err := NewReader(ctx, &observedSource{ReadCloser: input, started: started}, []string{encoding}, limits)
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { _, err := io.Copy(io.Discard, reader); done <- err }()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("codec never began its source read")
			}
			cancel()
			select {
			case err := <-done:
				if Kind(err) != Canceled {
					t.Fatalf("cancel reason=%s", Kind(err))
				}
			case <-time.After(time.Second):
				t.Fatal("codec input remained blocked after context cancellation")
			}
			_ = reader.Close()
			_ = output.Close()
			if reader.Complete() || limits.MemoryBudget.Used() != 0 {
				t.Fatal("cancel reported completion or retained workspace")
			}
		})
	}
}
func TestReaderAggregateMemoryFailsFastAndCanBeReused(t *testing.T) {
	body := encoded(t, []byte("sample"), "gzip")
	// Room for exactly one gzip reader; zero AdmissionWait fails fast.
	budget := NewMemoryBudget(chunkBytes + flateWorkspaceBytes)
	limits := DefaultLimits(1024)
	limits.MemoryBudget = budget
	limits.AdmissionWait = 0
	first, err := NewReader(context.Background(), io.NopCloser(bytes.NewReader(body)), []string{"gzip"}, limits)
	if err != nil {
		t.Fatal(err)
	}
	one := make([]byte, 1)
	if _, err := first.Read(one); err != nil {
		t.Fatal(err)
	}
	second, err := NewReader(context.Background(), io.NopCloser(bytes.NewReader(body)), []string{"gzip"}, limits)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.Read(one); Kind(err) != MemoryLimit {
		t.Fatalf("aggregate admission=%s", Kind(err))
	}
	_ = second.Close()
	_ = first.Close()
	if budget.Used() != 0 {
		t.Fatal("concurrent decoder budget leaked")
	}
	next, err := NewReader(context.Background(), io.NopCloser(bytes.NewReader(body)), []string{"gzip"}, limits)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(io.Discard, next)
	_ = next.Close()
	if err != nil || !next.Complete() {
		t.Fatal("released decoder workspace was not reusable")
	}
}
func TestReaderConcurrentCloseUnblocksCompressedRead(t *testing.T) {
	source, writer := io.Pipe()
	reader, err := NewReader(context.Background(), source, []string{"zstd"}, DefaultLimits(1<<20))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, reader); close(done) }()
	var wait sync.WaitGroup
	wait.Add(2)
	for range 2 {
		go func() { defer wait.Done(); _ = reader.Close() }()
	}
	wait.Wait()
	_ = writer.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("concurrent codec Close deadlocked")
	}
}

type observedSource struct {
	io.ReadCloser
	started chan struct{}
	once    sync.Once
}

func (s *observedSource) Read(p []byte) (int, error) {
	s.once.Do(func() { close(s.started) })
	return s.ReadCloser.Read(p)
}
func TestDecodeRejectsInvalidZlibHeaderAndPreservesConstructorLimits(t *testing.T) {
	body := encoded(t, []byte("synthetic"), "deflate")
	body[1] ^= 1
	if result, err := Decode(context.Background(), body, []string{"deflate"}, DefaultLimits(1024)); Kind(err) != InvalidData || result != nil {
		t.Fatal("invalid nominal zlib header fell back to raw deflate")
	}
	for _, codec := range []string{"deflate", "br"} {
		body := encoded(t, []byte("synthetic"), codec)
		limits := DefaultLimits(1024)
		limits.MaxEncodedBytes = 1
		if result, err := Decode(context.Background(), body, []string{codec}, limits); Kind(err) != EncodedLimit || result != nil {
			t.Fatalf("%s constructor lost encoded-limit reason=%s", codec, Kind(err))
		}
	}
	first := encoded(t, []byte("first "), "gzip")
	second := encoded(t, []byte("second"), "gzip")
	if result, err := Decode(context.Background(), append(first, second...), []string{"gzip"}, DefaultLimits(1024)); err != nil || string(result) != "first second" {
		t.Fatal("valid concatenated gzip members rejected")
	}
}

// The stored block's ignored padding makes CMF look like zlib, but 0x7802
// fails FCHECK. It is nevertheless a complete raw-deflate encoding of "ab".
var combinedReviewRawDeflate = []byte{0x78, 0x02, 0x00, 0xfd, 0xff, 'a', 'b', 0x01, 0x00, 0x00, 0xff, 0xff}

func TestDeflateCombinedReviewHeaderSelection(t *testing.T) {
	wrapped := encoded(t, []byte("ab"), "deflate")
	stacked := encoded(t, combinedReviewRawDeflate, "gzip")
	for _, tc := range []struct {
		name    string
		body    []byte
		headers []string
	}{
		{"raw_with_zlib_CMF", combinedReviewRawDeflate, []string{"deflate"}},
		{"wrapped", wrapped, []string{"deflate"}},
		{"raw_inside_gzip", stacked, []string{"deflate, gzip"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := Decode(context.Background(), tc.body, tc.headers, DefaultLimits(1024))
			if err != nil || string(result) != "ab" {
				t.Fatalf("valid deflate rejected: body=%q kind=%s", result, Kind(err))
			}
		})
	}

	limits := DefaultLimits(1024)
	limits.MemoryBudget = NewMemoryBudget(MaxMemoryBytes)
	reader, err := NewReader(context.Background(), &combinedReviewByteSource{Reader: bytes.NewReader(combinedReviewRawDeflate)}, []string{"deflate"}, limits)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var result bytes.Buffer
	chunk := make([]byte, 1)
	for {
		n, err := reader.Read(chunk)
		result.Write(chunk[:n])
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("fragmented streaming deflate rejected: %s", Kind(err))
		}
	}
	if result.String() != "ab" || !reader.Complete() || reader.EncodedBytes() != int64(len(combinedReviewRawDeflate)) || reader.DecodedBytes() != 2 {
		t.Fatal("raw-deflate stream lost completion or byte receipts")
	}
	_ = reader.Close()
	if limits.MemoryBudget.Used() != 0 {
		t.Fatal("raw-deflate stream retained codec workspace after Close")
	}
}

func TestDeflateCombinedReviewRawLimits(t *testing.T) {
	for _, tc := range []struct {
		name   string
		limits Limits
		kind   ErrorKind
	}{
		{"encoded", Limits{MaxEncodedBytes: int64(len(combinedReviewRawDeflate) - 1), MaxDecodedBytes: 1024, MaxWorkBytes: 1024}, EncodedLimit},
		{"decoded", Limits{MaxEncodedBytes: 1024, MaxDecodedBytes: 1, MaxWorkBytes: 1024}, DecodedLimit},
		{"work", Limits{MaxEncodedBytes: 1024, MaxDecodedBytes: 1024, MaxWorkBytes: int64(len(combinedReviewRawDeflate) + 1)}, WorkLimit},
		{"workspace", Limits{MaxEncodedBytes: 1024, MaxDecodedBytes: 1024, MaxWorkBytes: 1024, MemoryBudget: NewMemoryBudget(100)}, MemoryLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := Decode(context.Background(), combinedReviewRawDeflate, []string{"deflate"}, tc.limits)
			if result != nil || Kind(err) != tc.kind {
				t.Fatalf("raw-deflate limit reason=%s, want %s", Kind(err), tc.kind)
			}
			if tc.limits.MemoryBudget != nil && tc.limits.MemoryBudget.Used() != 0 {
				t.Fatal("failed raw-deflate decoder retained workspace")
			}
		})
	}
}

// This has two valid interpretations. The wrapped stream contains one stored
// block and an Adler-32 footer; raw flate consumes that footer as payload. A
// corrupt footer must fail the selected wrapped codec even though raw retry
// would produce a complete, different representation.
func combinedReviewAmbiguousDeflate() []byte {
	wire := make([]byte, 65448)
	copy(wire, []byte{0x78, 0x5e, 0x00, 0xa1, 0xff, 0x5e, 0x00})
	wire[99] = 0x01
	binary.LittleEndian.PutUint16(wire[100:102], 65353)
	binary.LittleEndian.PutUint16(wire[102:104], ^uint16(65353))
	wire = append(wire, 0x01, 0x00, 0x00, 0xff, 0xff)
	return binary.BigEndian.AppendUint32(wire, adler32.Checksum(wire[7:65448]))
}

func TestDeflateCombinedReviewWrappedChecksumNoRawRetry(t *testing.T) {
	wire := combinedReviewAmbiguousDeflate()
	if result, err := Decode(context.Background(), wire, []string{"deflate"}, DefaultLimits(1<<20)); err != nil || len(result) != 65441 {
		t.Fatalf("valid wrapped fixture rejected: %s", Kind(err))
	}
	wire[len(wire)-1] ^= 1
	raw := flate.NewReader(bytes.NewReader(wire))
	result, err := io.ReadAll(raw)
	_ = raw.Close()
	if err != nil || len(result) != 65447 {
		t.Fatal("fixture does not distinguish unsafe raw retry")
	}
	limits := DefaultLimits(1 << 20)
	limits.MemoryBudget = NewMemoryBudget(MaxMemoryBytes)
	if result, err := Decode(context.Background(), wire, []string{"deflate"}, limits); result != nil || Kind(err) != InvalidData {
		t.Fatalf("corrupt wrapped checksum accepted or retained partial bytes: %s", Kind(err))
	}
	if limits.MemoryBudget.Used() != 0 {
		t.Fatal("failed wrapped decoder retained workspace")
	}
	reader, err := NewReader(context.Background(), io.NopCloser(bytes.NewReader(wire)), []string{"deflate"}, limits)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(io.Discard, reader)
	_ = reader.Close()
	if Kind(err) != InvalidData || reader.Complete() || limits.MemoryBudget.Used() != 0 {
		t.Fatal("streaming wrapped checksum failure retried raw or claimed completion")
	}
}

type combinedReviewByteSource struct{ *bytes.Reader }

func (s *combinedReviewByteSource) Read(data []byte) (int, error) {
	if len(data) > 1 {
		data = data[:1]
	}
	return s.Reader.Read(data)
}
func (*combinedReviewByteSource) Close() error { return nil }
