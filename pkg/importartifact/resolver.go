// Package importartifact stores retained import sources and inspection sessions.
package importartifact

import (
	"context"
	"github.com/jasonczech/apexload/pkg/importartifact/legacyresolver"
	"io"
)

// Request is the retired Bruno execution request.
// Deprecated: native import/source workflows use EncryptedStore directly.
type Request = legacyresolver.Request

// Resolver is retained for external Go source compatibility.
// Deprecated: the bundled Bruno runtime is retired.
type Resolver = legacyresolver.Resolver

// StreamingResolver is retained for external Go source compatibility.
// Deprecated: the bundled Bruno runtime is retired.
type StreamingResolver = legacyresolver.StreamingResolver

// ResolverFunc adapts an external legacy resolver.
// Deprecated: the bundled Bruno runtime is retired.
type ResolverFunc = legacyresolver.ResolverFunc

// SetResolver installs only the external legacy resolver; controller startup
// and native import storage never call it.
// Deprecated: the bundled Bruno runtime is retired.
func SetResolver(value Resolver) func() { return legacyresolver.SetResolver(value) }

// ResolverAvailable reports external legacy resolver availability.
// Deprecated: the bundled Bruno runtime is retired.
func ResolverAvailable() bool { return legacyresolver.ResolverAvailable() }

// Resolve invokes the external legacy execution resolver.
// Deprecated: native import/source workflows use EncryptedStore.ReadAll.
func Resolve(ctx context.Context, request Request) ([]byte, error) {
	return legacyresolver.Resolve(ctx, request)
}

// Open invokes the external legacy execution resolver.
// Deprecated: native import/source workflows use EncryptedStore.Open.
func Open(ctx context.Context, request Request) (io.ReadCloser, int64, error) {
	return legacyresolver.Open(ctx, request)
}
