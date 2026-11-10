package engine

import (
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// A cache-hit child whose evidence is wiped after a runtime credential
// redaction failure (executeConcurrentHTTPSequence, the retry classification
// paths) must stay resolved locally: counted unsplit, it would drop the
// sequence aggregate's and every enclosing transaction's TTFB.
func TestClearUnredactableResultEvidenceKeepsCacheHitResolvedLocally(t *testing.T) {
	for name, mark := range map[string]func(*protocol.Result){
		"version_source": func(r *protocol.Result) { r.HTTPVersionSource = "cache" },
		"content_source": func(r *protocol.Result) { r.ProtocolEvidence["response_content_source"] = "cache" },
	} {
		t.Run(name, func(t *testing.T) {
			child := &protocol.Result{DependencyType: "http", ByteEvidence: protocol.ByteEvidenceHTTPBody, Duration: 4 * time.Millisecond, StatusCode: 200, ProtocolEvidence: map[string]any{}}
			mark(child)
			clearUnredactableResultEvidence(child)
			if !resultResolvedLocally(child) {
				t.Fatalf("cache hit lost its marker: source=%q evidence=%v", child.HTTPVersionSource, child.ProtocolEvidence)
			}
			aggregate := &protocol.Result{}
			copySequenceSourceResult(aggregate, child)
			if got := sequenceUnsplitChildren(aggregate); got != 0 {
				t.Fatalf("cache-hit child counted unsplit after evidence wipe: %d", got)
			}
		})
	}
	// A wire response keeps no cache marker, so a wiped no-TTFB wire child is
	// still unsplit.
	wire := &protocol.Result{DependencyType: "http", ByteEvidence: protocol.ByteEvidenceHTTPBody, Duration: 4 * time.Millisecond, HTTPVersionSource: "wire", ProtocolEvidence: map[string]any{"response_content_source": "wire"}}
	clearUnredactableResultEvidence(wire)
	if resultResolvedLocally(wire) || wire.HTTPVersionSource != "" {
		t.Fatalf("wire result gained a local marker: source=%q evidence=%v", wire.HTTPVersionSource, wire.ProtocolEvidence)
	}
}

// The async markers the engine rolled a row up under must survive the
// redaction-failure wipe so the report and drill-down claim the archived row
// exactly as the live rollup did; the conversation id itself is withheld.
func TestClearUnredactableResultEvidenceKeepsAsyncStructuralMarkers(t *testing.T) {
	result := &protocol.Result{ProtocolEvidence: map[string]any{
		"async_dispatch":           true,
		asyncDispatchEpochEvidence: int64(3),
		"async_conversation":       "conv-secret-id",
		"background_request":       true,
		"adapter_text":             "sensitive",
	}}
	clearUnredactableResultEvidence(result)
	evidence := result.ProtocolEvidence
	if dispatch, _ := evidence["async_dispatch"].(bool); !dispatch {
		t.Fatalf("async_dispatch lost: %v", evidence)
	}
	if epoch, _ := evidence[asyncDispatchEpochEvidence].(int64); epoch != 3 {
		t.Fatalf("async_dispatch_epoch lost: %v", evidence)
	}
	if conversation, ok := evidence["async_conversation"]; !ok || conversation != true {
		t.Fatalf("async_conversation presence not kept as a neutral marker: %v", evidence)
	}
	if !resultOutsideSourceTransactions(result) {
		t.Fatalf("wiped async row no longer outside source transactions: %v", evidence)
	}
	if _, ok := evidence["adapter_text"]; ok {
		t.Fatalf("non-structural evidence survived the wipe: %v", evidence)
	}
	if bounded, _ := evidence["concurrent_group_evidence_bounded"].(bool); !bounded {
		t.Fatalf("wipe marker missing: %v", evidence)
	}

	// A transaction row keeps the epoch its transaction opened in.
	tx := &protocol.Result{BusinessTransaction: true, ProtocolEvidence: map[string]any{asyncDispatchEpochEvidence: int64(5)}}
	clearUnredactableResultEvidence(tx)
	if epoch, _ := tx.ProtocolEvidence[asyncDispatchEpochEvidence].(int64); epoch != 5 {
		t.Fatalf("transaction epoch lost: %v", tx.ProtocolEvidence)
	}
}
