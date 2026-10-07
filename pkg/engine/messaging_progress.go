package engine

import (
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// Lifecycle evidence is independent of action results. Provider reads must be
// local bounded copies; the registry never holds collector locks during them.
// Active references and retained rows both have fixed bounds.
type engineMessagingProgress struct {
	mu      sync.Mutex
	next    uint64
	entries map[uint64]*messagingProgressRegistration
	retired *protocol.MessagingProgressSnapshot
	latest  *protocol.MessagingProgressSnapshot
	dropped int64
	closed  bool
}

type messagingProgressRegistration struct {
	browser *browserDocumentRegistration
	// har hands a browser session's published HAR artifacts to the sink
	// publishBrowser named, after the final teardown.
	har      protocol.Protocol
	harSink  ResultSink
	mu       sync.Mutex
	id       uint64
	provider protocol.Protocol
	group    string
	latest   *protocol.MessagingProgressSnapshot
	finished bool
}

func (mc *MetricsCollector) registerMessagingProgress(p protocol.Protocol, group string, resources *engineRuntimeResources, phase ...string) *messagingProgressRegistration {
	return mc.registerMessagingProgressWithRedaction(p, group, resources, nil, phase...)
}

func (mc *MetricsCollector) registerMessagingProgressWithRedaction(p protocol.Protocol, group string, resources *engineRuntimeResources, redaction func() *RedactionConfig, phase ...string) *messagingProgressRegistration {
	if mc == nil || p == nil {
		return nil
	}
	browser := mc.registerBrowserDocuments(p, group, redaction)
	_, harProvider := p.(protocol.BrowserHARArtifactProvider)
	if browser != nil || harProvider {
		entry := &messagingProgressRegistration{browser: browser}
		if harProvider {
			entry.har = p
		}
		return entry
	}
	// Processor configuration alone does not imply consumption. Producer-only
	// scripts must not retain otherwise unused per-VUser processor trackers.
	expected := false
	name, named := messagingProgressProtocolName(p)
	if named && name == "eventhubs" {
		if resources == nil {
			return nil
		}
		resources.mu.Lock()
		binding := resources.bindings[group]
		enabled := binding != nil && binding.plan.RequiresReservation()
		if enabled && binding.plan.initOnce {
			enabled = binding.plan.mainConsume
			if len(phase) > 0 {
				switch phase[0] {
				case "init":
					enabled = binding.plan.initConsume
				case "teardown":
					enabled = binding.plan.teardownConsume
				}
			}
		}
		resources.mu.Unlock()
		if !enabled {
			return nil
		}
		expected = true
	}
	var s *protocol.MessagingProgressSnapshot
	if named {
		s, _, _ = protocol.ReadMessagingProgress(p)
	} else {
		s = protocol.NewUnavailableMessagingProgress("malformed")
		// Faulty adapter metadata must not escape the lifecycle panic guards or
		// retain an unsafe provider reference for later asynchronous reads.
		p = nil
	}
	if s == nil {
		if !expected {
			return nil
		}
		s = protocol.NewUnavailableMessagingProgress("source_missing")
	}
	originalGroup := group
	group = boundedMetricString(group, 256)
	if group != originalGroup {
		s.Incomplete = true
	}
	trustMessagingProgress(s, group)
	state := &mc.messagingProgress
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closed {
		return nil
	}
	if len(state.entries) >= protocol.MaxMessagingProgressRows {
		state.dropped = min(int64(1e12), state.dropped+1)
		return nil
	}
	if state.entries == nil {
		state.entries = make(map[uint64]*messagingProgressRegistration)
	}
	state.next++
	entry := &messagingProgressRegistration{id: state.next, provider: p, group: group, latest: s}
	state.entries[entry.id] = entry
	return entry
}

func messagingProgressProtocolName(p protocol.Protocol) (name string, ok bool) {
	defer func() {
		if recover() != nil {
			name, ok = "", false
		}
	}()
	return p.Name(), true
}

func messagingProgressIdentity(row protocol.MessagingProgressRow) string {
	return strings.Join([]string{row.Origin, row.Group, row.Protocol, row.SourceID, row.Subscription, row.SessionID, row.Partition, row.Generation}, "\x00")
}

func messagingProgressPartitionIdentity(row protocol.MessagingProgressRow) string {
	return strings.Join([]string{row.Origin, row.Group, row.Protocol, row.SourceID, row.Subscription, row.SessionID, row.Partition}, "\x00")
}

func trustMessagingProgress(s *protocol.MessagingProgressSnapshot, group string) {
	for i := range s.Rows {
		s.Rows[i].Group = group
		s.Rows[i].Origin = "" // Only authenticated transport may assign origin.
	}
}

func (entry *messagingProgressRegistration) capture(finished bool) {
	if entry == nil {
		return
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	entry.finished = entry.finished || finished
	if entry.provider == nil {
		return
	}
	s, _, _ := protocol.ReadMessagingProgress(entry.provider)
	if s == nil {
		s = protocol.NewUnavailableMessagingProgress("source_missing")
	}
	trustMessagingProgress(s, entry.group)
	// A reporter returns cumulative values. Replays replace evidence rather
	// than adding its counters or its already-known dropped-row count again.
	dropped := s.DroppedRows
	capturedAt := entry.latest.CapturedAt
	if s.UnavailableReason == "" && !s.CapturedAt.Before(capturedAt) {
		// The local tracker owns its bounded generation inventory. Explicit
		// source eviction replaces that inventory; provisional rows also retire
		// when the same partition obtains a verified generation. Neither can
		// be repeatedly counted as fresh loss on every capture.
		identities := make(map[string]bool, len(s.Rows))
		verifiedPartitions := make(map[string]bool, len(s.Rows))
		for _, row := range s.Rows {
			identities[messagingProgressIdentity(row)] = true
			if row.Generation != "unverified" {
				verifiedPartitions[messagingProgressPartitionIdentity(row)] = true
			}
		}
		kept := entry.latest.Rows[:0]
		for _, row := range entry.latest.Rows {
			if !identities[messagingProgressIdentity(row)] {
				if dropped > 0 || row.Generation == "unverified" && verifiedPartitions[messagingProgressPartitionIdentity(row)] {
					continue
				}
				entry.latest.Incomplete = true
				row.Incomplete = true
			}
			kept = append(kept, row)
		}
		clear(entry.latest.Rows[len(kept):])
		entry.latest.Rows = kept
	}
	// Source coverage is a cumulative lower bound. Clear the incoming value
	// during row reconciliation and restore the maximum, preserving explicit
	// source loss without counting a replay as additional eviction.
	s.DroppedRows = 0
	protocol.MergeMessagingProgress(&entry.latest, s)
	// The shared merger combines independent reporters. This cache belongs to
	// one reporter, so its latest capture owns its lifecycle transition.
	if !s.CapturedAt.Before(capturedAt) {
		entry.latest.Lifecycle = s.Lifecycle
	}
	entry.latest.DroppedRows = max(dropped, entry.latest.DroppedRows)
	if entry.latest.CapturedAt.Before(capturedAt) {
		entry.latest.CapturedAt = capturedAt
	}
}

func (entry *messagingProgressRegistration) finish() { entry.capture(true) }

func runProtocolTeardownWithMessagingProgress(p protocol.Protocol, entry *messagingProgressRegistration, providers ...func() *RedactionConfig) error {
	err := runProtocolTeardown(p, providers...)
	if entry != nil && entry.har != nil {
		entry.mu.Lock()
		sink := entry.harSink
		entry.mu.Unlock()
		handOffBrowserSessionEvidence(entry.har, sink)
	}
	if entry != nil && entry.browser != nil {
		var redaction *RedactionConfig
		if len(providers) > 0 && providers[0] != nil {
			redaction = providers[0]()
		}
		entry.browser.finish(redaction)
	}
	entry.finish()
	return err
}

// sample runs only on the collector consumer. Final sampling releases provider
// references after retaining explicit incomplete coverage for unproved cleanup.
func (state *engineMessagingProgress) sample(final bool) bool {
	state.mu.Lock()
	if final {
		state.closed = true
	}
	entries := make([]*messagingProgressRegistration, 0, len(state.entries))
	for _, entry := range state.entries {
		entries = append(entries, entry)
	}
	state.mu.Unlock()
	sort.Slice(entries, func(i, j int) bool { return entries[i].id < entries[j].id })
	for _, entry := range entries {
		entry.capture(false)
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	out := state.retired.Clone()
	lifecycle, lifecycleSet := "", false
	observeLifecycle := func(s *protocol.MessagingProgressSnapshot) {
		if s == nil {
			return
		}
		if !lifecycleSet {
			lifecycle, lifecycleSet = s.Lifecycle, true
		} else if lifecycle != s.Lifecycle {
			lifecycle = ""
		}
	}
	observeLifecycle(out)
	for _, entry := range entries {
		entry.mu.Lock()
		s := entry.latest.Clone()
		released := entry.finished && s != nil && s.Lifecycle == "released"
		if final && !released && s != nil {
			s.Incomplete = true
			for i := range s.Rows {
				s.Rows[i].Incomplete = true
			}
		}
		if released || final {
			entry.provider = nil
		}
		entry.mu.Unlock()
		if released {
			protocol.MergeMessagingProgress(&state.retired, s)
			delete(state.entries, entry.id)
		}
		observeLifecycle(s)
		protocol.MergeMessagingProgress(&out, s)
	}
	if out != nil {
		out.Lifecycle = lifecycle
	}
	if state.dropped > 0 {
		if out == nil {
			out = protocol.NewUnavailableMessagingProgress("source_missing")
		}
		out.DroppedRows = min(int64(1e12), out.DroppedRows+state.dropped)
		out.Incomplete = true
	}
	if final {
		state.closed = true
		clear(state.entries)
	}
	changed := !reflect.DeepEqual(state.latest, out)
	state.latest = out
	return changed
}

func (state *engineMessagingProgress) snapshot() *protocol.MessagingProgressSnapshot {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.latest.Clone()
}

func (entry *messagingProgressRegistration) publishBrowser(sink ResultSink, region string, redaction func() *RedactionConfig) {
	if entry != nil && entry.har != nil {
		entry.mu.Lock()
		entry.harSink = sink
		entry.mu.Unlock()
	}
	if entry != nil && entry.browser != nil {
		entry.browser.publish(sink, redaction, region)
	}
}
