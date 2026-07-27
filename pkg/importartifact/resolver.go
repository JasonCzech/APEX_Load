// Package importartifact provides the narrow process boundary used by the
// Bruno protocol to resolve immutable source bundles. Controller and generator
// processes install a project-aware resolver; the protocol never opens an
// author-supplied filesystem path directly.
package importartifact

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"strings"
	"sync"
)

type Request struct {
	Reference string
	ProjectID string
	SHA256    string
}

type Resolver interface {
	ResolveBrunoArtifact(context.Context, Request) ([]byte, error)
}

type StreamingResolver interface {
	OpenBrunoArtifact(context.Context, Request) (io.ReadCloser, int64, error)
}

type digestVerifyingReadCloser struct {
	reader   io.ReadCloser
	hash     hash.Hash
	expected string
	verified bool
}

func (r *digestVerifyingReadCloser) Read(payload []byte) (int, error) {
	n, err := r.reader.Read(payload)
	if n > 0 {
		_, _ = r.hash.Write(payload[:n])
	}
	if err == io.EOF && !r.verified {
		r.verified = true
		actual := hex.EncodeToString(r.hash.Sum(nil))
		if actual != r.expected {
			return n, fmt.Errorf("Bruno artifact digest mismatch: got %s", actual)
		}
	}
	return n, err
}

func (r *digestVerifyingReadCloser) Close() error { return r.reader.Close() }

type ResolverFunc func(context.Context, Request) ([]byte, error)

func (fn ResolverFunc) ResolveBrunoArtifact(ctx context.Context, request Request) ([]byte, error) {
	return fn(ctx, request)
}

var (
	resolverMu sync.RWMutex
	resolver   Resolver
)

// SetResolver installs the process-scoped resolver and returns a restoration
// function. Production processes install one resolver during startup; the
// restoration seam keeps protocol tests isolated.
func SetResolver(value Resolver) func() {
	resolverMu.Lock()
	previous := resolver
	resolver = value
	resolverMu.Unlock()
	return func() {
		resolverMu.Lock()
		resolver = previous
		resolverMu.Unlock()
	}
}

// ResolverAvailable reports whether this process has an artifact delivery
// implementation installed. Generator capability advertisement uses this to
// avoid claiming Bruno support when immutable source delivery is unavailable.
func ResolverAvailable() bool {
	resolverMu.RLock()
	defer resolverMu.RUnlock()
	return resolver != nil
}

func Resolve(ctx context.Context, request Request) ([]byte, error) {
	reader, size, err := Open(ctx, request)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	if size < 0 {
		size = 0
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func Open(ctx context.Context, request Request) (io.ReadCloser, int64, error) {
	request.Reference = strings.TrimSpace(request.Reference)
	request.ProjectID = strings.TrimSpace(request.ProjectID)
	request.SHA256 = strings.ToLower(strings.TrimSpace(request.SHA256))
	if request.Reference == "" {
		return nil, 0, fmt.Errorf("Bruno artifact reference is required")
	}
	resolverMu.RLock()
	current := resolver
	resolverMu.RUnlock()
	if current == nil {
		return nil, 0, fmt.Errorf("Bruno artifact resolver is unavailable")
	}
	if streaming, ok := current.(StreamingResolver); ok {
		reader, size, err := streaming.OpenBrunoArtifact(ctx, request)
		if err != nil || request.SHA256 == "" {
			return reader, size, err
		}
		return &digestVerifyingReadCloser{
			reader:   reader,
			hash:     sha256.New(),
			expected: request.SHA256,
		}, size, nil
	}
	data, err := current.ResolveBrunoArtifact(ctx, request)
	if err != nil {
		return nil, 0, err
	}
	if request.SHA256 != "" {
		sum := sha256.Sum256(data)
		actual := hex.EncodeToString(sum[:])
		if actual != request.SHA256 {
			return nil, 0, fmt.Errorf("Bruno artifact digest mismatch: got %s", actual)
		}
	}
	return io.NopCloser(bytes.NewReader(data)), int64(len(data)), nil
}
