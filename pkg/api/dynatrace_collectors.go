package api

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/collectoragent"
	"github.com/jasonczech/apexload/pkg/dynatrace"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"github.com/jasonczech/apexload/pkg/storage"
)

func (s *Server) populateDynatraceGrant(ctx context.Context, grant *collectoragent.RunJob, job storage.RunMonitorJob) error {
	if job.DynatraceSnapshot == nil || job.Source.Dynatrace == nil {
		return errors.New("Dynatrace immutable snapshot unavailable")
	}
	snapshot := job.DynatraceSnapshot
	if snapshot.ProjectID != job.ProjectID {
		return errors.New("Dynatrace snapshot project mismatch")
	}
	credentials, err := resolveNativeDynatraceCredentials(ctx, s.runner.secretResolverForProject(job.ProjectID), snapshot, job.Source.Dynatrace)
	if err != nil {
		return err
	}
	cfg, err := s.dynatraceQuotaConfig(ctx, snapshot, credentials, "collector:"+job.Source.CollectorID, job.Source.Dynatrace)
	if err != nil {
		return err
	}
	selection := *job.Source.Dynatrace
	selection.ProjectID = job.ProjectID
	selection.QueryOffset = job.QueryOffset
	selection.Cursor = job.ProviderCursor
	grant.Dynatrace = &dynatrace.JobConfig{Config: cfg, Selection: selection}
	grant.QueryFromUTC, grant.QueryUntilUTC = appDynamicsQueryWindow(job, time.Now().UTC())
	grant.LoadEndedAt, grant.DrainDeadline, grant.QueryWatermark = job.LoadEndedAt, job.DrainDeadline, job.QueryWatermark
	grant.Reconciling = job.State == "reconciling"
	grant.MaxBatches = 32
	key, err := base64.StdEncoding.DecodeString(job.Source.CollectorPublicKey)
	if err != nil {
		return errors.New("collector encryption identity unavailable")
	}
	grant.Credentials, err = collectoragent.SealCredentials(*grant, key, collectoragent.DynatraceCredentialBundle(credentials))
	return err
}
func dynatraceGrantMatchesJob(grant collectoragent.RunJob, job storage.RunMonitorJob, collector string) bool {
	if job.DynatraceSnapshot == nil || job.Source.Dynatrace == nil || grant.Dynatrace == nil || grant.Pack != monitorprovider.PackDynatrace || grant.ID != job.ID || grant.ProjectID != job.ProjectID || grant.RunID != job.RunID || grant.ScenarioID != job.ScenarioID || grant.CollectorID != collector || job.Source.CollectorID != collector || grant.SourceID != job.Source.ID || grant.SourceRevision != job.Source.Revision || grant.Destination != job.Source.Destination {
		return false
	}
	expected := job.DynatraceSnapshot.Config.Defaults()
	expected.RequestsPerMinute = max(1, expected.RequestsPerMinute/2)
	expected.PlatformRequestsPerMinute = max(1, expected.PlatformRequestsPerMinute/2)
	selection := *job.Source.Dynatrace
	selection.ProjectID = job.ProjectID
	actual := grant.Dynatrace.Selection
	actual.QueryOffset = 0
	actual.Cursor = ""
	selection.QueryOffset = 0
	selection.Cursor = ""
	return reflect.DeepEqual(expected, grant.Dynatrace.Config) && reflect.DeepEqual(selection, actual)
}
func (s *Server) handleDynatraceScanReservation(w http.ResponseWriter, r *http.Request) {
	collector := s.authorizeMonitorCollector(w, r)
	if collector == nil {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 128<<10)
	var request collectoragent.DynatraceScanReservationRequest
	if !decodeJSONBody(w, r, &request) {
		return
	}
	key, err := monitorSigningKey()
	if err != nil {
		writeError(w, 503, "monitor signing configuration unavailable")
		return
	}
	grant, err := collectoragent.VerifyRunJob(request.Job, key.Public().(ed25519.PublicKey), collector.ID, time.Now().UTC())
	if err != nil || grant.ID != r.PathValue("job_id") {
		writeError(w, 403, "invalid Dynatrace scan grant")
		return
	}
	store, ok := s.store.(interface {
		storage.RunMonitorStore
		ReserveDynatraceScan(string, string, string, int64, time.Time) (bool, error)
	})
	if !ok {
		writeError(w, 503, "scan quota storage unavailable")
		return
	}
	job, err := store.GetRunMonitorJob(collector.ProjectID, grant.ID)
	if err != nil || job == nil || !dynatraceGrantMatchesJob(grant, *job, collector.ID) {
		writeError(w, 403, "scan grant does not match assigned job")
		return
	}
	admitted, err := store.ReserveDynatraceScan(collector.ProjectID, job.ID, request.AttemptID, request.Bytes, time.Now().UTC())
	if err != nil || !admitted {
		writeError(w, 429, "Dynatrace scan admission budget exhausted")
		return
	}
	writeJSON(w, 200, map[string]any{"admitted": true})
}
func (s *Server) handlePollDynatraceOperations(w http.ResponseWriter, r *http.Request) {
	collector := s.authorizeMonitorCollector(w, r)
	if collector == nil {
		return
	}
	if !slices.Contains(collector.AllowedMetricPacks, "dynatrace") {
		writeError(w, 403, "Dynatrace pack is not approved")
		return
	}
	store, ok := s.store.(storage.DynatraceOperationStore)
	if !ok {
		writeError(w, 503, "Dynatrace operation storage unavailable")
		return
	}
	key, err := monitorSigningKey()
	if err != nil {
		writeError(w, 503, "collector signing configuration unavailable")
		return
	}
	capacity := 4
	if raw := r.URL.Query().Get("capacity"); raw != "" {
		capacity, err = strconv.Atoi(raw)
		if err != nil || capacity < 1 || capacity > 4 {
			writeError(w, 400, "capacity must be between 1 and 4")
			return
		}
	}
	operations, err := store.ListDynatraceOperations(collector.ProjectID, collector.ID, capacity)
	if err != nil {
		writeError(w, 503, "Dynatrace operation storage unavailable")
		return
	}
	out := []collectoragent.SignedDynatraceInspectionJob{}
	for _, op := range operations {
		var work dynatraceOperationWork
		if json.Unmarshal([]byte(op.PayloadJSON), &work) != nil || work.Connection == nil {
			continue
		}
		now := time.Now().UTC()
		enrolled, err := store.DynatraceCollectorPublicKey(op.ProjectID, collector.ID, storage.DynatraceDestination(work.Connection), now)
		if err != nil || enrolled != work.CollectorPublicKey {
			continue
		}
		approved := true
		for _, origin := range collectoragent.DynatraceRequiredOrigins(work.Connection.Config) {
			k, e := store.DynatraceCollectorPublicKey(op.ProjectID, collector.ID, origin, now)
			approved = approved && e == nil && k == enrolled
		}
		if !approved {
			continue
		}
		owner := uuid.NewString()
		lease := now.Add(time.Minute)
		if op.ExpiresAt.Before(lease) {
			lease = op.ExpiresAt
		}
		claimed, err := store.ClaimDynatraceOperation(op.ID, owner, now, lease)
		if err != nil || !claimed {
			continue
		}
		credentials, err := resolveNativeDynatraceCredentials(r.Context(), s.runner.secretResolverForProject(op.ProjectID), work.Connection, work.Selection)
		if err != nil {
			_, _ = store.CompleteDynatraceOperation(op.ID, owner, "{}", "Dynatrace credential unavailable", now)
			continue
		}
		cfg, err := s.dynatraceQuotaConfig(r.Context(), work.Connection, credentials, "collector:"+collector.ID, work.Selection)
		if err != nil {
			_, _ = store.CompleteDynatraceOperation(op.ID, owner, "{}", "Dynatrace executor quota unavailable", now)
			continue
		}
		job := collectoragent.DynatraceInspectionJob{ID: op.ID, ProjectID: op.ProjectID, ConnectionID: op.ConnectionID, ConnectionRevision: op.ConnectionRevision, CollectorID: collector.ID, Kind: op.Kind, Config: cfg, Selection: work.Selection, Discovery: work.Options, PreviewFromUTC: work.PreviewFromUTC, PreviewUntilUTC: work.PreviewUntilUTC, OwnerToken: owner, LeaseExpiresAt: lease}
		publicKey, err := base64.StdEncoding.DecodeString(enrolled)
		if err != nil {
			continue
		}
		job.Credentials, err = collectoragent.SealDynatraceInspectionCredentials(job, publicKey, collectoragent.DynatraceCredentialBundle(credentials))
		if err != nil {
			continue
		}
		signed, err := collectoragent.SignDynatraceInspectionJob(job, key)
		if err == nil {
			out = append(out, signed)
		}
	}
	writeJSON(w, 200, out)
}
func (s *Server) handleDynatraceOperationResult(w http.ResponseWriter, r *http.Request) {
	collector := s.authorizeMonitorCollector(w, r)
	if collector == nil {
		return
	}
	if !slices.Contains(collector.AllowedMetricPacks, "dynatrace") {
		writeError(w, 403, "Dynatrace pack is not approved")
		return
	}
	store, ok := s.store.(storage.DynatraceOperationStore)
	if !ok {
		writeError(w, 503, "operation storage unavailable")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var result collectoragent.DynatraceInspectionResult
	if !decodeJSONBody(w, r, &result) {
		return
	}
	key, err := monitorSigningKey()
	if err != nil {
		writeError(w, 503, "collector signing configuration unavailable")
		return
	}
	grant, err := collectoragent.VerifyDynatraceInspectionJob(result.Job, key.Public().(ed25519.PublicKey), collector.ID, time.Now().UTC())
	if err != nil || grant.ID != r.PathValue("operation_id") || grant.ProjectID != collector.ProjectID {
		writeError(w, 403, "Dynatrace operation authorization rejected")
		return
	}
	op, err := store.GetDynatraceOperation(grant.ID)
	if err != nil || op.ProjectID != collector.ProjectID || op.CollectorID != collector.ID || op.ConnectionID != grant.ConnectionID || op.ConnectionRevision != grant.ConnectionRevision || op.OwnerToken != grant.OwnerToken {
		writeError(w, 403, "Dynatrace operation result does not match assignment")
		return
	}
	var work dynatraceOperationWork
	if json.Unmarshal([]byte(op.PayloadJSON), &work) != nil || work.Connection == nil {
		writeError(w, 409, "operation configuration unavailable")
		return
	}
	if len(result.Discovery.Items) > 1000 || len(result.Discovery.NextCursor) > 16384 || len(result.Message) > 1024 {
		writeError(w, 400, "operation result exceeds limits")
		return
	}
	payload := dynatraceOperationResult{Capabilities: sanitizeDynatraceCapabilitiesForConfig(result.Capabilities, work.Connection.Config), Items: sanitizeDynatraceEntities(result.Discovery.Items), NextCursor: result.Discovery.NextCursor, Truncated: result.Discovery.Truncated, Message: scrubTextBounded(result.Discovery.Message, 256), Preview: result.Preview}
	message := ""
	if result.Message != "" {
		message = "Dynatrace operation failed"
	}
	if op.Kind == "verify" {
		if work.Selection != nil {
			if validatePreparedDynatraceCapabilities(*work.Selection, payload.Capabilities) != nil {
				message = "selected Dynatrace capability unavailable"
			}
		} else if !dynatraceWantedCapabilitiesAvailable(work.WantedCapabilities, payload.Capabilities) {
			message = "selected Dynatrace capability unavailable"
		}
	}
	if payload.Preview != nil {
		if op.Kind != "preview" || sanitizeDynatracePreview(work, payload.Preview) != nil {
			writeError(w, 400, "invalid query preview")
			return
		}
	}
	body, err := json.Marshal(payload)
	if err != nil || len(body) > 1<<20 {
		writeError(w, 400, "operation result exceeds limits")
		return
	}
	if op.State == "complete" || op.State == "error" {
		if op.ResultJSON == string(body) && op.Error == message {
			writeJSON(w, 200, map[string]any{"accepted": true})
			return
		}
		writeError(w, 409, "operation result already committed")
		return
	}
	accepted, err := store.CompleteDynatraceOperation(op.ID, grant.OwnerToken, string(body), message, time.Now().UTC())
	if err != nil || !accepted {
		writeError(w, 409, "operation lease expired or changed")
		return
	}
	if op.Kind == "verify" && work.Selection == nil {
		s.publishDynatraceOperationHealth(op, message, payload.Capabilities)
	}
	writeJSON(w, 200, map[string]any{"accepted": true})
}
func sanitizeDynatraceEntities(items []dynatrace.Entity) []dynatrace.Entity {
	if len(items) > 1000 {
		items = items[:1000]
	}
	for i := range items {
		e := &items[i]
		e.ID = scrubTextBounded(e.ID, 128)
		e.Name = scrubTextBounded(e.Name, 512)
		e.Type = scrubTextBounded(e.Type, 64)
		e.ParentID = scrubTextBounded(e.ParentID, 128)
		e.Path = scrubTextBounded(e.Path, 4096)
		if len(e.EntityIDs) > 16 {
			e.EntityIDs = nil
		} else {
			for key, value := range e.EntityIDs {
				delete(e.EntityIDs, key)
				e.EntityIDs[scrubTextBounded(key, 128)] = scrubTextBounded(value, 256)
			}
		}
	}
	return items
}

func sanitizeDynatracePreview(work dynatraceOperationWork, preview *dynatrace.Result) error {
	if work.Connection == nil || work.Selection == nil || preview == nil || work.PreviewFromUTC.IsZero() || !work.PreviewUntilUTC.After(work.PreviewFromUTC) || len(preview.Points) > 512 || len(preview.Evidence) > 100 {
		return errors.New("invalid query preview")
	}
	sanitizeDynatraceDiagnostics(preview.Evidence)
	if err := dynatrace.ValidateResult(*work.Selection, work.Connection.Config, work.PreviewFromUTC, work.PreviewUntilUTC, "", *preview); err != nil {
		return err
	}
	preview.Progress.Cursor = ""
	preview.Progress.CursorExpiresAt = nil
	preview.Progress.ScanReservationIDs = nil
	preview.Progress.ScanSettlements = nil
	preview.Capabilities = sanitizeDynatraceCapabilitiesForConfig(preview.Capabilities, work.Connection.Config)
	preview.Topology = sanitizeDynatraceEntities(preview.Topology)
	for key, coverage := range preview.Coverage {
		coverage.Message = scrubTextBounded(coverage.Message, 256)
		preview.Coverage[key] = coverage
	}
	return nil
}
