package contentdecode

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
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
	budget := NewMemoryBudget((1 << 20) + chunkBytes)
	limits := DefaultLimits(1024)
	limits.MemoryBudget = budget
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
