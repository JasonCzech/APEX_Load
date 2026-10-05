package api

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/collectoragent"
	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"github.com/jasonczech/apexload/pkg/prometheus"
	"github.com/jasonczech/apexload/pkg/storage"
)

type PrometheusInspectionRequest struct {
	Selection *prometheus.Selection `json:"selection,omitempty"`
	Kind      string                `json:"kind,omitempty"`
	Selector  string                `json:"selector,omitempty"`
	Label     string                `json:"label,omitempty"`
	FromUTC   time.Time             `json:"from_utc,omitempty"`
	ToUTC     time.Time             `json:"to_utc,omitempty"`
}
type prometheusInspectionWork struct {
	Connection         *storage.PrometheusConnectionSnapshot `json:"connection"`
	Selection          *prometheus.Selection                 `json:"selection,omitempty"`
	CollectorPublicKey string                                `json:"collector_public_key,omitempty"`
	Kind               string                                `json:"kind,omitempty"`
	Selector           string                                `json:"selector,omitempty"`
	Label              string                                `json:"label,omitempty"`
	FromUTC            time.Time                             `json:"from_utc,omitempty"`
	ToUTC              time.Time                             `json:"to_utc,omitempty"`
}
type PrometheusOperationResponse = AppDynamicsOperationResponse

func prometheusOperationResponse(op *storage.PrometheusInspection) PrometheusOperationResponse {
	return appDynamicsOperationResponse(op)
}

type prometheusInspectionProjection struct {
	Capabilities prometheus.Capabilities             `json:"capabilities"`
	Entities     []monitorprovider.Entity            `json:"entities,omitempty"`
	Points       []analysis.MetricPoint              `json:"points,omitempty"`
	Coverage     map[string]monitorprovider.Coverage `json:"coverage,omitempty"`
	Truncated    bool                                `json:"truncated,omitempty"`
}

func (s *Server) prometheusInspectionStore(w http.ResponseWriter) storage.PrometheusInspectionStore {
	store, ok := s.store.(storage.PrometheusInspectionStore)
	if !ok || store.EnsurePrometheusInspectionSchema() != nil {
		writeError(w, 503, "Prometheus operation storage unavailable")
		return nil
	}
	return store
}
func (s *Server) handleTestPrometheusConnection(w http.ResponseWriter, r *http.Request) {
	s.createPrometheusInspection(w, r, "test")
}
func (s *Server) handleDiscoverPrometheusConnection(w http.ResponseWriter, r *http.Request) {
	s.createPrometheusInspection(w, r, "discover")
}
func (s *Server) handlePreviewPrometheusConnection(w http.ResponseWriter, r *http.Request) {
	s.createPrometheusInspection(w, r, "preview")
}
func (s *Server) createPrometheusInspection(w http.ResponseWriter, r *http.Request, kind string) {
	connection, _ := s.loadPrometheusConnection(w, r)
	if connection == nil {
		return
	}
	store := s.prometheusInspectionStore(w)
	if store == nil {
		return
	}
	var req PrometheusInspectionRequest
	r.Body = http.MaxBytesReader(w, r.Body, 96<<10)
	if r.ContentLength != 0 && !decodePrometheusJSON(w, r, &req) {
		return
	}
	work := prometheusInspectionWork{Connection: connection.Snapshot(), Selection: req.Selection, Kind: req.Kind, Selector: req.Selector, Label: req.Label, FromUTC: req.FromUTC, ToUTC: req.ToUTC}
	if work.Connection.Config.Validate() != nil {
		writeError(w, 409, "connection transport is incomplete")
		return
	}
	if work.Selection != nil {
		work.Selection.ProjectID = connection.ProjectID
		work.Selection.QueryOffset = 0
		work.Selection.QueryTime = nil
		if work.Selection.Validate() != nil {
			writeError(w, 400, "invalid selected Prometheus queries")
			return
		}
	}
	if kind == "preview" {
		if work.Selection == nil {
			writeError(w, 400, "preview requires a query selection")
			return
		}
		now := time.Now().UTC()
		if work.ToUTC.IsZero() {
			work.ToUTC = now
		}
		if work.FromUTC.IsZero() {
			work.FromUTC = work.ToUTC.Add(-5 * time.Minute)
		}
		work.FromUTC, work.ToUTC = work.FromUTC.UTC(), work.ToUTC.UTC()
		if !work.ToUTC.After(work.FromUTC) || work.ToUTC.Sub(work.FromUTC) > 10*time.Minute || work.ToUTC.After(now.Add(30*time.Second)) {
			writeError(w, 400, "preview window must be positive and at most ten minutes")
			return
		}
	}
	if kind == "discover" {
		if !slices.Contains([]string{"metrics", "labels", "label_values", "series", "targets", "metadata"}, work.Kind) || len(work.Selector) > 8192 || len(work.Label) > 128 {
			writeError(w, 400, "invalid Prometheus discovery selection")
			return
		}
	}
	if _, err := resolvePrometheusCredentials(r.Context(), s.runner.prometheusSecretResolverForProject(connection.ProjectID), work.Connection); err != nil {
		writeError(w, 409, err.Error())
		return
	}
	collector := ""
	if connection.CollectionMode == "collector" {
		collector = connection.CollectorAgentID
		agent, err := s.store.GetMonitoringCollector(collector)
		if err != nil || !prometheusCollectorAllowed(agent, connection.ProjectID) {
			writeError(w, 409, "assigned Prometheus collector unavailable")
			return
		}
		key, err := store.PrometheusCollectorPublicKey(connection.ProjectID, collector, connection.Config.Endpoint, time.Now().UTC(), work.Connection.RequiresCredentials())
		if err != nil {
			writeError(w, 409, "assigned collector destination is not enrolled and ready")
			return
		}
		if _, err = monitorSigningKey(); err != nil {
			writeError(w, 503, err.Error())
			return
		}
		work.CollectorPublicKey = key
	}
	body, err := json.Marshal(work)
	if err != nil {
		writeError(w, 400, "invalid inspection configuration")
		return
	}
	now := time.Now().UTC()
	op := &storage.PrometheusInspection{ID: "prom-inspection-" + uuid.NewString(), ProjectID: connection.ProjectID, ConnectionID: connection.ID, ConnectionRevision: connection.Revision, CollectorID: collector, Kind: kind, PayloadJSON: string(body), CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	if err = store.CreatePrometheusInspection(op); err != nil {
		writeError(w, 503, "cannot queue Prometheus inspection")
		return
	}
	s.recordAuditEvent(r, "monitoring.prometheus."+kind, "queued", "prometheus_connection", connection.ID, connection.ProjectID, map[string]any{"operation_id": op.ID, "revision": connection.Revision, "collection_mode": connection.CollectionMode})
	writeJSON(w, 202, prometheusOperationResponse(op))
	if collector == "" {
		s.resumePrometheusInspections(context.Background())
	}
}
func (s *Server) handleGetPrometheusOperation(w http.ResponseWriter, r *http.Request) {
	store := s.prometheusInspectionStore(w)
	if store == nil {
		return
	}
	op, err := store.GetPrometheusInspection(r.PathValue("operation_id"))
	if err != nil || op.ProjectID != r.PathValue("id") || op.ConnectionID != r.PathValue("connection_id") {
		writeError(w, 404, "Prometheus operation not found")
		return
	}
	writeJSON(w, 200, prometheusOperationResponse(op))
	if op.CollectorID == "" && (op.State == "pending" || op.State == "running") {
		s.resumePrometheusInspections(context.Background())
	}
}
func (s *Server) resumePrometheusInspections(parent context.Context) {
	if s.runner == nil || !s.runner.isExecutionLeader() {
		return
	}
	store, ok := s.store.(storage.PrometheusInspectionStore)
	if !ok || store.EnsurePrometheusInspectionSchema() != nil {
		return
	}
	ops, err := store.ListPrometheusInspections("", "", 4)
	if err != nil {
		return
	}
	for _, op := range ops {
		owner := uuid.NewString()
		now := time.Now().UTC()
		claimed, err := store.ClaimPrometheusInspection(op.ID, owner, now, now.Add(time.Minute))
		if err != nil || !claimed {
			continue
		}
		s.launchBackground(func() { s.executeDirectPrometheusInspection(parent, store, op, owner) })
	}
}
func (s *Server) executeDirectPrometheusInspection(parent context.Context, store storage.PrometheusInspectionStore, op *storage.PrometheusInspection, owner string) {
	ctx, cancel := s.runner.executionOperationContext(parent)
	defer cancel()
	// Queueing and execution share the persisted one-minute operation deadline.
	// A shorter parent deadline still applies; restarting must not extend expiry.
	ctx, stop := context.WithDeadline(ctx, op.ExpiresAt)
	defer stop()
	var work prometheusInspectionWork
	projection := prometheusInspectionProjection{}
	message := ""
	if json.Unmarshal([]byte(op.PayloadJSON), &work) != nil || work.Connection == nil {
		message = "invalid inspection configuration"
	} else {
		current, currentErr := s.store.(storage.PrometheusConnectionStore).GetPrometheusConnection(op.ConnectionID)
		if currentErr != nil || current.ProjectID != op.ProjectID || current.Revision != op.ConnectionRevision {
			_, _ = store.CompletePrometheusInspection(op.ID, owner, "{}", "Prometheus connection changed before inspection", time.Now().UTC())
			return
		}
		credentials, err := resolvePrometheusCredentials(ctx, s.runner.prometheusSecretResolverForProject(op.ProjectID), work.Connection)
		var client *prometheus.Client
		if err == nil {
			var config prometheus.Config
			config, err = s.prometheusQuotaConfig(ctx, work.Connection, credentials, "direct:"+s.controllerID)
			if err == nil {
				client, err = s.newPrometheusClient(config, credentials)
			}
		}
		if err == nil {
			switch op.Kind {
			case "test":
				if work.Selection != nil {
					projection.Capabilities, err = client.TestSelection(ctx, *work.Selection)
				} else {
					projection.Capabilities, err = client.Test(ctx)
				}
			case "discover":
				projection.Entities, err = client.Discover(ctx, work.Selector, work.Kind, work.Label)
			case "preview":
				var result prometheus.Result
				result, err = client.Collect(ctx, *work.Selection, work.FromUTC, work.ToUTC, "inspection:"+op.ID)
				projection.Points, projection.Coverage, projection.Capabilities = result.Points, result.Coverage, result.Capabilities
			default:
				err = errors.New("unsupported inspection")
			}
		}
		if err != nil {
			message = "Prometheus inspection failed"
		}
	}
	if ctx.Err() != nil || !s.runner.isExecutionLeader() {
		return
	}
	projection = sanitizePrometheusInspectionProjection(projection, op.ProjectID)
	if op.Kind == "test" && !projection.Capabilities.Metrics {
		message = "Prometheus metric API is unavailable"
	}
	data, err := json.Marshal(projection)
	if err != nil || len(data) > 1<<20 {
		data = []byte("{}")
		message = "Prometheus inspection result exceeds limits"
	}
	completed, err := store.CompletePrometheusInspection(op.ID, owner, string(data), message, time.Now().UTC())
	if err == nil && completed && op.Kind == "test" && work.Selection == nil {
		s.publishPrometheusInspectionHealth(op, message, projection.Capabilities)
	}
}
func (s *Server) publishPrometheusInspectionHealth(op *storage.PrometheusInspection, message string, caps prometheus.Capabilities) {
	if store, ok := s.store.(storage.PrometheusConnectionStore); ok {
		status := "connected"
		if message != "" {
			status = "error"
		}
		_, _ = store.UpdatePrometheusConnectionHealth(op.ConnectionID, op.ProjectID, op.ConnectionRevision, status, message, caps, time.Now().UTC())
	}
}
func sanitizePrometheusCapabilities(c prometheus.Capabilities) prometheus.Capabilities {
	if c.BackendProfile != "prometheus" && c.BackendProfile != "azure_managed" {
		c.BackendProfile = ""
	}
	c.Version = boundedtext.TruncateUTF8(scrubELKText(c.Version), 128)
	status := map[string]string{}
	for _, key := range []string{"metrics", "freshness", "discovery", "targets", "alerts", "exemplars", "metadata", "buildinfo", "controller", "authentication"} {
		value := c.Status[key]
		if slices.Contains([]string{"ok", "partial", "authentication_failed", "permission_denied", "throttled", "available", "available_no_data", "no_data", "empty", "not_configured", "not_selected", "unavailable", "unauthorized", "forbidden", "unsupported", "rate_limited", "budget_exhausted", "unlicensed", "unreachable", "error"}, value) {
			status[key] = value
		}
	}
	c.Status = status
	return c
}
func sanitizePrometheusInspectionProjection(p prometheusInspectionProjection, project string) prometheusInspectionProjection {
	p.Capabilities = sanitizePrometheusCapabilities(p.Capabilities)
	if len(p.Entities) >= 200 {
		p.Truncated = true
		p.Entities = p.Entities[:min(200, len(p.Entities))]
	}
	for i := range p.Entities {
		e := &p.Entities[i]
		if e.Type == "truncation" {
			p.Truncated = true
		}
		e.ID = boundedtext.TruncateUTF8(scrubELKText(e.ID), 128)
		e.Name = boundedtext.TruncateUTF8(scrubELKText(e.Name), 512)
		e.Path = boundedtext.TruncateUTF8(scrubELKText(e.Path), 4096)
		e.Type = boundedtext.TruncateUTF8(e.Type, 64)
		e.ParentID = ""
		fields, omitted := prometheus.SafeDiscoveryEntityFields(e.Type, e.EntityIDs)
		// Reapply the public API's stricter disclosure filter to untrusted collector values.
		for key, value := range fields {
			if scrubELKText(value) != value {
				delete(fields, key)
				omitted = true
			}
		}
		if len(fields) == 0 {
			fields = nil
		}
		e.EntityIDs = fields
		p.Truncated = p.Truncated || omitted
	}
	if len(p.Points) > 512 {
		p.Truncated = true
		p.Points = p.Points[:512]
	}
	filtered := p.Points[:0]
	for _, point := range p.Points {
		point.ProjectID = project
		point.Source = "preview"
		point.Service = ""
		point.Host = ""
		point.DatasetID = ""
		point.ReceivedAt = time.Time{}
		if point.Validate() == nil {
			filtered = append(filtered, point)
		}
	}
	p.Points = filtered
	// Arbitrary collector coverage text is not an administrator-controlled field.
	coverage := map[string]monitorprovider.Coverage{}
	for key, value := range p.Coverage {
		if len(coverage) >= 24 {
			p.Truncated = true
			break
		}
		if len(key) > 256 {
			continue
		}
		value.Message = boundedtext.TruncateUTF8(scrubELKText(value.Message), 512)
		value.Warnings = sanitizePrometheusInspectionMessages(value.Warnings)
		value.Infos = sanitizePrometheusInspectionMessages(value.Infos)
		coverage[key] = value
	}
	p.Coverage = coverage
	return p
}

func sanitizePrometheusInspectionMessages(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, min(len(values), 20))
	for _, value := range values[:min(len(values), 20)] {
		value = scrubELKText(value)
		value = strings.Map(func(character rune) rune {
			if character < 32 || character == 127 {
				return ' '
			}
			return character
		}, value)
		out = append(out, boundedtext.TruncateUTF8(value, 256))
	}
	return out
}
func (s *Server) pollPrometheusInspections(r *http.Request, c *storage.MonitoringCollector, capacity int, key ed25519.PrivateKey) ([]collectoragent.SignedInspectionJob, error) {
	store, ok := s.store.(storage.PrometheusInspectionStore)
	if !ok || capacity < 1 || !prometheusCollectorAllowed(c, c.ProjectID) {
		return nil, nil
	}
	ops, err := store.ListPrometheusInspections(c.ProjectID, c.ID, capacity)
	if err != nil {
		return nil, err
	}
	out := []collectoragent.SignedInspectionJob{}
	for _, op := range ops {
		var work prometheusInspectionWork
		if json.Unmarshal([]byte(op.PayloadJSON), &work) != nil || work.Connection == nil {
			continue
		}
		now := time.Now().UTC()
		enrolled, err := store.PrometheusCollectorPublicKey(c.ProjectID, c.ID, work.Connection.Config.Endpoint, now, work.Connection.RequiresCredentials())
		if err != nil || enrolled != work.CollectorPublicKey {
			continue
		}
		owner := uuid.NewString()
		lease := minTime(now.Add(time.Minute), op.ExpiresAt)
		claimed, err := store.ClaimPrometheusInspection(op.ID, owner, now, lease)
		if err != nil || !claimed {
			continue
		}
		current, currentErr := s.store.(storage.PrometheusConnectionStore).GetPrometheusConnection(op.ConnectionID)
		if currentErr != nil || current.ProjectID != op.ProjectID || current.Revision != op.ConnectionRevision {
			_, _ = store.CompletePrometheusInspection(op.ID, owner, "{}", "Prometheus connection changed before inspection", now)
			continue
		}
		credentials, err := resolvePrometheusCredentials(r.Context(), s.runner.prometheusSecretResolverForProject(c.ProjectID), work.Connection)
		if err != nil {
			_, _ = store.CompletePrometheusInspection(op.ID, owner, "{}", "Prometheus credential unavailable", now)
			continue
		}
		selection := prometheus.Selection{ProjectID: op.ProjectID}
		if work.Selection != nil {
			selection = *work.Selection
		}
		config, err := s.prometheusQuotaConfig(r.Context(), work.Connection, credentials, "collector:"+c.ID)
		if err != nil {
			_, _ = store.CompletePrometheusInspection(op.ID, owner, "{}", "Prometheus request quota unavailable", now)
			continue
		}
		job := collectoragent.InspectionJob{Provider: "prometheus", Prometheus: &prometheus.JobConfig{Config: config, Selection: selection}, ID: op.ID, ProjectID: op.ProjectID, ConnectionID: op.ConnectionID, ConnectionRevision: op.ConnectionRevision, CollectorID: c.ID, Kind: op.Kind, EntityKind: work.Kind, Selector: work.Selector, Label: work.Label, FromUTC: work.FromUTC, ToUTC: work.ToUTC, OwnerToken: owner, LeaseExpiresAt: lease}
		if work.Connection.RequiresCredentials() {
			publicKey, err := base64.StdEncoding.DecodeString(enrolled)
			if err != nil {
				continue
			}
			job.Credentials, err = collectoragent.SealInspectionCredentials(job, publicKey, collectoragent.PrometheusCredentialBundle(credentials))
			if err != nil {
				continue
			}
		}
		envelope, err := collectoragent.SignInspectionJob(job, key)
		if err == nil {
			out = append(out, envelope)
		}
	}
	return out, nil
}
func (s *Server) handlePrometheusInspectionResult(w http.ResponseWriter, r *http.Request, c *storage.MonitoringCollector, result collectoragent.InspectionResult, job collectoragent.InspectionJob) {
	if !prometheusCollectorAllowed(c, c.ProjectID) {
		writeError(w, 403, "Prometheus pack is not approved")
		return
	}
	store := s.prometheusInspectionStore(w)
	if store == nil {
		return
	}
	op, err := store.GetPrometheusInspection(job.ID)
	if err != nil || op.ProjectID != c.ProjectID || op.CollectorID != c.ID || op.ConnectionID != job.ConnectionID || op.ConnectionRevision != job.ConnectionRevision || op.OwnerToken != job.OwnerToken {
		writeError(w, 403, "inspection result does not match operation")
		return
	}
	if result.PrometheusCapabilities == nil && op.Kind == "test" || len(result.Entities) > 200 || len(result.Points) > 512 || len(result.Message) > 1024 {
		writeError(w, 400, "inspection result exceeds limits")
		return
	}
	caps := prometheus.Capabilities{}
	if result.PrometheusCapabilities != nil {
		caps = *result.PrometheusCapabilities
	}
	payload := sanitizePrometheusInspectionProjection(prometheusInspectionProjection{Capabilities: caps, Entities: result.Entities, Points: result.Points, Coverage: result.Coverage, Truncated: result.Truncated}, c.ProjectID)
	message := ""
	if result.Message != "" {
		message = "Prometheus inspection failed"
	}
	if op.Kind == "test" && !payload.Capabilities.Metrics {
		message = "Prometheus metric API is unavailable"
	}
	body, err := json.Marshal(payload)
	if err != nil || len(body) > 1<<20 {
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
	completed, err := store.CompletePrometheusInspection(op.ID, job.OwnerToken, string(body), message, time.Now().UTC())
	if err != nil || !completed {
		writeError(w, 409, "inspection lease expired or operation changed")
		return
	}
	if op.Kind == "test" {
		var work prometheusInspectionWork
		if json.Unmarshal([]byte(op.PayloadJSON), &work) == nil && work.Selection == nil {
			s.publishPrometheusInspectionHealth(op, message, payload.Capabilities)
		}
	}
	writeJSON(w, 200, map[string]any{"accepted": true})
}
func prometheusCollectorAllowed(c *storage.MonitoringCollector, project string) bool {
	return c != nil && c.ProjectID == project && slices.Contains(c.AllowedMetricPacks, "prometheus-query")
}

// verifyPreparedPrometheus reads only the selected monitoring APIs from the
// frozen execution location. It never issues application/load traffic.
func (s *Server) verifyPreparedPrometheus(parent context.Context, source storage.MonitorSource, snapshot *storage.PrometheusConnectionSnapshot, rules []analysis.MonitorRule) error {
	if snapshot == nil || source.Prometheus == nil {
		return errors.New("Prometheus preparation selection unavailable")
	}
	selection := *source.Prometheus
	selection.QueryOffset = 0
	selection.QueryTime = nil
	if selection.Validate() != nil {
		return errors.New("Prometheus preparation selection is invalid")
	}
	connections, ok := s.store.(storage.PrometheusConnectionStore)
	if !ok {
		return errors.New("Prometheus preparation connection storage unavailable")
	}
	current, err := connections.GetPrometheusConnection(snapshot.ConnectionID)
	if err != nil || current.ProjectID != source.ProjectID || snapshot.ProjectID != source.ProjectID || current.Revision != snapshot.Revision || current.Readiness() != "ready" {
		return errors.New("Prometheus preparation connection changed or is no longer ready")
	}
	ctx, cancel := context.WithTimeout(parent, time.Minute)
	defer cancel()
	if source.ExecutorMode == "direct" {
		credentials, err := resolvePrometheusCredentials(ctx, s.runner.prometheusSecretResolverForProject(source.ProjectID), snapshot)
		if err != nil {
			return err
		}
		config, err := s.prometheusQuotaConfig(ctx, snapshot, credentials, "direct:"+s.controllerID)
		if err != nil {
			return errors.New("Prometheus preparation request quota unavailable")
		}
		client, err := s.newPrometheusClient(config, credentials)
		if err != nil {
			return errors.New("Prometheus preparation transport unavailable")
		}
		caps, err := client.TestSelection(ctx, selection)
		if errors.Is(err, prometheus.ErrBudget) {
			return errors.New("Prometheus preparation collection budget is insufficient for selected queries")
		}
		if err != nil || !caps.Metrics || selection.Targets && !caps.Targets || selection.Alerts && !caps.Alerts {
			return errors.New("Prometheus preparation selected capability unavailable")
		}
		return nil
	}
	store, ok := s.store.(storage.PrometheusInspectionStore)
	if !ok {
		return errors.New("Prometheus preparation operation storage unavailable")
	}
	key, err := store.PrometheusCollectorPublicKey(source.ProjectID, source.CollectorID, snapshot.Config.Endpoint, time.Now().UTC(), snapshot.RequiresCredentials())
	if err != nil || key != source.CollectorPublicKey {
		return errors.New("Prometheus preparation collector enrollment changed")
	}
	credentials, err := resolvePrometheusCredentials(ctx, s.runner.prometheusSecretResolverForProject(source.ProjectID), snapshot)
	if err != nil {
		return err
	}
	if err = preflightPrometheusCollectorPayload(source, snapshot, selection, credentials, key, rules); err != nil {
		return err
	}
	work := prometheusInspectionWork{Connection: snapshot, Selection: &selection, CollectorPublicKey: key}
	body, err := json.Marshal(work)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	op := &storage.PrometheusInspection{ID: "prom-inspection-" + uuid.NewString(), ProjectID: source.ProjectID, ConnectionID: source.ConnectionID, ConnectionRevision: snapshot.Revision, CollectorID: source.CollectorID, Kind: "test", PayloadJSON: string(body), CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	if err = store.CreatePrometheusInspection(op); err != nil {
		return err
	}
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return errors.New("Prometheus preparation verification timed out")
		case <-ticker.C:
			current, err := store.GetPrometheusInspection(op.ID)
			if err != nil {
				return errors.New("Prometheus preparation operation unavailable")
			}
			if current.State == "error" {
				return errors.New("Prometheus preparation verification failed")
			}
			if current.State == "complete" {
				var p prometheusInspectionProjection
				if json.Unmarshal([]byte(current.ResultJSON), &p) != nil || !p.Capabilities.Metrics || selection.Targets && !p.Capabilities.Targets || selection.Alerts && !p.Capabilities.Alerts {
					return errors.New("Prometheus preparation selected capability unavailable")
				}
				return nil
			}
		}
	}
}

// No test or collector job is created here. Serialize the same signed/sealed
// authorization shapes locally so preparation fails before a worker is asked
// to poll a configuration that cannot fit the transport contract.
func preflightPrometheusCollectorPayload(source storage.MonitorSource, snapshot *storage.PrometheusConnectionSnapshot, selection prometheus.Selection, credentials prometheus.Credentials, publicKey string, rules []analysis.MonitorRule) error {
	signing, err := monitorSigningKey()
	if err != nil {
		return errors.New("Prometheus collector signing configuration unavailable")
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	lease := now.Add(time.Minute)
	key, err := base64.StdEncoding.DecodeString(publicKey)
	if err != nil {
		return errors.New("Prometheus collector encryption identity unavailable")
	}
	inspection := collectoragent.InspectionJob{Provider: "prometheus", Prometheus: &prometheus.JobConfig{Config: snapshot.Config, Selection: selection}, ID: "preflight", ProjectID: source.ProjectID, ConnectionID: source.ConnectionID, ConnectionRevision: snapshot.Revision, CollectorID: source.CollectorID, Kind: "test", OwnerToken: "preflight", LeaseExpiresAt: lease}
	if snapshot.RequiresCredentials() {
		inspection.Credentials, err = collectoragent.SealInspectionCredentials(inspection, key, collectoragent.PrometheusCredentialBundle(credentials))
		if err != nil {
			return errors.New("Prometheus credential bundle exceeds the collector transport contract")
		}
	}
	if _, err = collectoragent.SignInspectionJob(inspection, signing); err != nil {
		return errors.New("Prometheus inspection configuration exceeds the signed payload budget")
	}
	job := collectoragent.RunJob{ID: strings.Repeat("j", 512), ProjectID: source.ProjectID, RunID: uuid.NewString(), CollectorID: source.CollectorID, SourceID: source.ID, SourceRevision: source.Revision, Pack: "prometheus-query", Destination: snapshot.Config.Endpoint, IntervalSeconds: source.IntervalSeconds, StartedAt: now, ExpiresAt: lease, LeaseExpiresAt: lease, MaxBatches: 32, Prometheus: &prometheus.JobConfig{Config: snapshot.Config, Selection: selection}}
	if snapshot.RequiresCredentials() {
		job.Credentials, err = collectoragent.SealCredentials(job, key, collectoragent.PrometheusCredentialBundle(credentials))
		if err != nil {
			return errors.New("Prometheus credential bundle exceeds the collector transport contract")
		}
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	job.TerminalWindows = prometheusTerminalWindows(storage.RunMonitorJob{Source: source, StartedAt: start, LoadEndedAt: &end, FrozenMonitorRules: rules, RulesFrozen: true})
	if _, err = collectoragent.SignRunJob(job, signing); err != nil {
		return errors.New("Prometheus run configuration and terminal windows exceed the signed payload budget")
	}
	return nil
}
