package engine

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// Bounding a concurrent group's public evidence must not erase the markers
// the phase rollup and the report rely on: a cache hit stays a cache hit (not
// a no-first-byte response), and async-dispatch work keeps its epoch.
func TestOmitConcurrentPublicEvidenceKeepsStructuralMarkers(t *testing.T) {
	cached := &protocol.Result{
		DependencyType:    "http",
		HTTPVersionSource: "cache",
		ProtocolEvidence:  map[string]any{"response_content_source": "cache", "secret_header": "x"},
	}
	omitConcurrentPublicEvidence(cached)
	if !resultResolvedLocally(cached) {
		t.Fatalf("cache hit lost its marker: source=%q evidence=%v", cached.HTTPVersionSource, cached.ProtocolEvidence)
	}
	if _, leaked := cached.ProtocolEvidence["secret_header"]; leaked {
		t.Fatal("non-structural evidence survived the wipe")
	}

	dispatched := &protocol.Result{ProtocolEvidence: map[string]any{"async_dispatch": true, asyncDispatchEpochEvidence: int64(7)}}
	omitConcurrentPublicEvidence(dispatched)
	if dispatched.ProtocolEvidence["async_dispatch"] != true || dispatched.ProtocolEvidence[asyncDispatchEpochEvidence] != int64(7) {
		t.Fatalf("async markers lost: %v", dispatched.ProtocolEvidence)
	}

	wire := &protocol.Result{DependencyType: "http", ByteEvidence: protocol.ByteEvidenceHTTPBody, ProtocolEvidence: map[string]any{}}
	omitConcurrentPublicEvidence(wire)
	if resultResolvedLocally(wire) {
		t.Fatal("a wire response must not be treated as resolved locally")
	}
}
