package api

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/appdynamics"
	"github.com/jasonczech/apexload/pkg/collectoragent"
	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
	"github.com/jasonczech/apexload/pkg/storage"
)

type AppDynamicsInspectionRequest struct {
	CollectionMode     string `json:"collection_mode,omitempty"`
	CollectorAgentID   string `json:"collector_agent_id,omitempty"`
	CollectorPublicKey string `json:"collector_public_key,omitempty"`
}
type appDynamicsInspectionWork struct {
	Connection         *storage.AppDynamicsConnectionSnapshot `json:"connection"`
	Selection          *appdynamics.Selection                 `json:"selection,omitempty"`
	CollectorPublicKey string                                 `json:"collector_public_key,omitempty"`
	ApplicationID      string                                 `json:"application_id,omitempty"`
	EntityKind         string                                 `json:"entity_kind,omitempty"`
	Path               string                                 `json:"path,omitempty"`
}
type AppDynamicsOperationResponse struct {
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

func appDynamicsOperationResponse(op *storage.AppDynamicsInspection) AppDynamicsOperationResponse {
	state := op.State
	if (state == "pending" || state == "running") && !op.ExpiresAt.After(time.Now().UTC()) {
		state = "expired"
	}
	return AppDynamicsOperationResponse{ID: op.ID, ConnectionID: op.ConnectionID, ConnectionRevision: op.ConnectionRevision, Status: state, Kind: op.Kind, Message: op.Error, CreatedAt: op.CreatedAt, ExpiresAt: op.ExpiresAt, Result: json.RawMessage(op.ResultJSON)}
}
func (s *Server) appDynamicsInspectionStore(w http.ResponseWriter) storage.AppDynamicsInspectionStore {
	store, ok := s.store.(storage.AppDynamicsInspectionStore)
	if !ok {
		writeError(w, 503, "AppDynamics operation storage unavailable")
		return nil
	}
	if err := store.EnsureAppDynamicsInspectionSchema(); err != nil {
		writeError(w, 503, "AppDynamics operation storage unavailable")
		return nil
	}
	return store
}
func (s *Server) handleTestAppDynamicsConnection(w http.ResponseWriter, r *http.Request) {
	s.createAppDynamicsInspection(w, r, "test", "")
}
func (s *Server) handleDiscoverAppDynamicsApplications(w http.ResponseWriter, r *http.Request) {
	s.createAppDynamicsInspection(w, r, "discover", "applications")
}
func (s *Server) handleDiscoverAppDynamicsTiers(w http.ResponseWriter, r *http.Request) {
	s.createAppDynamicsInspection(w, r, "discover", "tiers")
}
func (s *Server) handleDiscoverAppDynamicsNodes(w http.ResponseWriter, r *http.Request) {
	s.createAppDynamicsInspection(w, r, "discover", "nodes")
}
func (s *Server) handleDiscoverAppDynamicsBackends(w http.ResponseWriter, r *http.Request) {
	s.createAppDynamicsInspection(w, r, "discover", "backends")
}
func (s *Server) handleDiscoverAppDynamicsBusinessTransactions(w http.ResponseWriter, r *http.Request) {
	s.createAppDynamicsInspection(w, r, "discover", "business-transactions")
}
func (s *Server) handleDiscoverAppDynamicsMetrics(w http.ResponseWriter, r *http.Request) {
	s.createAppDynamicsInspection(w, r, "discover", "metrics")
}
func (s *Server) createAppDynamicsInspection(w http.ResponseWriter, r *http.Request, kind, entity string) {
	c, _ := s.loadAppDynamicsConnection(w, r)
	if c == nil {
		return
	}
	store := s.appDynamicsInspectionStore(w)
	if store == nil {
		return
	}
	var req AppDynamicsInspectionRequest
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if r.Method == http.MethodPost && r.ContentLength != 0 {
		if !decodeAppDynamicsJSON(w, r, &req) {
			return
		}
	} else {
		req.CollectionMode = r.URL.Query().Get("collection_mode")
		req.CollectorAgentID = r.URL.Query().Get("collector_agent_id")
		req.CollectorPublicKey = r.URL.Query().Get("collector_public_key")
	}
	mode := req.CollectionMode
	if mode == "" {
		mode = c.CollectionMode
	}
	if mode != c.CollectionMode {
		writeError(w, 400, "inspection must use the saved connection collection mode")
		return
	}
	if mode != "direct" && mode != "collector" {
		writeError(w, 400, "collection_mode must be direct or collector")
		return
	}
	if err := c.Config.Validate(); err != nil {
		writeError(w, 409, "connection transport is incomplete")
		return
	}
	// Verify credential presence before queuing an operation. This never sends a
	// request to the monitored application and never serializes resolved values.
	if _, err := resolveAppDynamicsCredentials(r.Context(), s.runner.secretResolverForProject(c.ProjectID), c.Snapshot()); err != nil {
		writeError(w, 409, err.Error())
		return
	}
	work := appDynamicsInspectionWork{Connection: c.Snapshot(), ApplicationID: r.URL.Query().Get("application_id"), EntityKind: entity, Path: r.URL.Query().Get("metric_path")}
	if len(work.ApplicationID) > 20 || len(work.Path) > 4096 || entity != "" && entity != "applications" && work.ApplicationID == "" {
		writeError(w, 400, "application_id is required for this discovery operation")
		return
	}
	collector := ""
	if mode == "collector" {
		collector = req.CollectorAgentID
		if collector == "" {
			collector = c.CollectorAgentID
		}
		agent, err := s.store.GetMonitoringCollector(collector)
		if err != nil || !appDynamicsCollectorAllowed(agent, c.ProjectID) {
			writeError(w, 409, "assigned AppDynamics collector unavailable")
			return
		}
		if collector != c.CollectorAgentID {
			writeError(w, 400, "inspection must use the saved assigned collector")
			return
		}
		enrolled, err := store.AppDynamicsCollectorPublicKey(c.ProjectID, collector, c.Config.Endpoint, time.Now().UTC())
		if err != nil {
			writeError(w, 409, "assigned collector destination is not enrolled and ready")
			return
		}
		if req.CollectorPublicKey != "" && req.CollectorPublicKey != enrolled {
			writeError(w, 400, "collector key does not match enrollment")
			return
		}
		req.CollectorPublicKey = enrolled
		key, err := base64.StdEncoding.DecodeString(req.CollectorPublicKey)
		if err != nil || len(key) != 32 {
			writeError(w, 400, "enrolled collector_public_key is required")
			return
		}
		if _, err := monitorSigningKey(); err != nil {
			writeError(w, 503, err.Error())
			return
		}
		work.CollectorPublicKey = req.CollectorPublicKey
	}
	body, err := json.Marshal(work)
	if err != nil {
		writeError(w, 400, "inspection configuration exceeds limits")
		return
	}
	now := time.Now().UTC()
	op := &storage.AppDynamicsInspection{ID: "appd-inspection-" + uuid.NewString(), ProjectID: c.ProjectID, ConnectionID: c.ID, ConnectionRevision: c.Revision, CollectorID: collector, Kind: kind, PayloadJSON: string(body), CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	if err = store.CreateAppDynamicsInspection(op); err != nil {
		writeError(w, 503, "cannot queue AppDynamics inspection")
		return
	}
	s.recordAuditEvent(r, "monitoring.appdynamics."+kind, "queued", "appdynamics_connection", c.ID, c.ProjectID, map[string]any{"operation_id": op.ID, "revision": c.Revision, "collection_mode": mode})
	writeJSON(w, http.StatusAccepted, appDynamicsOperationResponse(op))
	if mode == "direct" {
		s.resumeAppDynamicsInspections(context.Background())
	}
}
func (s *Server) handleGetAppDynamicsOperation(w http.ResponseWriter, r *http.Request) {
	store := s.appDynamicsInspectionStore(w)
	if store == nil {
		return
	}
	op, err := store.GetAppDynamicsInspection(r.PathValue("operation_id"))
	if err != nil || op.ProjectID != r.PathValue("id") || op.ConnectionID != r.PathValue("connection_id") {
		writeError(w, 404, "AppDynamics operation not found")
		return
	}
	writeJSON(w, 200, appDynamicsOperationResponse(op))
	if op.CollectorID == "" && (op.State == "pending" || op.State == "running") {
		s.resumeAppDynamicsInspections(context.Background())
	}
}

// Called by startup/leader reconciliation and on administrative requests. Claim
// CAS prevents concurrent replicas from executing the same pending operation.
func (s *Server) resumeAppDynamicsInspections(parent context.Context) {
	if s.runner == nil || !s.runner.isExecutionLeader() {
		return
	}
	store, ok := s.store.(storage.AppDynamicsInspectionStore)
	if !ok {
		return
	}
	if err := store.EnsureAppDynamicsInspectionSchema(); err != nil {
		return
	}
	ops, err := store.ListAppDynamicsInspections("", "", 4)
	if err != nil {
		return
	}
	for _, op := range ops {
		owner := uuid.NewString()
		now := time.Now().UTC()
		claimed, err := store.ClaimAppDynamicsInspection(op.ID, owner, now, now.Add(time.Minute))
		if err != nil || !claimed {
			continue
		}
		s.launchBackground(func() { s.executeDirectAppDynamicsInspection(parent, store, op, owner) })
	}
}
func (s *Server) executeDirectAppDynamicsInspection(parent context.Context, store storage.AppDynamicsInspectionStore, op *storage.AppDynamicsInspection, owner string) {
	ctx, cancel := s.runner.executionOperationContext(parent)
	defer cancel()
	ctx, stop := context.WithTimeout(ctx, 45*time.Second)
	defer stop()
	var work appDynamicsInspectionWork
	var result struct {
		Capabilities appdynamics.Capabilities `json:"capabilities"`
		Entities     []appdynamics.Entity     `json:"entities,omitempty"`
		Truncated    bool                     `json:"truncated,omitempty"`
	}
	message := ""
	if json.Unmarshal([]byte(op.PayloadJSON), &work) != nil || work.Connection == nil {
		message = "invalid inspection configuration"
	} else {
		credentials, err := resolveAppDynamicsCredentials(ctx, s.runner.secretResolverForProject(op.ProjectID), work.Connection)
		if err != nil {
			message = err.Error()
		} else {
			cfg, err := s.appDynamicsQuotaConfig(ctx, work.Connection, credentials, "direct:"+s.controllerID)
			var client *appdynamics.Client
			if err == nil {
				client, err = appdynamics.NewClient(cfg, credentials)
			}
			if err == nil {
				if op.Kind == "test" {
					if work.Selection != nil {
						result.Capabilities, err = client.TestSelection(ctx, *work.Selection)
					} else {
						result.Capabilities, err = client.Test(ctx)
					}
				} else {
					result.Entities, err = client.Discover(ctx, work.ApplicationID, work.EntityKind, work.Path)
				}
			}
			if err != nil {
				message = "AppDynamics inspection failed"
			}
		}
	}
	if ctx.Err() != nil || !s.runner.isExecutionLeader() {
		return
	}
	result.Truncated = len(result.Entities) >= 200
	result.Entities = sanitizeAppDynamicsEntities(result.Entities)
	data, _ := json.Marshal(result)
	completed, err := store.CompleteAppDynamicsInspection(op.ID, owner, string(data), message, time.Now().UTC())
	if err == nil && completed && op.Kind == "test" && work.Selection == nil {
		s.publishAppDynamicsInspectionHealth(op, message, result.Capabilities)
	}
}
func (s *Server) publishAppDynamicsInspectionHealth(op *storage.AppDynamicsInspection, message string, caps appdynamics.Capabilities) {
	if store, ok := s.store.(storage.AppDynamicsConnectionStore); ok {
		status := "connected"
		if message != "" {
			status = "error"
		}
		_, _ = store.UpdateAppDynamicsConnectionHealth(op.ConnectionID, op.ProjectID, op.ConnectionRevision, status, message, caps, time.Now().UTC())
	}
}
func sanitizeAppDynamicsEntities(entities []appdynamics.Entity) []appdynamics.Entity {
	if len(entities) > 200 {
		entities = entities[:200]
	}
	for i := range entities {
		e := &entities[i]
		e.ID = boundedtext.TruncateUTF8(scrubELKText(e.ID), 128)
		e.Name = boundedtext.TruncateUTF8(scrubELKText(e.Name), 512)
		e.Type = boundedtext.TruncateUTF8(e.Type, 64)
		e.ParentID = boundedtext.TruncateUTF8(e.ParentID, 128)
		e.Path = boundedtext.TruncateUTF8(scrubELKText(e.Path), 4096)
		ids := map[string]string{}
		for _, kind := range []string{"application", "tier", "node", "business_transaction", "machine", "backend"} {
			value := e.EntityIDs[kind]
			if parsed, err := strconv.ParseUint(value, 10, 64); err == nil && parsed > 0 && len(value) <= 20 {
				ids[kind] = value
			}
		}
		e.EntityIDs = ids
	}
	return entities
}
func sanitizeAppDynamicsCapabilities(c appdynamics.Capabilities) appdynamics.Capabilities {
	if c.MetricAPI != "metric-data" && c.MetricAPI != "metric-data-v2" {
		c.MetricAPI = ""
	}
	statuses := map[string]string{}
	for _, key := range []string{"controller", "applications", "discovery", "metrics", "events", "violations", "snapshots", "analytics"} {
		value := c.Status[key]
		switch value {
		case "available", "available_no_data", "empty", "not_configured", "not_selected", "unavailable", "unauthorized", "forbidden", "unsupported", "rate_limited", "budget_exhausted", "unlicensed", "unreachable", "error":
			statuses[key] = value
		}
	}
	c.Status = statuses
	return c
}
func (s *Server) handlePollAppDynamicsInspections(w http.ResponseWriter, r *http.Request) {
	c := s.authorizeMonitorCollector(w, r)
	if c == nil {
		return
	}
	if !appDynamicsCollectorAllowed(c, c.ProjectID) && !prometheusCollectorAllowed(c, c.ProjectID) {
		writeError(w, 403, "AppDynamics pack is not approved for this collector")
		return
	}
	store := s.appDynamicsInspectionStore(w)
	if store == nil {
		return
	}
	key, err := monitorSigningKey()
	if err != nil {
		writeError(w, 503, err.Error())
		return
	}
	capacity := 4
	if raw := r.URL.Query().Get("capacity"); raw != "" {
		value, parseErr := strconv.Atoi(raw)
		if parseErr != nil || value < 1 || value > 4 {
			writeError(w, 400, "inspection capacity must be between 1 and 4")
			return
		}
		capacity = value
	}
	ops, err := store.ListAppDynamicsInspections(c.ProjectID, c.ID, capacity)
	if err != nil {
		writeError(w, 503, "cannot poll inspections")
		return
	}
	out := []collectoragent.SignedInspectionJob{}
	for _, op := range ops {
		now := time.Now().UTC()
		owner := uuid.NewString()
		lease := now.Add(time.Minute)
		if op.ExpiresAt.Before(lease) {
			lease = op.ExpiresAt
		}
		var work appDynamicsInspectionWork
		if json.Unmarshal([]byte(op.PayloadJSON), &work) != nil || work.Connection == nil {
			continue
		}
		// Assignment requires a current advertisement of the typed batch contract,
		// approved origin and enrolled encryption key, including resumed operations.
		enrolled, err := store.AppDynamicsCollectorPublicKey(c.ProjectID, c.ID, work.Connection.Config.Endpoint, now)
		if err != nil || enrolled != work.CollectorPublicKey {
			continue
		}
		claimed, err := store.ClaimAppDynamicsInspection(op.ID, owner, now, lease)
		if err != nil || !claimed {
			continue
		}
		credentials, err := resolveAppDynamicsCredentials(r.Context(), s.runner.secretResolverForProject(c.ProjectID), work.Connection)
		if err != nil {
			_, _ = store.CompleteAppDynamicsInspection(op.ID, owner, "{}", "AppDynamics credential unavailable", now)
			continue
		}
		cfg, err := s.appDynamicsQuotaConfig(r.Context(), work.Connection, credentials, "collector:"+c.ID)
		if err != nil {
			_, _ = store.CompleteAppDynamicsInspection(op.ID, owner, "{}", "AppDynamics executor quota temporarily unavailable", now)
			continue
		}
		job := collectoragent.InspectionJob{ID: op.ID, ProjectID: op.ProjectID, ConnectionID: op.ConnectionID, ConnectionRevision: op.ConnectionRevision, CollectorID: c.ID, Kind: op.Kind, ApplicationID: work.ApplicationID, EntityKind: work.EntityKind, Path: work.Path, Config: cfg, Selection: work.Selection, OwnerToken: owner, LeaseExpiresAt: lease}
		publicKey, decodeErr := base64.StdEncoding.DecodeString(work.CollectorPublicKey)
		if decodeErr != nil {
			continue
		}
		job.Credentials, err = collectoragent.SealInspectionCredentials(job, publicKey, collectoragent.AppDynamicsCredentialBundle(credentials))
		if err != nil {
			continue
		}
		envelope, err := collectoragent.SignInspectionJob(job, key)
		if err != nil {
			continue
		}
		out = append(out, envelope)
	}
	prometheusJobs, prometheusErr := s.pollPrometheusInspections(r, c, capacity-len(out), key)
	if prometheusErr != nil {
		writeError(w, 503, "cannot poll provider inspections")
		return
	}
	out = append(out, prometheusJobs...)
	writeJSON(w, 200, out)
}
func (s *Server) handleAppDynamicsInspectionResult(w http.ResponseWriter, r *http.Request) {
	c := s.authorizeMonitorCollector(w, r)
	if c == nil {
		return
	}
	if !appDynamicsCollectorAllowed(c, c.ProjectID) && !prometheusCollectorAllowed(c, c.ProjectID) {
		writeError(w, 403, "AppDynamics pack is not approved")
		return
	}
	store := s.appDynamicsInspectionStore(w)
	if store == nil {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var result collectoragent.InspectionResult
	if !decodeAppDynamicsJSON(w, r, &result) {
		return
	}
	key, err := monitorSigningKey()
	if err != nil {
		writeError(w, 503, err.Error())
		return
	}
	job, err := collectoragent.VerifyInspectionJob(result.Job, key.Public().(ed25519.PublicKey), c.ID, time.Now().UTC())
	if err != nil || job.ProjectID != c.ProjectID || job.ID != r.PathValue("operation_id") {
		writeError(w, 403, "inspection authorization rejected")
		return
	}
	if job.Prometheus != nil {
		s.handlePrometheusInspectionResult(w, r, c, result, job)
		return
	}
	op, err := store.GetAppDynamicsInspection(job.ID)
	if err != nil || op.ProjectID != c.ProjectID || op.CollectorID != c.ID || op.ConnectionID != job.ConnectionID || op.ConnectionRevision != job.ConnectionRevision || op.OwnerToken != job.OwnerToken {
		writeError(w, 403, "inspection result does not match operation")
		return
	}
	if len(result.Entities) > 200 || len(result.Message) > 1024 {
		writeError(w, 400, "inspection result exceeds limits")
		return
	}
	message := ""
	if result.Message != "" {
		message = "AppDynamics inspection failed"
	}
	payload := struct {
		Capabilities appdynamics.Capabilities `json:"capabilities"`
		Entities     []appdynamics.Entity     `json:"entities,omitempty"`
		Truncated    bool                     `json:"truncated,omitempty"`
	}{Capabilities: sanitizeAppDynamicsCapabilities(result.Capabilities), Entities: sanitizeAppDynamicsEntities(result.Entities), Truncated: len(result.Entities) >= 200 || result.Truncated}
	body, err := json.Marshal(payload)
	if err != nil {
		writeError(w, 400, "invalid inspection result")
		return
	}
	if op.State == "complete" || op.State == "error" {
		if op.ResultJSON == string(body) && op.Error == message {
			writeJSON(w, 200, map[string]any{"accepted": true})
			return
		}
		writeError(w, 409, "inspection result already committed")
		return
	}
	completed, err := store.CompleteAppDynamicsInspection(op.ID, job.OwnerToken, string(body), message, time.Now().UTC())
	if err != nil || !completed {
		writeError(w, 409, "inspection lease expired or operation changed")
		return
	}
	if op.Kind == "test" {
		var work appDynamicsInspectionWork
		if json.Unmarshal([]byte(op.PayloadJSON), &work) == nil && work.Selection == nil {
			s.publishAppDynamicsInspectionHealth(op, message, payload.Capabilities)
		}
	}
	writeJSON(w, 200, map[string]any{"accepted": true})
}
