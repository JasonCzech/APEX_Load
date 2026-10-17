package legacyresolver

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
)

type streamingResolverFunc func(context.Context, Request) (io.ReadCloser, int64, error)

func (fn streamingResolverFunc) ResolveBrunoArtifact(context.Context, Request) ([]byte, error) {
	return nil, nil
}

func (fn streamingResolverFunc) OpenBrunoArtifact(ctx context.Context, request Request) (io.ReadCloser, int64, error) {
	return fn(ctx, request)
}

func TestResolveVerifiesDigest(t *testing.T) {
	restore := SetResolver(ResolverFunc(func(context.Context, Request) ([]byte, error) {
		return []byte("bundle"), nil
	}))
	defer restore()

	if _, err := Resolve(context.Background(), Request{
		Reference: "artifact://bundle",
		SHA256:    strings.Repeat("0", 64),
	}); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("Resolve error = %v", err)
	}
}

func TestOpenVerifiesStreamingDigestAtEOF(t *testing.T) {
	restore := SetResolver(streamingResolverFunc(func(context.Context, Request) (io.ReadCloser, int64, error) {
		return io.NopCloser(bytes.NewReader([]byte("bundle"))), 6, nil
	}))
	defer restore()

	reader, _, err := Open(context.Background(), Request{
		Reference: "artifact://bundle",
		SHA256:    strings.Repeat("0", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := io.ReadAll(reader); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("ReadAll error = %v", err)
	}
}
