package engine

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

const maxBrowserEvidenceProviders = 512
const maxBrowserSeenMeasurements = 4096

type browserSeenMeasurement struct {
	revision uint64
	terminal bool
	digest   [32]byte
}
// LiveBrowserDocumentEvidenceSink receives scrubbed incremental and final
// browser document evidence for provisional live evaluation without writing to
// the durable archive sidecar.
type LiveBrowserDocumentEvidenceSink interface {
	RecordLiveBrowserDocumentEvidence(*protocol.BrowserDocumentEvidence) error
}

type browserDocumentRegistration struct {
	mu                                  sync.Mutex
	provider                            protocol.BrowserDocumentEvidenceProvider
	registry                            *engineBrowserDocuments
	group, region                       string
	finished                            bool
	generation                          string
	sourceDropped                       int64
	sink                                ResultSink
	publishedGeneration                 string
	publishedDigest                     [32]byte
	receiptDigest                       [32]byte
	receiptPublished, receiptPublishing bool
	receiptDone                         chan struct{}
	publishing                          bool
	redaction                           func() *RedactionConfig
	liveSeen                            map[string]browserSeenMeasurement
	liveGeneration                      string
	liveSourceDropped                   int64
	liveDroppedRequestIdentities        int64
	liveIncomplete                      bool
}
type engineBrowserDocuments struct {
	mu                 sync.Mutex
	entries            []*browserDocumentRegistration
	interactions       []*messagingProgressRegistration
	seen               map[string]browserSeenMeasurement
	window             map[string]protocol.BrowserDocumentObservation
	dropped            int64
	incomplete, closed bool
	coverageDirty      bool
	receiptIncomplete  bool
}

// markIncomplete is called with state.mu held. Coverage changes must be
// published even when no action or countable document observation occurred.
func (state *engineBrowserDocuments) markIncomplete() {
	if !state.incomplete {
		state.coverageDirty = true
	}
	state.incomplete = true
}

func readBrowserDocuments(p protocol.BrowserDocumentEvidenceProvider) (out *protocol.BrowserDocumentEvidence) {
	defer func() {
		if recover() != nil {
			out = nil
		}
	}()
	return p.BrowserDocumentEvidence().Clone()
}
func (mc *MetricsCollector) registerBrowserDocuments(p protocol.Protocol, group string, redaction ...func() *RedactionConfig) *browserDocumentRegistration {
	provider, ok := p.(protocol.BrowserDocumentEvidenceProvider)
	if !ok || mc == nil {
		return nil
	}
	if enabled, reported := p.(interface{ BrowserDocumentCaptureEnabled() bool }); reported && !enabled.BrowserDocumentCaptureEnabled() {
		return nil
	}
	state := &mc.browserDocuments
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closed {
		return nil
	}
	if len(state.entries) >= maxBrowserEvidenceProviders {
		state.markIncomplete()
		return nil
	}
	registration := &browserDocumentRegistration{provider: provider, registry: state, group: boundedMetricString(group, 256)}
	if len(redaction) > 0 {
		registration.redaction = redaction[0]
	}
	state.entries = append(state.entries, registration)
	return registration
}
func (r *browserDocumentRegistration) nextLiveDeltaLocked(raw *protocol.BrowserDocumentEvidence) *protocol.BrowserDocumentEvidence {
	if r == nil || raw == nil {
		return nil
	}
	if r.liveGeneration != raw.SourceGeneration {
		r.liveGeneration = raw.SourceGeneration
		r.liveSourceDropped = 0
		r.liveDroppedRequestIdentities = 0
		r.liveIncomplete = false
		clear(r.liveSeen)
	}
	if r.liveSeen == nil {
		r.liveSeen = make(map[string]browserSeenMeasurement)
	}
	var delta []protocol.BrowserDocumentObservation
	for _, observation := range raw.Observations {
		data, _ := json.Marshal(observation)
		digest := sha256.Sum256(data)
		previous, exists := r.liveSeen[observation.MeasurementID]
		if exists {
			if observation.Revision < previous.revision || previous.terminal || observation.Revision == previous.revision {
				continue
			}
		} else if len(r.liveSeen) >= maxBrowserSeenMeasurements {
			r.liveIncomplete = true
			continue
		}
		r.liveSeen[observation.MeasurementID] = browserSeenMeasurement{
			revision: observation.Revision,
			terminal: observation.State == "final" || observation.State == "censored",
			digest:   digest,
		}
		delta = append(delta, observation)
	}
	coverageChanged := raw.DroppedObservations > r.liveSourceDropped ||
		raw.DroppedRequestIdentities > r.liveDroppedRequestIdentities ||
		(raw.Incomplete && !r.liveIncomplete)
	if raw.DroppedObservations > r.liveSourceDropped {
		r.liveSourceDropped = raw.DroppedObservations
	}
	if raw.DroppedRequestIdentities > r.liveDroppedRequestIdentities {
		r.liveDroppedRequestIdentities = raw.DroppedRequestIdentities
	}
	if raw.Incomplete {
		r.liveIncomplete = true
	}
	if len(delta) == 0 && !coverageChanged {
		return nil
	}
	cloned := raw.Clone()
	if cloned == nil {
		return nil
	}
	cloned.Observations = delta
	cloned.Incomplete = cloned.Incomplete || r.liveIncomplete
	return protocol.PackBrowserDocumentEvidence(cloned)
}

func (r *browserDocumentRegistration) capture(finish bool) bool {
	if r == nil {
		return false
	}
	if finish {
		r.publishReceipts()
	}
	r.mu.Lock()
	if r.provider == nil {
		r.mu.Unlock()
		return false
	}
	sink, group, region := r.sink, r.group, r.region
	redactionCfg := currentRedactionConfig(r.redaction)
	rawEvidence := readBrowserDocuments(r.provider)
	if rawEvidence != nil {
		for i := range rawEvidence.Observations {
			rawEvidence.Observations[i].Group = group
			rawEvidence.Observations[i].Region = region
		}
	}
	evidence := scrubBrowserDocumentEvidence(rawEvidence.Clone(), redactionCfg)
	state := r.registry
	state.mu.Lock()
	if evidence == nil {
		state.markIncomplete()
		if finish {
			r.finished = true
			r.provider = nil
		}
		coverageDirty := state.coverageDirty
		state.mu.Unlock()
		r.mu.Unlock()
		return coverageDirty
	}
	changed := false
	if r.generation != evidence.SourceGeneration {
		r.generation = evidence.SourceGeneration
		r.sourceDropped = 0
	}
	// Request identities and document observations have distinct units. Losing
	// either makes coverage incomplete; only observation losses are counted.
	dropped := evidence.DroppedObservations
	if dropped > r.sourceDropped {
		state.dropped = min(int64(1e12), state.dropped+dropped-r.sourceDropped)
		r.sourceDropped = dropped
		state.coverageDirty = true
		changed = true
	}
	if evidence.Incomplete || evidence.DroppedObservations > 0 || evidence.DroppedRequestIdentities > 0 {
		state.markIncomplete()
	}
	if state.seen == nil {
		state.seen = make(map[string]browserSeenMeasurement)
		state.window = make(map[string]protocol.BrowserDocumentObservation)
	}
	for _, observation := range evidence.Observations {
		data, _ := json.Marshal(observation)
		digest := sha256.Sum256(data)
		previous, exists := state.seen[observation.MeasurementID]
		if exists {
			if observation.Revision < previous.revision {
				continue
			}
			if previous.terminal {
				if digest != previous.digest {
					state.markIncomplete()
				}
				continue
			}
			if observation.Revision == previous.revision {
				if digest != previous.digest {
					state.markIncomplete()
				}
				continue
			}
		}
		if !exists && len(state.seen) >= maxBrowserSeenMeasurements {
			// The bounded identity ledger cannot distinguish rejected retries.
			// Retain unknown incomplete coverage without inventing a distinct
			// document-observation loss count on each publication window.
			state.markIncomplete()
			continue
		}
		state.seen[observation.MeasurementID] = browserSeenMeasurement{revision: observation.Revision, terminal: observation.State == "final" || observation.State == "censored", digest: digest}
		if !exists || observation.State != "provisional" {
			state.window[observation.MeasurementID] = observation
		} else if _, pending := state.window[observation.MeasurementID]; pending {
			state.window[observation.MeasurementID] = observation
		}
		changed = true
	}
	liveSink, hasLiveSink := sink.(LiveBrowserDocumentEvidenceSink)
	var liveDelta *protocol.BrowserDocumentEvidence
	if hasLiveSink && rawEvidence != nil {
		liveDelta = r.nextLiveDeltaLocked(rawEvidence)
	}
	if finish {
		r.finished = true
		r.provider = nil
	}
	state.mu.Unlock()
	r.mu.Unlock()
	if hasLiveSink && liveDelta != nil {
		result := &protocol.Result{
			ActionName:    "Browser document evidence",
			SampleKind:    "browser_document_evidence",
			Group:         group,
			Region:        region,
			Phase:         "teardown",
			StartTime:     time.Now().UTC(),
			Success:       true,
			ClientMetrics: &protocol.ClientMetrics{DocumentEvidence: liveDelta},
		}
		scrubSensitiveStructuredResult(result, redactionCfg)
		scrubSensitiveResultWithOptions(result, redactionCfg, false)
		boundResultEvidence(result)
		if result.ClientMetrics != nil && result.ClientMetrics.DocumentEvidence != nil {
			_ = liveSink.RecordLiveBrowserDocumentEvidence(result.ClientMetrics.DocumentEvidence)
		}
	}
	return changed
}
func (state *engineBrowserDocuments) sample(final bool) bool {
	state.mu.Lock()
	entries := append([]*browserDocumentRegistration(nil), state.entries...)
	interactions := append([]*messagingProgressRegistration(nil), state.interactions...)
	if final {
		state.closed = true
	}
	state.mu.Unlock()
	changed := false
	for _, entry := range entries {
		changed = entry.capture(final) || changed
	}
	for _, entry := range interactions {
		entry.flushBrowserInteractions()
	}
	finished := make(map[*browserDocumentRegistration]bool, len(entries))
	for _, entry := range entries {
		entry.mu.Lock()
		finished[entry] = entry.finished
		entry.mu.Unlock()
	}
	finishedInteractions := make(map[*messagingProgressRegistration]bool, len(interactions))
	for _, entry := range interactions {
		entry.mu.Lock()
		finishedInteractions[entry] = entry.finished || final
		entry.mu.Unlock()
	}
	state.mu.Lock()
	kept := state.entries[:0]
	for _, entry := range state.entries {
		if !finished[entry] {
			kept = append(kept, entry)
		}
	}
	clear(state.entries[len(kept):])
	state.entries = kept
	keptInteractions := state.interactions[:0]
	for _, entry := range state.interactions {
		if !finishedInteractions[entry] {
			keptInteractions = append(keptInteractions, entry)
		}
	}
	clear(state.interactions[len(keptInteractions):])
	state.interactions = keptInteractions
	// A provider can finish between ticks and retire its handle after writing
	// terminal observations. Only snapshot publication acknowledges that window.
	changed = changed || state.coverageDirty || len(state.window) > 0
	state.coverageDirty = false
	state.mu.Unlock()
	return changed
}
func (state *engineBrowserDocuments) snapshot() *protocol.BrowserDocumentSummary {
	state.mu.Lock()
	defer state.mu.Unlock()
	if len(state.window) == 0 && state.dropped == 0 && !state.incomplete {
		return nil
	}
	ids := make([]string, 0, len(state.window))
	for id := range state.window {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	observations := make([]protocol.BrowserDocumentObservation, 0, len(ids))
	for _, id := range ids {
		observations = append(observations, state.window[id])
	}
	summary := protocol.SummarizeBrowserDocumentObservations(observations, state.dropped, state.incomplete)
	clear(state.window)
	state.dropped = 0
	state.coverageDirty = false
	return summary
}
func (r *browserDocumentRegistration) publish(sink ResultSink, redaction func() *RedactionConfig, region string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.sink = sink
	r.redaction = redaction
	r.region = boundedMetricString(region, 256)
	r.mu.Unlock()
}

// bindBrowserDocumentRedaction publishes only an immutable matcher getter.
// Provider reads and aggregation must use the same disclosure boundary as
// action results, including credentials resolved after protocol initialization.
func (mc *MetricsCollector) bindBrowserDocumentRedaction(p protocol.Protocol, redaction func() *RedactionConfig) {
	if mc == nil || p == nil || redaction == nil {
		return
	}
	state := &mc.browserDocuments
	state.mu.Lock()
	entries := append([]*browserDocumentRegistration(nil), state.entries...)
	state.mu.Unlock()
	for _, entry := range entries {
		entry.mu.Lock()
		if reflect.ValueOf(entry.provider) == reflect.ValueOf(p) {
			entry.redaction = redaction
		}
		entry.mu.Unlock()
	}
}

func (r *browserDocumentRegistration) publishFinal(evidence *protocol.BrowserDocumentEvidence, redaction *RedactionConfig) {
	r.publishReceipts()
	if r == nil || evidence == nil {
		return
	}
	evidence = evidence.Clone()
	if evidence == nil {
		return
	}
	r.mu.Lock()
	sink, group, region := r.sink, r.group, r.region
	if sink == nil || r.publishing {
		r.mu.Unlock()
		return
	}
	for i := range evidence.Observations {
		evidence.Observations[i].Group = group
		evidence.Observations[i].Region = region
	}
	evidence = protocol.PackBrowserDocumentEvidence(evidence)
	if evidence == nil {
		r.mu.Unlock()
		return
	}
	data, _ := json.Marshal(evidence)
	digest := sha256.Sum256(data)
	if r.publishedGeneration == evidence.SourceGeneration && r.publishedDigest == digest {
		r.mu.Unlock()
		return
	}
	r.publishing = true
	r.mu.Unlock()
	result := &protocol.Result{ActionName: "Browser document evidence", SampleKind: "browser_document_evidence", Group: group, Region: region, Phase: "teardown", StartTime: time.Now().UTC(), Success: true, ClientMetrics: &protocol.ClientMetrics{DocumentEvidence: evidence}}
	scrubSensitiveStructuredResult(result, redaction)
	scrubSensitiveResultWithOptions(result, redaction, false)
	boundResultEvidence(result)
	documentSink, supported := sink.(BrowserDocumentEvidenceSink)
	var err error
	if !supported {
		err = fmt.Errorf("browser document archive sink is unavailable")
	} else {
		err = documentSink.RecordBrowserDocumentEvidence(result.ClientMetrics.DocumentEvidence)
	}
	if liveSink, liveSupported := sink.(LiveBrowserDocumentEvidenceSink); liveSupported && result.ClientMetrics != nil && result.ClientMetrics.DocumentEvidence != nil {
		_ = liveSink.RecordLiveBrowserDocumentEvidence(result.ClientMetrics.DocumentEvidence.Clone())
	}
	r.mu.Lock()
	r.publishing = false
	if err == nil {
		r.publishedGeneration = evidence.SourceGeneration
		r.publishedDigest = digest
	}
	r.mu.Unlock()
	if err != nil {
		r.registry.mu.Lock()
		r.registry.markIncomplete()
		r.registry.mu.Unlock()
	}
}

func (r *browserDocumentRegistration) finish(redaction *RedactionConfig) {
	if r == nil {
		return
	}
	r.mu.Lock()
	provider := r.provider
	r.mu.Unlock()
	if provider != nil {
		evidence := readBrowserDocuments(provider)
		r.publishFinal(evidence, redaction)
	}
	r.capture(true)
}
func (mc *MetricsCollector) captureBrowserProvider(p protocol.Protocol, sink ResultSink, group, region string, redaction *RedactionConfig) {
	if mc == nil {
		return
	}
	state := &mc.browserDocuments
	state.mu.Lock()
	entries := append([]*browserDocumentRegistration(nil), state.entries...)
	state.mu.Unlock()
	for _, entry := range entries {
		entry.mu.Lock()
		provider := entry.provider
		entry.mu.Unlock()
		if reflect.ValueOf(provider) == reflect.ValueOf(p) {
			entry.publish(sink, func() *RedactionConfig { return redaction }, region)
			evidence := readBrowserDocuments(provider)
			entry.publishFinal(evidence, redaction)
			entry.capture(false)
			return
		}
	}
}
