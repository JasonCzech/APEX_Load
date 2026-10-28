package api

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

type byteProtocolBucketState struct {
	values     [][]int64
	missing    []string
	excluded   map[string]int64
	identities map[string]bool
	selected   int
	evidenced  int
}

func newByteProtocolBucketState(numBuckets int) *byteProtocolBucketState {
	return &byteProtocolBucketState{
		values:     make([][]int64, numBuckets),
		missing:    make([]string, numBuckets),
		excluded:   map[string]int64{},
		identities: map[string]bool{},
	}
}

func (s *byteProtocolBucketState) hasActivity() bool {
	return s != nil && (s.selected > 0 || s.excluded["no_request"] > 0 || s.excluded["byte_evidence_unavailable"] > 0 || s.excluded["invalid_byte_count"] > 0)
}

func (s *byteProtocolBucketState) hasEvidencedActivity() bool {
	return s != nil && s.evidenced > 0
}

func extractArchiveTransactionBytes(tx *TransactionEvent, targetProtocol string, received bool) int64 {
	if tx == nil {
		return 0
	}
	if targetProtocol == "grpc" && tx.ByteEvidence == protocol.ByteEvidenceGRPCUncompressedPayload && tx.ServerTelemetry != nil && tx.ServerTelemetry.GRPC != nil && tx.ServerTelemetry.GRPC.Valid() {
		if received {
			return tx.ServerTelemetry.GRPC.ResponseBytes
		}
		return tx.ServerTelemetry.GRPC.RequestBytes
	}
	if received {
		return tx.BytesReceived
	}
	return tx.BytesSent
}

// buildNativeByteSeries reads protocol payload/body bytes (HTTP http_body.v1,
// gRPC grpc_uncompressed_payload.v1, WebSocket ws_application_payload.v1) from
// the archive's raw observations (NA-C). Each observation's count is a
// measurement only when its adapter set the protocol's byte evidence; an
// observation archived without it (by an earlier binary) is unavailable, and
// its bucket is unscored with the exclusion counted. A step that dispatched no
// transfer (evidence "none") is not in the population. Observations are
// bucketed by their start. A sum or a rate needs a complete bucket: every
// selected observation evidenced, the archive's collection complete and
// required, and the bucket ended by the run's completion; otherwise it is
// unscored, never a partial total. The per-observation distribution reads every
// observation of a fully evidenced bucket. A bucket without observations has no
// value.
func buildNativeByteSeries(ctx context.Context, record *storage.RunArchiveRecord, manifest *RunArchiveManifest, q storage.AnalyticsQuery, result NativeArchiveSeries, from, to time.Time) (NativeArchiveSeries, error) {
	definition, _ := analysis.LookupMetric(q.Metric)
	result.Unit = definition.Unit
	result.Excluded = map[string]int64{}
	received := q.Metric == "bytes_received" || q.Metric == "bytes_received_rate"
	explicitProtocol := strings.ToLower(strings.TrimSpace(q.Filters["protocol"]))
	if explicitProtocol != "" && explicitProtocol != "http" && explicitProtocol != "grpc" && explicitProtocol != "websocket" {
		return result, errors.New("byte queries filter protocol only by http, grpc, or websocket")
	}
	states := map[string]*byteProtocolBucketState{
		"http":      newByteProtocolBucketState(len(result.Buckets)),
		"grpc":      newByteProtocolBucketState(len(result.Buckets)),
		"websocket": newByteProtocolBucketState(len(result.Buckets)),
	}
	err := forEachArchiveTransactionContext(ctx, record, func(tx *TransactionEvent) error {
		if tx.Timestamp.Before(from) || !tx.Timestamp.Before(to) || tx.BusinessTransaction || archiveEventPopulation(*tx) != "action" {
			return nil
		}
		proto := strings.ToLower(strings.TrimSpace(tx.DependencyType))
		state, supported := states[proto]
		if !supported {
			return nil
		}
		if explicitProtocol != "" && proto != explicitProtocol {
			return nil
		}
		if action := q.Filters["action"]; action != "" && tx.ActionName != action {
			return nil
		}
		if tx.ByteEvidence == protocol.ByteEvidenceNone {
			state.evidenced++
			state.excluded["no_request"]++
			return nil
		}
		if !state.identities[tx.ActionName] && len(state.identities) >= 1000 {
			return errors.New("identity limit exceeded; select an action")
		}
		state.identities[tx.ActionName] = true
		if state.selected >= 100000 {
			return errors.New("raw observation limit exceeded; narrow the window or filters")
		}
		state.selected++
		if tx.ByteEvidence != "" {
			state.evidenced++
		}
		i := int(tx.Timestamp.Sub(from)/time.Second) / q.BucketSeconds
		if i < 0 || i >= len(result.Buckets) {
			return nil
		}
		value := extractArchiveTransactionBytes(tx, proto, received)
		expectedEvidence, _ := protocol.ExpectedByteEvidenceForProtocol(proto)
		refuse := func(reason string) {
			if state.missing[i] == "" {
				state.missing[i] = reason
			}
			state.excluded[reason]++
		}
		switch {
		case tx.ByteEvidence != expectedEvidence:
			refuse("byte_evidence_unavailable")
		case value < 0:
			refuse("invalid_byte_count")
		default:
			state.values[i] = append(state.values[i], value)
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	targetProtocol := explicitProtocol
	if targetProtocol == "" {
		evidencedCount := 0
		for _, proto := range []string{"http", "grpc", "websocket"} {
			if states[proto].hasEvidencedActivity() {
				evidencedCount++
				targetProtocol = proto
			}
		}
		if evidencedCount > 1 {
			return result, errors.New("multiple byte-measured protocols present; specify filters.protocol (http, grpc, or websocket)")
		}
		if evidencedCount == 0 {
			activeCount := 0
			for _, proto := range []string{"http", "grpc", "websocket"} {
				if states[proto].hasActivity() {
					activeCount++
					targetProtocol = proto
				}
			}
			if activeCount > 1 {
				return result, errors.New("multiple byte-measured protocols present; specify filters.protocol (http, grpc, or websocket)")
			}
			if activeCount == 0 {
				targetProtocol = "http"
			}
		}
	}
	chosen := states[targetProtocol]
	result.Excluded = chosen.excluded
	values := chosen.values
	missing := chosen.missing
	identities := chosen.identities
	selected := chosen.selected

	// Totals need every source's requests: a missing or best-effort source
	// leaves them unproven.
	collected := !archiveCollectionIncomplete(manifest) && (manifest.SourceCoverage == nil || manifest.SourceCoverage.Mode == "required")
	completedAt := archiveExecutionCompletedAt(manifest)
	unscored := false
	for i, v := range values {
		b := &result.Buckets[i]
		b.Count = len(v)
		b.complete = missing[i] == "" && collected && !b.To.After(completedAt)
		if len(v) == 0 && missing[i] == "" {
			continue
		}
		if missing[i] != "" {
			b.Reason, unscored = missing[i], true
			continue
		}
		var value float64
		switch q.Aggregation {
		case "sum", "rate":
			if !b.complete {
				b.Reason, unscored = "incomplete_bucket", true
				continue
			}
			var sum int64
			for _, x := range v {
				if x > 1<<53-1-sum {
					sum = -1
					break
				}
				sum += x
			}
			if sum < 0 {
				b.Reason, unscored = "exact_sum_limit", true
				continue
			}
			value = float64(sum)
			if q.Aggregation == "rate" {
				value /= b.To.Sub(b.From).Seconds()
			}
		default:
			sorted := append([]int64(nil), v...)
			sort.Slice(sorted, func(a, b int) bool { return sorted[a] < sorted[b] })
			switch q.Aggregation {
			case "avg":
				// Whole byte counts add exactly well past any bucket's
				// requests; the one division rounds once.
				for _, x := range sorted {
					value += float64(x)
				}
				value /= float64(len(sorted))
			case "min":
				value = float64(sorted[0])
			case "max":
				value = float64(sorted[len(sorted)-1])
			default:
				p := map[string]float64{"p50": .50, "p90": .90, "p95": .95, "p99": .99}[q.Aggregation]
				value = float64(sorted[int(math.Ceil(p*float64(len(sorted))))-1])
			}
		}
		b.Value = &value
	}
	for id := range identities {
		result.Identities = append(result.Identities, id)
	}
	sort.Strings(result.Identities)
	noun := "HTTP requests"
	switch targetProtocol {
	case "grpc":
		noun = "gRPC actions"
	case "websocket":
		noun = "WebSocket messages"
	}
	if selected == 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf("No matching %s; missing evidence is not zero.", noun))
	}
	if result.Excluded["byte_evidence_unavailable"] > 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf("Some %s were archived without byte evidence (by an earlier version); their buckets are unscored.", noun))
	}
	if result.Excluded["invalid_byte_count"] > 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf("Some %s carry an invalid byte count; their buckets are unscored.", noun))
	}
	if !collected {
		result.Warnings = append(result.Warnings, fmt.Sprintf("The archive does not hold every source's %s; sums and rates are unscored and distributions describe the collected observations only.", noun))
	} else if unscored {
		result.Warnings = append(result.Warnings, "Some buckets are unscored; their reason says why.")
	}
	if to.After(completedAt) {
		result.Warnings = append(result.Warnings, "The requested window extends past run completion.")
	}
	return result, nil
}

func buildNativeGroupedByteArchive(ctx context.Context, record *storage.RunArchiveRecord, manifest *RunArchiveManifest, q storage.AnalyticsQuery) (NativeEvidenceResponse, error) {
	out := nativeEvidenceEnvelope(*manifest.Provenance.StartedAt, q)
	n := (q.EndSeconds - q.StartSeconds + q.BucketSeconds - 1) / q.BucketSeconds
	received := q.Metric == "bytes_received" || q.Metric == "bytes_received_rate"
	explicitProtocol := strings.ToLower(strings.TrimSpace(q.Filters["protocol"]))
	type bucket struct {
		values  []int64
		missing string
	}
	type protoGroupState struct {
		buckets   []bucket
		selected  int
		evidenced int
	}
	groupStates := map[string]map[string]*protoGroupState{}
	totalSelected := 0
	unscored := false
	err := forEachArchiveTransactionContext(ctx, record, func(tx *TransactionEvent) error {
		if tx.Timestamp.Before(out.From) || !tx.Timestamp.Before(out.To) || tx.BusinessTransaction || archiveEventPopulation(*tx) != "action" {
			return nil
		}
		proto := strings.ToLower(strings.TrimSpace(tx.DependencyType))
		expectedEv, supported := protocol.ExpectedByteEvidenceForProtocol(proto)
		if !supported {
			return nil
		}
		if explicitProtocol != "" && proto != explicitProtocol {
			return nil
		}
		if action := q.Filters["action"]; action != "" && tx.ActionName != action {
			return nil
		}
		if tx.ByteEvidence == protocol.ByteEvidenceNone {
			return nil
		}
		name := "Selection"
		if nativeGroup(q) == "action" {
			name = tx.ActionName
		}
		if name == "" || len(name) > 256 {
			return errors.New("group identity exceeds limit")
		}
		byProto, ok := groupStates[name]
		if !ok {
			if len(groupStates) >= q.MaxSeries {
				return errors.New("series limit exceeded; narrow the selection")
			}
			byProto = map[string]*protoGroupState{}
			groupStates[name] = byProto
		}
		st, ok := byProto[proto]
		if !ok {
			st = &protoGroupState{buckets: make([]bucket, n)}
			byProto[proto] = st
		}
		if totalSelected >= 100000 {
			return errors.New("raw observation limit exceeded; narrow the selection")
		}
		totalSelected++
		st.selected++
		if tx.ByteEvidence != "" {
			st.evidenced++
		}
		i := int(tx.Timestamp.Sub(out.From)/time.Second) / q.BucketSeconds
		if i < 0 || i >= n {
			return nil
		}
		b := &st.buckets[i]
		val := extractArchiveTransactionBytes(tx, proto, received)
		switch {
		case tx.ByteEvidence != expectedEv:
			if b.missing == "" {
				b.missing = "byte_evidence_unavailable"
			}
		case val < 0:
			if b.missing == "" {
				b.missing = "invalid_byte_count"
			}
		default:
			b.values = append(b.values, val)
		}
		return nil
	})
	if err != nil {
		return out, err
	}
	keys := make([]string, 0, len(groupStates))
	for k := range groupStates {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	groups := make(map[string][]bucket, len(groupStates))
	selected := 0
	for _, key := range keys {
		byProto := groupStates[key]
		targetProto := explicitProtocol
		if targetProto == "" {
			evidencedCount := 0
			for _, proto := range []string{"http", "grpc", "websocket"} {
				if st := byProto[proto]; st != nil && st.evidenced > 0 {
					evidencedCount++
					targetProto = proto
				}
			}
			if evidencedCount > 1 {
				return out, errors.New("multiple byte-measured protocols present in series; specify filters.protocol")
			}
			if evidencedCount == 0 {
				activeCount := 0
				for _, proto := range []string{"http", "grpc", "websocket"} {
					if st := byProto[proto]; st != nil && st.selected > 0 {
						activeCount++
						targetProto = proto
					}
				}
				if activeCount > 1 {
					return out, errors.New("multiple byte-measured protocols present in series; specify filters.protocol")
				}
			}
		}
		if st := byProto[targetProto]; st != nil {
			groups[key] = st.buckets
			selected += st.selected
		}
	}
	collected := !archiveCollectionIncomplete(manifest) && (manifest.SourceCoverage == nil || manifest.SourceCoverage.Mode == "required")
	completedAt := archiveExecutionCompletedAt(manifest)
	for _, key := range keys {
		series := NativeEvidenceSeries{Name: key, Unit: nativeMetricUnit(q), Points: []NativeEvidencePoint{}}
		for i, b := range groups[key] {
			from := out.From.Add(time.Duration(i*q.BucketSeconds) * time.Second)
			to := from.Add(time.Duration(q.BucketSeconds) * time.Second)
			if to.After(out.To) {
				to = out.To
			}
			complete := b.missing == "" && collected && !to.After(completedAt)
			p := NativeEvidencePoint{From: from, To: to, OffsetSeconds: from.Sub(out.Start).Seconds(), Count: int64(len(b.values)), complete: complete}
			if len(b.values) == 0 && b.missing == "" {
				series.Points = append(series.Points, p)
				continue
			}
			if b.missing != "" {
				p.Reason, unscored = b.missing, true
				series.Points = append(series.Points, p)
				continue
			}
			var value float64
			switch q.Aggregation {
			case "sum", "rate":
				if !complete {
					p.Reason, unscored = "incomplete_bucket", true
					series.Points = append(series.Points, p)
					continue
				}
				var sum int64
				for _, x := range b.values {
					if x > 1<<53-1-sum {
						sum = -1
						break
					}
					sum += x
				}
				if sum < 0 {
					p.Reason, unscored = "exact_sum_limit", true
					series.Points = append(series.Points, p)
					continue
				}
				value = float64(sum)
				if q.Aggregation == "rate" {
					value /= to.Sub(from).Seconds()
				}
			default:
				sorted := append([]int64(nil), b.values...)
				sort.Slice(sorted, func(a, b int) bool { return sorted[a] < sorted[b] })
				switch q.Aggregation {
				case "avg":
					for _, x := range sorted {
						value += float64(x)
					}
					value /= float64(len(sorted))
				case "min":
					value = float64(sorted[0])
				case "max":
					value = float64(sorted[len(sorted)-1])
				default:
					pct := map[string]float64{"p50": .50, "p90": .90, "p95": .95, "p99": .99}[q.Aggregation]
					value = float64(sorted[int(math.Ceil(pct*float64(len(sorted))))-1])
				}
			}
			p.Value = &value
			series.Points = append(series.Points, p)
		}
		out.Series = append(out.Series, series)
	}
	if unscored {
		out.Warnings = append(out.Warnings, "Some byte buckets are unscored; their reason says why.")
	}
	if selected == 0 {
		out.Warnings = append(out.Warnings, "No matching raw byte observations; missing is not zero.")
	}
	return out, nil
}

func buildNativeLiveByteEvidence(ctx context.Context, start time.Time, history []*LiveMetrics, q storage.AnalyticsQuery) (NativeEvidenceResponse, error) {
	out := nativeEvidenceEnvelope(start, q)
	received := q.Metric == "bytes_received" || q.Metric == "bytes_received_rate"
	explicitProtocol := strings.ToLower(strings.TrimSpace(q.Filters["protocol"]))
	var expectedEv string
	if explicitProtocol != "" {
		var ok bool
		expectedEv, ok = protocol.ExpectedByteEvidenceForProtocol(explicitProtocol)
		if !ok {
			return out, errors.New("byte queries filter protocol only by http, grpc, or websocket")
		}
	}
	groupedByAction := nativeGroup(q) == "action"
	matchingActions := map[string]bool{}
	if expectedEv != "" {
		if action := q.Filters["action"]; action != "" {
			matchingActions[action] = true
		}
		for _, h := range history {
			if h == nil || h.Timestamp.Before(out.From) || !h.Timestamp.Before(out.To) || h.NativeEvidence == nil {
				continue
			}
			for name, m := range h.NativeEvidence.Dimensions["action"] {
				if q.Filters["action"] != "" && name != q.Filters["action"] {
					continue
				}
				if m.ByteEvidence == expectedEv {
					matchingActions[name] = true
				}
			}
		}
	}
	names := map[string]bool{}
	intervals := []*LiveMetrics{}
	available := false
	for _, h := range history {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if h == nil || h.Timestamp.Before(out.From) || !h.Timestamp.Before(out.To) {
			continue
		}
		intervals = append(intervals, h)
		e := h.NativeEvidence
		if e == nil || (e.Version < 1 || e.Version > 3) || e.WindowSeconds <= 0 || e.Incomplete {
			continue
		}
		actions, ok := e.Dimensions["action"]
		if !ok {
			continue
		}
		available = true
		for name, m := range actions {
			if q.Filters["action"] != "" && name != q.Filters["action"] {
				continue
			}
			if m.ByteEvidence == protocol.ByteEvidenceNone {
				continue
			}
			if expectedEv != "" && !matchingActions[name] {
				continue
			}
			seriesName := "Selection"
			if groupedByAction || q.Filters["action"] != "" {
				seriesName = name
			}
			names[seriesName] = true
		}
	}
	if len(intervals) > 0 && !available {
		return out, errors.New("selected live interval evidence is unavailable or incomplete; the peer/runtime must support native evidence v1")
	}
	if len(names) > q.MaxSeries {
		return out, errors.New("series limit exceeded; narrow the selection or raise max_series (up to 20)")
	}
	keys := make([]string, 0, len(names))
	for name := range names {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	unavailable := false
	for _, seriesName := range keys {
		series := NativeEvidenceSeries{Name: seriesName, Unit: nativeMetricUnit(q), Points: []NativeEvidencePoint{}}
		for _, h := range intervals {
			p := NativeEvidencePoint{From: h.Timestamp, To: h.Timestamp, OffsetSeconds: h.Timestamp.Sub(start).Seconds()}
			e := h.NativeEvidence
			if e != nil && (e.Version >= 1 && e.Version <= 3) && e.WindowSeconds > 0 && !e.Incomplete {
				p.From = h.Timestamp.Add(-time.Duration(e.WindowSeconds * float64(time.Second)))
				p.complete = true
				var sumBytes, sumSamples int64
				var bucketEv string
				var reason string
				for actionName, m := range e.Dimensions["action"] {
					if q.Filters["action"] != "" && actionName != q.Filters["action"] {
						continue
					}
					if groupedByAction && actionName != seriesName {
						continue
					}
					if m.Count <= 0 || m.ByteEvidence == protocol.ByteEvidenceNone {
						continue
					}
					if expectedEv != "" && !matchingActions[actionName] {
						continue
					}
					if (expectedEv != "" && m.ByteEvidence != expectedEv) || !protocol.IsMeasuredByteEvidence(m.ByteEvidence) || m.ByteSamples <= 0 {
						reason = "byte_evidence_unavailable"
						break
					}
					if bucketEv == "" {
						bucketEv = m.ByteEvidence
					} else if bucketEv != m.ByteEvidence {
						reason = "byte_evidence_unavailable"
						break
					}
					val := m.BytesSent
					if received {
						val = m.BytesReceived
					}
					if val < 0 {
						reason = "invalid_byte_count"
						break
					}
					if val > 1<<53-1-sumBytes {
						reason = "exact_sum_limit"
						break
					}
					sumBytes += val
					sumSamples += m.ByteSamples
				}
				if reason != "" {
					p.Reason = reason
					p.complete = false
					unavailable = true
				} else if sumSamples > 0 {
					value := float64(sumBytes)
					if q.Aggregation == "rate" {
						value /= e.WindowSeconds
					}
					p.Value = &value
					p.Count = sumSamples
				}
			} else {
				p.Reason = "incomplete_bucket"
				unavailable = true
			}
			series.Points = append(series.Points, p)
		}
		out.Series = append(out.Series, series)
	}
	if unavailable || len(keys) == 0 {
		out.Warnings = append(out.Warnings, "Some interval byte evidence is unavailable, incomplete, or from an unsupported peer. No totals are substituted.")
	}
	out.Warnings = append(out.Warnings, "Retained publication intervals, labelled by end time; live interval byte totals report only sum and rate over complete windows.")
	return out, nil
}

func buildMergedNativeLiveByteEvidence(ctx context.Context, start time.Time, history []*LiveMetrics, q storage.AnalyticsQuery) (NativeEvidenceResponse, error) {
	out := nativeEvidenceEnvelope(start, q)
	out.LiveBucketMode = "merged"
	n := (q.EndSeconds - q.StartSeconds + q.BucketSeconds - 1) / q.BucketSeconds
	received := q.Metric == "bytes_received" || q.Metric == "bytes_received_rate"
	explicitProtocol := strings.ToLower(strings.TrimSpace(q.Filters["protocol"]))
	var expectedEv string
	if explicitProtocol != "" {
		var ok bool
		expectedEv, ok = protocol.ExpectedByteEvidenceForProtocol(explicitProtocol)
		if !ok {
			return out, errors.New("byte queries filter protocol only by http, grpc, or websocket")
		}
	}
	groupedByAction := nativeGroup(q) == "action"
	matchingActions := map[string]bool{}
	if expectedEv != "" {
		if action := q.Filters["action"]; action != "" {
			matchingActions[action] = true
		}
		for _, h := range history {
			if h == nil || h.NativeEvidence == nil {
				continue
			}
			timestamp := h.Timestamp
			dims := h.NativeEvidence.Dimensions
			if p := h.NativeEvidence.Publication; p != nil {
				timestamp = p.Timestamp
				dims = p.Dimensions
			}
			if !timestamp.After(out.From) || timestamp.After(out.To) {
				continue
			}
			for name, m := range dims["action"] {
				if q.Filters["action"] != "" && name != q.Filters["action"] {
					continue
				}
				if m.ByteEvidence == expectedEv {
					matchingActions[name] = true
				}
			}
		}
	}
	type byteAggregate struct {
		samples  int64
		bytes    int64
		evidence string
		reason   string
	}
	groups := map[string][]byteAggregate{}
	invalid := make([]bool, n)
	type sourceWindow struct{ from, to time.Time }
	windows := make([]map[string][]sourceWindow, n)
	rateUnsafe := make([]bool, n)
	expected := make([]map[string]bool, n)
	for i := range windows {
		windows[i] = map[string][]sourceWindow{}
		expected[i] = map[string]bool{}
	}
	seen := map[string]bool{}
	supported := false
	publications := 0
	for _, h := range history {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if h == nil {
			continue
		}
		timestamp := h.Timestamp
		e := h.NativeEvidence
		source := "local"
		stamp := fmt.Sprintf("local/%d", timestamp.UnixNano())
		sources := []string{"local"}
		distributed := false
		if e != nil && e.Publication != nil {
			p := e.Publication
			timestamp = p.Timestamp
			source = p.SourceID
			if source == "" || len(source) > 512 || p.Sequence == 0 || len(p.ExpectedSources) > 128 {
				return out, errors.New("invalid source publication identity")
			}
			stamp = nativePublicationIdentity(p)
			sources = p.ExpectedSources
			distributed = true
			e = &storage.NativeIntervalEvidence{Version: p.Version, WindowSeconds: p.WindowSeconds, Dimensions: p.Dimensions, Incomplete: p.Incomplete, Joint: p.Joint}
		}
		if !timestamp.After(out.From) || timestamp.After(out.To) {
			continue
		}
		if seen[stamp] {
			if distributed {
				continue
			}
			return out, errors.New("duplicate publication timestamp")
		}
		seen[stamp] = true
		i := int((timestamp.Sub(out.From) - time.Nanosecond) / (time.Duration(q.BucketSeconds) * time.Second))
		if i < 0 || i >= n {
			continue
		}
		publications++
		expected[i][source] = true
		for _, id := range sources {
			if id == "" || len(id) > 512 {
				return out, errors.New("invalid expected source identity")
			}
			expected[i][id] = true
		}
		if e == nil || e.Version < 1 || e.Version > 3 || e.Incomplete || math.IsNaN(e.WindowSeconds) || math.IsInf(e.WindowSeconds, 0) || e.WindowSeconds <= 0 || e.WindowSeconds > 3600 {
			invalid[i] = true
			continue
		}
		actions, ok := e.Dimensions["action"]
		if !ok {
			invalid[i] = true
			continue
		}
		supported = true
		window := sourceWindow{from: timestamp.Add(-time.Duration(math.Round(e.WindowSeconds * float64(time.Second)))), to: timestamp}
		windows[i][source] = append(windows[i][source], window)
		bucketFrom := out.From.Add(time.Duration(i*q.BucketSeconds) * time.Second)
		bucketTo := bucketFrom.Add(time.Duration(q.BucketSeconds) * time.Second)
		if bucketTo.After(out.To) {
			bucketTo = out.To
		}
		if window.from.Before(bucketFrom) || window.to.After(bucketTo) {
			rateUnsafe[i] = true
		}
		for actionName, m := range actions {
			if q.Filters["action"] != "" && actionName != q.Filters["action"] {
				continue
			}
			if m.ByteEvidence == protocol.ByteEvidenceNone {
				continue
			}
			if expectedEv != "" && !matchingActions[actionName] {
				continue
			}
			name := "Selection"
			if groupedByAction {
				name = actionName
			}
			if _, ok := groups[name]; !ok {
				if len(groups) >= q.MaxSeries {
					return out, errors.New("series limit exceeded; narrow the selection")
				}
				groups[name] = make([]byteAggregate, n)
			}
			b := &groups[name][i]
			if m.Count <= 0 {
				continue
			}
			if b.reason != "" {
				continue
			}
			if (expectedEv != "" && m.ByteEvidence != expectedEv) || !protocol.IsMeasuredByteEvidence(m.ByteEvidence) || m.ByteSamples <= 0 {
				b.reason = "byte_evidence_unavailable"
				continue
			}
			if b.evidence == "" {
				b.evidence = m.ByteEvidence
			} else if b.evidence != m.ByteEvidence {
				b.reason = "byte_evidence_unavailable"
				continue
			}
			val := m.BytesSent
			if received {
				val = m.BytesReceived
			}
			if val < 0 {
				b.reason = "invalid_byte_count"
				continue
			}
			if val > 1<<53-1-b.bytes {
				b.reason = "exact_sum_limit"
				continue
			}
			b.bytes += val
			b.samples += m.ByteSamples
		}
	}
	if publications > 0 && !supported {
		return out, errors.New("merged live byte queries require native evidence from every contributing runtime")
	}
	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)
	partial := false
	for _, name := range names {
		series := NativeEvidenceSeries{Name: name, Unit: nativeMetricUnit(q)}
		for i, b := range groups[name] {
			from := out.From.Add(time.Duration(i*q.BucketSeconds) * time.Second)
			to := from.Add(time.Duration(q.BucketSeconds) * time.Second)
			if to.After(out.To) {
				to = out.To
			}
			coverage := 0.0
			if len(expected[i]) > 0 {
				coverage = 1
				for source := range expected[i] {
					intervals := windows[i][source]
					if len(intervals) == 0 {
						invalid[i] = true
					}
					sort.Slice(intervals, func(a, b int) bool { return intervals[a].from.Before(intervals[b].from) })
					covered := time.Duration(0)
					coveredTo := from
					lastEnd := time.Time{}
					for _, interval := range intervals {
						if !lastEnd.IsZero() && interval.from.Before(lastEnd) {
							rateUnsafe[i] = true
						}
						if interval.to.After(lastEnd) {
							lastEnd = interval.to
						}
						clippedFrom, clippedTo := interval.from, interval.to
						if clippedFrom.Before(coveredTo) {
							clippedFrom = coveredTo
						}
						if clippedTo.After(to) {
							clippedTo = to
						}
						if clippedTo.After(clippedFrom) {
							covered += clippedTo.Sub(clippedFrom)
							coveredTo = clippedTo
						}
					}
					coverage = math.Min(coverage, math.Min(1, float64(covered)/float64(to.Sub(from))))
				}
			}
			completeBucket := len(expected[i]) > 0 && !invalid[i] && b.reason == "" && coverage >= 1 && !rateUnsafe[i]
			p := NativeEvidencePoint{
				From:          from,
				To:            to,
				OffsetSeconds: from.Sub(start).Seconds(),
				Coverage:      &coverage,
				complete:      completeBucket,
			}
			if b.reason != "" {
				p.Reason = b.reason
				partial = true
			} else if (b.samples > 0 || len(expected[i]) > 0) && (!completeBucket) {
				p.Reason = "incomplete_bucket"
				partial = true
			} else if b.samples > 0 && completeBucket {
				value := float64(b.bytes)
				if q.Aggregation == "rate" {
					value /= to.Sub(from).Seconds()
				}
				p.Value = &value
				p.Count = b.samples
			}
			series.Points = append(series.Points, p)
		}
		out.Series = append(out.Series, series)
	}
	if partial {
		out.Warnings = append(out.Warnings, "Collection coverage or byte evidence is partial in some buckets; incomplete or unmeasured cohorts remain unscored.")
	}
	out.Warnings = append(out.Warnings, "Merged byte sums and rates require nonoverlapping source windows wholly contained in each bucket and complete collection coverage.")
	return out, nil
}

func isAnalysisByteMetric(metric string) bool {
	switch strings.TrimSpace(metric) {
	case "bytes_sent", "bytes_received", "bytes_sent_rate", "bytes_received_rate":
		return true
	default:
		return false
	}
}

func (s *Server) loadRunByteAnalysisSelection(ctx context.Context, project string, selection AnalysisSeriesSelection, q AnalysisComparisonRequest) ([]analysis.MetricPoint, string, error) {
	agg := strings.TrimSpace(selection.Aggregation)
	if agg == "" {
		if selection.Metric == "bytes_sent_rate" || selection.Metric == "bytes_received_rate" {
			agg = "rate"
		} else {
			agg = "sum"
		}
	}
	if err := analysis.ValidateMetricAggregation(selection.Metric, analysis.PopulationByte, agg); err != nil {
		return nil, "", err
	}
	filters := map[string]string{}
	if selection.Transaction != "" {
		filters["action"] = selection.Transaction
	}
	if selection.Protocol != "" {
		proto := strings.ToLower(strings.TrimSpace(selection.Protocol))
		if proto != "http" && proto != "grpc" && proto != "websocket" {
			return nil, "", errors.New("byte queries filter protocol only by http, grpc, or websocket")
		}
		filters["protocol"] = proto
	}
	record, hasArchive := s.archiveDomainService().GetRunArchiveContext(ctx, selection.RunID)
	if hasArchive {
		manifest, err := loadRunArchiveManifestFromRecord(record)
		if err != nil {
			return nil, "", err
		}
		bucketDur := time.Duration(q.BucketSeconds) * time.Second
		numBuckets := int((q.To.Sub(q.From) + bucketDur - 1) / bucketDur)
		base := NativeArchiveSeries{
			RunID:   selection.RunID,
			Buckets: make([]NativeArchiveBucket, numBuckets),
		}
		for i := 0; i < numBuckets; i++ {
			bFrom := q.From.Add(time.Duration(i) * bucketDur)
			bTo := bFrom.Add(bucketDur)
			if bTo.After(q.To) {
				bTo = q.To
			}
			base.Buckets[i] = NativeArchiveBucket{From: bFrom, To: bTo}
		}
		aq := storage.AnalyticsQuery{
			Population:    analysis.PopulationByte,
			Metric:        selection.Metric,
			Aggregation:   agg,
			BucketSeconds: q.BucketSeconds,
			Filters:       filters,
		}
		series, err := buildNativeByteSeries(ctx, record, manifest, aq, base, q.From, q.To)
		if err != nil {
			return nil, "", err
		}
		if q.sampleExclusions != nil && len(series.Excluded) > 0 {
			excl := make(map[string]int, len(series.Excluded))
			for k, v := range series.Excluded {
				excl[k] = int(v)
			}
			q.sampleExclusions[analysisSelectionKey(selection)] = excl
		}
		points := make([]analysis.MetricPoint, 0, len(series.Buckets))
		for _, b := range series.Buckets {
			if b.Count == 0 && b.Value == nil && b.Reason == "" {
				continue
			}
			quality := analysis.QualityValid
			if b.Value == nil {
				quality = analysis.QualityMissing
			}
			points = append(points, analysis.MetricPoint{
				SchemaVersion: analysis.SchemaVersion,
				ProjectID:     project,
				RunID:         selection.RunID,
				Source:        "byte:" + selection.Metric,
				TimestampUTC:  b.From.UTC(),
				MetricName:    selection.Metric,
				Population:    analysis.PopulationByte,
				Kind:          analysis.KindGauge,
				InputKind:     analysis.KindObservation,
				Value:         b.Value,
				Unit:          series.Unit,
				Quality:       quality,
			})
		}
		return points, selection.RunID, nil
	}
	if agg != "sum" && agg != "rate" {
		return nil, "", errors.New("live interval byte totals cannot synthesize per-request size distributions; use sum or rate, or query completed archive observations")
	}
	if s.runner == nil {
		return nil, "", errors.New("completed run archive or live runner required for byte analysis")
	}
	live, exists := s.runner.GetTestMetricsWindow(selection.RunID, nil, 1000)
	if !exists || live.StartedAt == nil {
		return nil, "", errors.New("execution start unavailable")
	}
	startUTC := live.StartedAt.UTC()
	startSec := int(q.From.Sub(startUTC) / time.Second)
	if startSec < 0 {
		startSec = 0
	}
	endSec := int((q.To.Sub(startUTC) + time.Second - 1) / time.Second)
	if endSec <= startSec {
		endSec = startSec + q.BucketSeconds
	}
	aq := storage.AnalyticsQuery{
		EvidenceMode:   "live",
		LiveBucketMode: "merged",
		Population:     analysis.PopulationByte,
		Metric:         selection.Metric,
		Aggregation:    agg,
		StartSeconds:   startSec,
		EndSeconds:     endSec,
		BucketSeconds:  q.BucketSeconds,
		MaxSeries:      1,
		Filters:        filters,
	}
	liveResp, err := buildMergedNativeLiveByteEvidence(ctx, *live.StartedAt, live.History, aq)
	if err != nil {
		return nil, "", err
	}
	var points []analysis.MetricPoint
	for _, series := range liveResp.Series {
		for _, p := range series.Points {
			if p.From.Before(q.From) || !p.From.Before(q.To) {
				continue
			}
			if p.Count == 0 && p.Value == nil && p.Reason == "" {
				continue
			}
			quality := analysis.QualityValid
			if p.Value == nil {
				quality = analysis.QualityMissing
			}
			points = append(points, analysis.MetricPoint{
				SchemaVersion: analysis.SchemaVersion,
				ProjectID:     project,
				RunID:         selection.RunID,
				Source:        "byte:" + selection.Metric,
				TimestampUTC:  p.From.UTC(),
				MetricName:    selection.Metric,
				Population:    analysis.PopulationByte,
				Kind:          analysis.KindGauge,
				InputKind:     analysis.KindObservation,
				Value:         p.Value,
				Unit:          series.Unit,
				Quality:       quality,
			})
		}
	}
	return points, selection.RunID, nil
}

