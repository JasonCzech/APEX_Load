package api

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/collectoragent"
	"github.com/jasonczech/apexload/pkg/elastic"
	"github.com/jasonczech/apexload/pkg/storage"
)

type ElasticInspectionRequest struct {
	RunID            string             `json:"run_id,omitempty"`
	Kind             string             `json:"kind"`
	ExpectedRevision int64              `json:"expected_revision"`
	Indices          []string           `json:"indices,omitempty"`
	Selection        *elastic.Selection `json:"selection,omitempty"`
	FromUTC          time.Time          `json:"from_utc,omitempty"`
	ToUTC            time.Time          `json:"to_utc,omitempty"`
	Limit            int                `json:"limit,omitempty"`
}
type elasticInspectionWork struct {
	Connection         *storage.ElasticConnectionSnapshot `json:"connection"`
	Request            ElasticInspectionRequest           `json:"request"`
	CollectorPublicKey string                             `json:"collector_public_key,omitempty"`
}
type ElasticInspectionResponse struct {
	ID                 string          `json:"id"`
	ConnectionID       string          `json:"connection_id"`
	ConnectionRevision int64           `json:"connection_revision"`
	Status             string          `json:"status"`
	Kind               string          `json:"kind"`
	Message            string          `json:"message,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
	ExpiresAt          time.Time       `json:"expires_at"`
	Result             json.RawMessage `json:"result,omitempty"`
}

func elasticInspectionResponse(op *storage.ElasticInspection) ElasticInspectionResponse {
	status := op.State
	if (status == "pending" || status == "running") && !op.ExpiresAt.After(time.Now().UTC()) {
		status = "expired"
	}
	return ElasticInspectionResponse{op.ID, op.ConnectionID, op.ConnectionRevision, status, op.Kind, op.Error, op.CreatedAt, op.ExpiresAt, json.RawMessage(op.ResultJSON)}
}
func (s *Server) elasticInspectionStore(w http.ResponseWriter) storage.ElasticInspectionStore {
	store, ok := s.store.(storage.ElasticInspectionStore)
	if !ok || store.EnsureElasticInspectionSchema() != nil {
		writeError(w, 503, "Elastic inspection storage unavailable")
		return nil
	}
	return store
}
func (s *Server) handleCreateElasticInspection(w http.ResponseWriter, r *http.Request) {
	c, _, ok := s.loadProjectELKConnection(w, r)
	if !ok {
		return
	}
	store := s.elasticInspectionStore(w)
	if store == nil {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 96<<10)
	var req ElasticInspectionRequest
	if !decodeAppDynamicsJSON(w, r, &req) {
		return
	}
	if req.ExpectedRevision < 1 || req.ExpectedRevision != c.Revision {
		writeError(w, 409, "Elastic connection changed; reload before inspecting")
		return
	}
	switch req.Kind {
	case "verify", "discover_indices", "discover_fields", "preview":
	default:
		writeError(w, 400, "unsupported Elastic inspection kind")
		return
	}
	if len(req.Indices) > 32 || req.Limit < 0 || req.Limit > 100 || len(req.RunID) > 256 {
		writeError(w, 400, "inspection limits exceeded")
		return
	}
	if len(req.Indices) == 0 && c.IndexPattern != "" {
		req.Indices = []string{c.IndexPattern}
	}
	if req.Selection != nil {
		selection := req.Selection.Defaults()
		selection.ProjectID = c.ProjectID
		selection.QueryOffset = 0
		selection.Continuation = nil
		if selection.Validate() != nil {
			writeError(w, 400, "invalid preview selection")
			return
		}
		req.Selection = &selection
		req.Indices = selection.Indices
	}
	if req.Kind == "preview" && req.Selection == nil || req.Kind == "discover_fields" && len(req.Indices) == 0 {
		writeError(w, 400, "selected indices and preview mapping are required")
		return
	}
	now := time.Now().UTC()
	if req.ToUTC.IsZero() {
		req.ToUTC = now
	}
	if req.FromUTC.IsZero() {
		req.FromUTC = req.ToUTC.Add(-time.Minute)
	}
	req.FromUTC = req.FromUTC.UTC()
	req.ToUTC = req.ToUTC.UTC()
	if !req.ToUTC.After(req.FromUTC) || req.ToUTC.Sub(req.FromUTC) > time.Hour || req.ToUTC.After(now.Add(time.Minute)) {
		writeError(w, 400, "inspection window must be at most one hour")
		return
	}
	if c.RuntimeConfig().Validate() != nil {
		writeError(w, 409, "Elastic connection transport is incomplete")
		return
	}
	if s.runner == nil {
		writeError(w, 503, "monitoring runner unavailable")
		return
	}
	if _, err := resolveElasticCredentials(r.Context(), s.runner.secretResolverForProject(c.ProjectID), c.Snapshot()); err != nil {
		writeError(w, 409, "Elastic project credentials unavailable")
		return
	}
	work := elasticInspectionWork{Connection: c.Snapshot(), Request: req}
	collector := ""
	if c.CollectionMode == "collector" {
		collector = c.CollectorAgentID
		key, err := store.ElasticCollectorPublicKey(c.ProjectID, collector, c.RuntimeConfig().Endpoint, now)
		if err != nil {
			writeError(w, 409, "assigned Elastic collector is not enrolled and ready")
			return
		}
		work.CollectorPublicKey = key
		if _, err := monitorSigningKey(); err != nil {
			writeError(w, 503, err.Error())
			return
		}
	}
	body, err := json.Marshal(work)
	if err != nil || len(body) > 96<<10 {
		writeError(w, 400, "inspection configuration exceeds limits")
		return
	}
	op := &storage.ElasticInspection{ID: "elastic-inspection-" + uuid.NewString(), ProjectID: c.ProjectID, ConnectionID: c.ID, ConnectionRevision: c.Revision, CollectorID: collector, Kind: req.Kind, PayloadJSON: string(body), CreatedAt: now, ExpiresAt: now.Add(15 * time.Minute)}
	if err = store.CreateElasticInspection(op); err != nil {
		writeError(w, 503, "cannot queue Elastic inspection")
		return
	}
	s.recordAuditEvent(r, "monitoring.elastic.inspection", "queued", "elk_connection", c.ID, c.ProjectID, map[string]any{"operation_id": op.ID, "revision": c.Revision, "kind": req.Kind})
	writeJSON(w, 202, elasticInspectionResponse(op))
	if collector == "" {
		s.resumeElasticInspections(context.Background())
	}
}
func (s *Server) handleGetElasticInspection(w http.ResponseWriter, r *http.Request) {
	store := s.elasticInspectionStore(w)
	if store == nil {
		return
	}
	op, err := store.GetElasticInspection(r.PathValue("operation_id"))
	if err != nil || op.ProjectID != r.PathValue("id") || op.ConnectionID != r.PathValue("connection_id") {
		writeError(w, 404, "Elastic inspection not found")
		return
	}
	writeJSON(w, 200, elasticInspectionResponse(op))
	if op.CollectorID == "" {
		s.resumeElasticInspections(context.Background())
	}
}

var elasticInspectionCancels sync.Map
var elasticInspectionSlots = make(chan struct{}, 2)

func (s *Server) handleCancelElasticInspection(w http.ResponseWriter, r *http.Request) {
	store := s.elasticInspectionStore(w)
	if store == nil {
		return
	}
	op, err := store.GetElasticInspection(r.PathValue("operation_id"))
	if err != nil || op.ProjectID != r.PathValue("id") || op.ConnectionID != r.PathValue("connection_id") {
		writeError(w, 404, "Elastic inspection not found")
		return
	}
	_, err = store.CancelElasticInspection(op.ID, op.ProjectID, op.ConnectionID, time.Now().UTC())
	if err != nil {
		writeError(w, 503, "cannot cancel Elastic inspection")
		return
	}
	if cancel, ok := elasticInspectionCancels.Load(op.ID); ok {
		cancel.(context.CancelFunc)()
	}
	s.recordAuditEvent(r, "monitoring.elastic.inspection.cancel", "success", "elk_connection", op.ConnectionID, op.ProjectID, map[string]any{"operation_id": op.ID})
	w.WriteHeader(204)
}
func (s *Server) resumeElasticInspections(parent context.Context) {
	if s.runner == nil || !s.runner.isExecutionLeader() {
		return
	}
	store, ok := s.store.(storage.ElasticInspectionStore)
	if !ok {
		return
	}
	ops, err := store.ListElasticInspections("", "", 4)
	if err != nil {
		return
	}
	for _, op := range ops {
		select {
		case elasticInspectionSlots <- struct{}{}:
		default:
			return
		}
		owner := uuid.NewString()
		now := time.Now().UTC()
		claimed, err := store.ClaimElasticInspection(op.ID, owner, now, now.Add(time.Minute))
		if err != nil || !claimed {
			<-elasticInspectionSlots
			continue
		}
		work := op
		if !s.launchBackground(func() {
			defer func() { <-elasticInspectionSlots }()
			s.executeDirectElasticInspection(parent, store, work, owner)
		}) {
			<-elasticInspectionSlots
		}
	}
}
func (s *Server) executeDirectElasticInspection(parent context.Context, store storage.ElasticInspectionStore, op *storage.ElasticInspection, owner string) {
	ctx, cancel := s.runner.executionOperationContext(parent)
	defer cancel()
	ctx, stop := context.WithTimeout(ctx, 45*time.Second)
	defer stop()
	elasticInspectionCancels.Store(op.ID, context.CancelFunc(stop))
	defer elasticInspectionCancels.Delete(op.ID)
	var work elasticInspectionWork
	result := collectoragent.ElasticInspectionData{}
	message := ""
	if json.Unmarshal([]byte(op.PayloadJSON), &work) != nil || work.Connection == nil {
		message = "invalid Elastic inspection configuration"
	} else {
		credentials, err := resolveElasticCredentials(ctx, s.runner.secretResolverForProject(op.ProjectID), work.Connection)
		if err == nil {
			var client *elastic.Client
			client, err = elastic.NewClient(elasticPolicyConfig(work.Connection.Config), credentials)
			if err == nil {
				defer client.Close()
				result, err = collectoragent.InspectElastic(ctx, client, op.Kind, work.Request.Indices, work.Request.Selection, work.Request.FromUTC, work.Request.ToUTC, work.Request.RunID)
			}
		}
		if err != nil {
			message = "Elastic inspection failed"
		}
	}
	limitElasticInspectionResult(&result, work.Request.Limit)
	body, err := json.Marshal(result)
	if err != nil || len(body) > 1<<20 {
		body = []byte(`{}`)
		message = "Elastic inspection result exceeds limits"
	}
	completed, err := store.CompleteElasticInspection(op.ID, owner, string(body), message, time.Now().UTC())
	if err == nil && completed && op.Kind == "verify" {
		s.persistElasticVerification(op, result.Capabilities, message)
	}
}
func (s *Server) persistElasticVerification(op *storage.ElasticInspection, caps elastic.Capabilities, message string) {
	store, ok := s.store.(storage.ElasticConnectionStore)
	if !ok {
		return
	}
	status := "connected"
	if message != "" {
		status = "error"
	}
	_, _ = store.UpdateElasticConnectionHealth(op.ConnectionID, op.ProjectID, op.ConnectionRevision, status, message, caps, time.Now().UTC())
}
func (s *Server) handlePollElasticInspections(w http.ResponseWriter, r *http.Request) {
	c := s.authorizeMonitorCollector(w, r)
	if c == nil {
		return
	}
	store := s.elasticInspectionStore(w)
	if store == nil {
		return
	}
	key, err := monitorSigningKey()
	if err != nil {
		writeError(w, 503, err.Error())
		return
	}
	capacity := 2
	if raw := r.URL.Query().Get("capacity"); raw != "" {
		capacity, err = strconv.Atoi(raw)
		if err != nil || capacity < 1 || capacity > 4 {
			writeError(w, 400, "invalid inspection capacity")
			return
		}
	}
	ops, err := store.ListElasticInspections(c.ProjectID, c.ID, capacity)
	if err != nil {
		writeError(w, 503, "Elastic inspection unavailable")
		return
	}
	out := []collectoragent.SignedElasticInspectionJob{}
	for _, op := range ops {
		var work elasticInspectionWork
		if json.Unmarshal([]byte(op.PayloadJSON), &work) != nil || work.Connection == nil {
			continue
		}
		now := time.Now().UTC()
		owner := uuid.NewString()
		until := now.Add(time.Minute)
		if op.ExpiresAt.Before(until) {
			until = op.ExpiresAt
		}
		claimed, err := store.ClaimElasticInspection(op.ID, owner, now, until)
		if err != nil || !claimed {
			continue
		}
		grant := collectoragent.ElasticInspectionJob{ID: op.ID, ProjectID: op.ProjectID, ConnectionID: op.ConnectionID, ConnectionRevision: op.ConnectionRevision, CollectorID: c.ID, Kind: op.Kind, Config: elasticPolicyConfig(work.Connection.Config), Indices: work.Request.Indices, Selection: work.Request.Selection, RunID: work.Request.RunID, FromUTC: work.Request.FromUTC, ToUTC: work.Request.ToUTC, OwnerToken: owner, LeaseExpiresAt: until}
		credentials, err := resolveElasticCredentials(r.Context(), s.runner.secretResolverForProject(c.ProjectID), work.Connection)
		if err != nil {
			_, _ = store.CompleteElasticInspection(op.ID, owner, "{}", "Elastic credentials unavailable", now)
			continue
		}
		public, err := base64.StdEncoding.DecodeString(work.CollectorPublicKey)
		if err != nil {
			continue
		}
		grant.Credentials, err = collectoragent.SealElasticInspectionCredentials(grant, public, collectoragent.ElasticCredentialBundle(credentials))
		if err != nil {
			continue
		}
		envelope, err := collectoragent.SignElasticInspectionJob(grant, key)
		if err == nil {
			out = append(out, envelope)
		}
	}
	writeJSON(w, 200, out)
}
func (s *Server) handleElasticInspectionResult(w http.ResponseWriter, r *http.Request) {
	c := s.authorizeMonitorCollector(w, r)
	if c == nil {
		return
	}
	store := s.elasticInspectionStore(w)
	if store == nil {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var result collectoragent.ElasticInspectionResult
	if !decodeJSONBody(w, r, &result) {
		return
	}
	key, err := monitorSigningKey()
	if err != nil {
		writeError(w, 503, err.Error())
		return
	}
	grant, err := collectoragent.VerifyElasticInspectionJob(result.Job, key.Public().(ed25519.PublicKey), c.ID, time.Now().UTC())
	if err != nil || grant.ID != r.PathValue("operation_id") || grant.ProjectID != c.ProjectID {
		writeError(w, 403, "invalid Elastic inspection grant")
		return
	}
	op, err := store.GetElasticInspection(grant.ID)
	if err != nil || op.CollectorID != c.ID || op.ProjectID != c.ProjectID || op.ConnectionID != grant.ConnectionID || op.ConnectionRevision != grant.ConnectionRevision {
		writeError(w, 403, "inspection does not match immutable operation")
		return
	}
	if err = collectoragent.ValidateElasticInspectionData(result.Data); err != nil || result.Data.Capabilities.Product != "" && result.Data.Capabilities.Product != grant.Config.Defaults().Product {
		writeError(w, 400, "invalid Elastic inspection result")
		return
	}
	var work elasticInspectionWork
	if json.Unmarshal([]byte(op.PayloadJSON), &work) != nil {
		writeError(w, 409, "Elastic inspection configuration unavailable")
		return
	}
	limitElasticInspectionResult(&result.Data, work.Request.Limit)
	message := ""
	if result.Message != "" {
		message = "Elastic inspection failed"
	}
	body, err := json.Marshal(result.Data)
	if err != nil {
		writeError(w, 400, "invalid Elastic inspection result")
		return
	}
	completed, err := store.CompleteElasticInspection(op.ID, grant.OwnerToken, string(body), message, time.Now().UTC())
	if err != nil || !completed {
		writeError(w, 409, "Elastic inspection claim expired or cancelled")
		return
	}
	if op.Kind == "verify" {
		s.persistElasticVerification(op, result.Data.Capabilities, message)
	}
	writeJSON(w, 200, map[string]bool{"accepted": true})
}

func limitElasticInspectionResult(result *collectoragent.ElasticInspectionData, limit int) {
	if result == nil || limit <= 0 {
		return
	}
	if len(result.Indices) > limit {
		result.Indices = result.Indices[:limit]
		result.Truncated = true
	}
	if len(result.Fields) > limit {
		result.Fields = result.Fields[:limit]
		result.Truncated = true
	}
	if result.Preview != nil {
		if len(result.Preview.Evidence) > limit {
			result.Preview.Evidence = result.Preview.Evidence[:limit]
			result.Truncated = true
		}
		if len(result.Preview.Points) > limit {
			result.Preview.Points = result.Preview.Points[:limit]
			result.Truncated = true
		}
	}
}

func (s *Server) verifyPreparedElastic(ctx context.Context, source storage.MonitorSource, snapshot *storage.ElasticConnectionSnapshot) error {
	if source.Elastic == nil || snapshot == nil {
		return errors.New("Elastic prepared source unavailable")
	}
	if source.ExecutorMode == "direct" {
		credentials, err := resolveElasticCredentials(ctx, s.runner.secretResolverForProject(source.ProjectID), snapshot)
		if err != nil {
			return err
		}
		client, err := elastic.NewClient(elasticPolicyConfig(snapshot.Config), credentials)
		if err != nil {
			return err
		}
		defer client.Close()
		result, err := collectoragent.InspectElastic(ctx, client, "verify", source.Elastic.Indices, source.Elastic, time.Now().UTC().Add(-time.Minute), time.Now().UTC())
		caps := result.Capabilities
		if err != nil {
			return errors.New("Elastic preparation connectivity verification failed")
		}
		return validatePreparedElasticCapabilities(*source.Elastic, caps)
	}
	store, ok := s.store.(storage.ElasticInspectionStore)
	if !ok {
		return errors.New("Elastic collector inspection unavailable")
	}
	now := time.Now().UTC()
	expires := now.Add(time.Minute)
	if deadline, ok := ctx.Deadline(); ok && deadline.Before(expires) {
		expires = deadline
	}
	work := elasticInspectionWork{Connection: snapshot, CollectorPublicKey: source.CollectorPublicKey, Request: ElasticInspectionRequest{Kind: "verify", Indices: source.Elastic.Indices, Selection: source.Elastic, FromUTC: now.Add(-time.Minute), ToUTC: now}}
	body, _ := json.Marshal(work)
	op := &storage.ElasticInspection{ID: "elastic-inspection-" + uuid.NewString(), ProjectID: source.ProjectID, ConnectionID: source.ConnectionID, ConnectionRevision: snapshot.Revision, CollectorID: source.CollectorID, Kind: "verify", PayloadJSON: string(body), CreatedAt: now, ExpiresAt: expires}
	if err := store.CreateElasticInspection(op); err != nil {
		return err
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			_, _ = store.CancelElasticInspection(op.ID, op.ProjectID, op.ConnectionID, time.Now().UTC())
			return errors.New("Elastic preparation connectivity timed out")
		case <-ticker.C:
			current, err := store.GetElasticInspection(op.ID)
			if err != nil {
				return errors.New("Elastic preparation result unavailable")
			}
			if current.State == "error" || !current.ExpiresAt.After(time.Now()) {
				return errors.New("Elastic preparation connectivity failed")
			}
			if current.State == "complete" {
				var result collectoragent.ElasticInspectionData
				if json.Unmarshal([]byte(current.ResultJSON), &result) != nil {
					return errors.New("invalid Elastic preparation result")
				}
				return validatePreparedElasticCapabilities(*source.Elastic, result.Capabilities)
			}
		}
	}
}
func validatePreparedElasticCapabilities(selection elastic.Selection, caps elastic.Capabilities) error {
	indexed := len(selection.Metrics) > 0 || len(selection.Queries) > 0 || selection.Logs || selection.Traces || selection.Errors || selection.Transactions
	if indexed && !caps.DSL || (selection.Logs || selection.Traces || selection.Errors || selection.Transactions) && !caps.PIT {
		return errors.New("selected Elastic search or pagination capability unavailable")
	}
	for _, q := range selection.Queries {
		if q.Language == "esql" && (!caps.ESQL || q.Async && !caps.AsyncESQL) {
			return errors.New("selected ES|QL capability unavailable")
		}
	}
	if caps.Product == elastic.ProductOpenSearch && !caps.ShardDoc && (selection.Logs || selection.Traces || selection.Errors || selection.Transactions) && selection.UniqueSortField == "" {
		return errors.New("OpenSearch document capture requires a stable sortable identity")
	}
	if selection.KibanaHealth && !caps.KibanaHealth || selection.LogstashHealth && !caps.LogstashHealth {
		return errors.New("selected component health API unavailable")
	}
	if selection.ClusterHealth && !caps.Health {
		return errors.New("selected Elastic component health capability unavailable")
	}
	return nil
}
