package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Partition progress carries a trusted group dimension, unlike global action
// rollups. Filter only those rows and retain their source qualification.
func messagingProgressForGroups(snapshot *protocol.MessagingProgressSnapshot, groups []string) *protocol.MessagingProgressSnapshot {
	if snapshot == nil {
		return nil
	}
	out := snapshot.Clone()
	selected := selectedGroupNameSet(groups)
	out.Rows = out.Rows[:0]
	for _, row := range snapshot.Clone().Rows {
		if _, ok := selected[row.Group]; ok {
			out.Rows = append(out.Rows, row)
		}
	}
	return out
}

// This native analytical operation preserves exact cursors and qualification.
// It does not admit arbitrary SQL or place unqualified clocks on an overlay.
type MessagingProgressAnalysisResponse struct {
	CatalogVersion    string                              `json:"catalog_version"`
	Mode              string                              `json:"mode"`
	Interpretation    string                              `json:"interpretation"`
	Snapshot          *protocol.MessagingProgressSnapshot `json:"snapshot,omitempty"`
	Lag               []MessagingProgressLag              `json:"lag"`
	UnavailableReason string                              `json:"unavailable_reason,omitempty"`
}
type MessagingProgressLag struct {
	CapturedAt time.Time `json:"captured_at"`
	Origin     string    `json:"origin,omitempty"`
	Group      string    `json:"group,omitempty"`
	SourceID   string    `json:"source_id"`
	SessionID  string    `json:"session_id"`
	Partition  string    `json:"partition"`
	Generation string    `json:"generation"`
	Delivered  *string   `json:"delivered"`
	Checkpoint *string   `json:"checkpoint"`
}

func messagingProgressAnalysis(snapshot *protocol.MessagingProgressSnapshot, mode, group, source string) MessagingProgressAnalysisResponse {
	out := MessagingProgressAnalysisResponse{CatalogVersion: analysis.CatalogVersion, Mode: mode,
		Interpretation: "Cumulative counters per ownership generation. Exact sequence backlog as of source capture; transit time and cross-host alignment are unqualified. Missing evidence is unavailable. Aggregate omitted-row counts are lower bounds.", Lag: []MessagingProgressLag{}}
	if snapshot == nil {
		out.UnavailableReason = "no_retained_processor_evidence"
		return out
	}
	out.Snapshot = snapshot.Clone()
	rows := out.Snapshot.Rows
	out.Snapshot.Rows = make([]protocol.MessagingProgressRow, 0, len(rows))
	for _, row := range rows {
		if group != "" && row.Group != group || source != "" && row.SourceID != source {
			continue
		}
		out.Snapshot.Rows = append(out.Snapshot.Rows, row)
		lag := MessagingProgressLag{Origin: row.Origin, Group: row.Group, SourceID: row.SourceID, SessionID: row.SessionID, Partition: row.Partition, Generation: row.Generation, CapturedAt: row.CapturedAt}
		if !out.Snapshot.Incomplete {
			if value, ok := row.QualifiedLag("delivered", 30000); ok {
				lag.Delivered = &value
			}
			if value, ok := row.QualifiedLag("checkpoint", 30000); ok {
				lag.Checkpoint = &value
			}
		}
		out.Lag = append(out.Lag, lag)
	}
	return out
}
func (s *Server) handleMessagingProgress(w http.ResponseWriter, r *http.Request) {
	mode, group, source := r.URL.Query().Get("mode"), r.URL.Query().Get("group"), r.URL.Query().Get("source_id")
	if mode == "" {
		mode = "live"
	}
	if mode != "live" && mode != "archive" || len(group) > 256 || len(source) > 256 {
		writeError(w, 400, "choose live or archive with bounded group and source identity")
		return
	}
	if !admitAnalysisPipeline(w) {
		return
	}
	defer func() { <-analysisPipelineJobs }()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var snapshot *protocol.MessagingProgressSnapshot
	if mode == "live" {
		if s.runner == nil {
			writeError(w, 409, "live evidence unavailable")
			return
		}
		var ok bool
		var projectID string
		snapshot, projectID, ok = s.runner.latestMessagingProgress(r.PathValue("id"))
		if !ok {
			writeError(w, 404, "run not found")
			return
		}
		if !requireAuthorizedProjectMatch(w, r, projectID) {
			return
		}

	} else {
		record, ok := s.archiveDomainService().GetRunArchiveContext(ctx, r.PathValue("id"))
		if !ok {
			writeError(w, 409, "completed archive unavailable")
			return
		}
		lease, err := acquireArchiveMaterialized(ctx, record)
		if err != nil {
			writeError(w, 422, "archive evidence unavailable")
			return
		}
		defer lease.Release()
		manifest, err := loadRunArchiveManifestFromRecord(record)
		if err != nil {
			writeError(w, 422, "archive identity unavailable")
			return
		}
		if !requireAuthorizedProjectMatch(w, r, manifest.Provenance.ProjectID) {
			return
		}
		snapshot, err = readArchiveMessagingProgress(ctx, record)
		if err != nil {
			writeError(w, 422, "processor evidence unavailable or exceeds bounded archive limits")
			return
		}
	}
	writeJSON(w, 200, messagingProgressAnalysis(snapshot, mode, group, source))
}
func readArchiveMessagingProgress(ctx context.Context, record *storage.RunArchiveRecord) (*protocol.MessagingProgressSnapshot, error) {
	if record == nil {
		return nil, errors.New("archive unavailable")
	}
	var snapshot *protocol.MessagingProgressSnapshot
	err := forEachArchiveRecordContext(ctx, record, func(m *RunArchiveManifest) string { return m.IntervalSummariesFile }, func(row *IntervalSummaryRecord) error {
		if row.MessagingProgress != nil {
			protocol.MergeMessagingProgress(&snapshot, row.MessagingProgress)
		}
		return nil
	})
	return snapshot, err
}

// Called under the runner lock. Immutable admitted processor claims establish
// the expected peers; HTTP and producer-only peers do not create false gaps.
func qualifyExpectedMessagingProgress(test *ManagedTest, live *LiveMetrics, batches map[string]generatorrpc.MetricsBatch) {
	if test == nil || live == nil {
		return
	}
	for _, shard := range test.GeneratorShards {
		if shard.RuntimeResourceClaim == nil || shard.RuntimeResourceClaim.Requested == nil || shard.RuntimeResourceClaim.Requested.Processors <= 0 || remoteTargetStateIntentionallyRemoved(test.remoteTargetStates[shard.Target]) {
			continue
		}
		batch, present := batches[shard.Target]
		if present && batch.MessagingProgress != nil {
			continue
		}
		reason := "source_missing"
		if present && batch.MessagingProgressVersion == 0 {
			reason = "legacy_peer"
		}
		if live.MessagingProgress == nil {
			live.MessagingProgress = protocol.NewUnavailableMessagingProgress(reason)
		} else {
			live.MessagingProgress.Incomplete = true
			live.MessagingProgress.UnavailableReason = reason
		}
	}
}

// Scan the already-bounded resident history under the runner lock and clone
// only the requested sidecar. A later unrelated interval cannot erase the last
// captured terminal processor evidence or force hydration of all history.
func (tr *TestRunner) latestMessagingProgress(id string) (*protocol.MessagingProgressSnapshot, string, bool) {
	identity, ok := tr.getTestSnapshot(id, false)
	if !ok {
		return nil, "", false
	}
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	test := tr.tests[identity.ID]
	if !sameStoredTestIdentity(test, &storage.TestRecord{ID: identity.ID, ProjectID: identity.ProjectID, StartTime: identity.CreatedAt, InitialProvenance: identity.initialArchiveProvenance}) {
		// A stale local execution handle may remain for teardown after an HA
		// delete/recreate. It cannot disclose evidence for the replacement row.
		if identity.LiveMetrics != nil {
			return identity.LiveMetrics.MessagingProgress.Clone(), identity.ProjectID, true
		}
		return nil, identity.ProjectID, true
	}
	if test.LiveMetrics != nil && test.LiveMetrics.MessagingProgress != nil {
		return test.LiveMetrics.MessagingProgress.Clone(), identity.ProjectID, true
	}
	for index := len(test.History) - 1; index >= 0; index-- {
		if point := test.History[index]; point != nil && point.MessagingProgress != nil {
			return point.MessagingProgress.Clone(), identity.ProjectID, true
		}
	}
	return nil, identity.ProjectID, true
}
