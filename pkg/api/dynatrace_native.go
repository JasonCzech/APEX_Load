package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dynatrace"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

func dynatraceCollectionCapabilities(snapshot *storage.DynatraceConnectionSnapshot, selections ...*dynatrace.Selection) dynatrace.Capabilities {
	if len(selections) == 0 || selections[0] == nil {
		return snapshot.AllowedCapabilities
	}
	selection := selections[0]
	capabilities := dynatrace.Capabilities{Metrics: len(selection.Metrics) > 0, Problems: selection.Problems, Events: selection.Events, ClassicTopology: selection.TopologyMode == "classic", Smartscape: selection.TopologyMode == "smartscape"}
	for _, query := range selection.Grail {
		switch query.Kind {
		case "metric":
			capabilities.GrailMetrics = true
		case "log":
			capabilities.Logs = true
		case "span", "trace":
			capabilities.Spans = true
		case "event":
			capabilities.GrailEvents = true
		}
	}
	return capabilities
}

func dynatraceOperationSnapshot(snapshot *storage.DynatraceConnectionSnapshot, kind string, options dynatrace.DiscoveryRequest) *storage.DynatraceConnectionSnapshot {
	if snapshot == nil || kind != "discover" {
		return snapshot
	}
	copy := *snapshot
	if options.Kind == "smartscape" || options.TopologyMode == "smartscape" {
		copy.AllowedCapabilities.Metrics, copy.AllowedCapabilities.Problems, copy.AllowedCapabilities.Events, copy.AllowedCapabilities.ClassicTopology = false, false, false, false
	} else {
		copy.AllowedCapabilities.Logs, copy.AllowedCapabilities.Spans, copy.AllowedCapabilities.GrailMetrics, copy.AllowedCapabilities.GrailEvents, copy.AllowedCapabilities.Smartscape = false, false, false, false, false
	}
	return &copy
}

func resolveNativeDynatraceCredentials(ctx context.Context, resolver engine.SecretResolver, snapshot *storage.DynatraceConnectionSnapshot, selections ...*dynatrace.Selection) (dynatrace.Credentials, error) {
	var out dynatrace.Credentials
	if snapshot == nil {
		return out, errors.New("Dynatrace connection snapshot unavailable")
	}
	allowed := dynatraceCollectionCapabilities(snapshot, selections...)
	useEnvironment := allowed.Metrics || allowed.Problems || allowed.Events || allowed.ClassicTopology
	usePlatform := allowed.Logs || allowed.Spans || allowed.GrailMetrics || allowed.GrailEvents || allowed.Smartscape
	for _, item := range []struct {
		ref   string
		value *string
		used  bool
	}{{snapshot.EnvironmentTokenSecret, &out.EnvironmentToken, useEnvironment}, {snapshot.PlatformTokenSecret, &out.PlatformToken, usePlatform && snapshot.Config.PlatformAuthType == dynatrace.AuthToken}, {snapshot.OAuthClientIDSecret, &out.ClientID, usePlatform && snapshot.Config.PlatformAuthType == dynatrace.AuthOAuth}, {snapshot.OAuthClientSecretSecret, &out.ClientSecret, usePlatform && snapshot.Config.PlatformAuthType == dynatrace.AuthOAuth}} {
		if item.ref == "" || !item.used {
			continue
		}
		value, ok := engine.ResolveSecret(ctx, resolver, item.ref)
		if !ok || value == "" {
			return dynatrace.Credentials{}, errors.New("Dynatrace project credential unavailable")
		}
		*item.value = value
	}
	// Hash resolved versions in memory. Rotation changes the cache identity even
	// for providers that do not expose a version identifier through the resolver.
	sum := sha256.Sum256([]byte(strings.Join([]string{snapshot.ProjectID, snapshot.Config.EnvironmentURL, snapshot.Config.PlatformURL, snapshot.Config.OAuthResource, strings.Join(snapshot.Config.OAuthScopes, " "), out.EnvironmentToken, out.PlatformToken, out.ClientID, out.ClientSecret}, "\x00")))
	out.VersionIdentity = hex.EncodeToString(sum[:])
	if useEnvironment && out.EnvironmentToken == "" {
		return dynatrace.Credentials{}, errors.New("Dynatrace Environment credential unavailable")
	}
	if usePlatform && (snapshot.Config.PlatformAuthType == dynatrace.AuthToken && out.PlatformToken == "" || snapshot.Config.PlatformAuthType == dynatrace.AuthOAuth && (out.ClientID == "" || out.ClientSecret == "")) {
		return dynatrace.Credentials{}, errors.New("Dynatrace Platform credential unavailable")
	}
	return out, nil
}

func validateDynatraceSecretReferences(c *storage.DynatraceConnection) error {
	for _, ref := range []string{c.EnvironmentTokenSecret, c.PlatformTokenSecret, c.OAuthClientIDSecret, c.OAuthClientSecretSecret} {
		if ref != "" {
			if _, err := normalizeProjectSecretName(ref); err != nil {
				return errors.New("Dynatrace credentials require valid logical project secret names")
			}
		}
	}
	return nil
}

func (s *Server) nativeDynatraceClient(ctx context.Context, snapshot *storage.DynatraceConnectionSnapshot, executor string, selections ...*dynatrace.Selection) (*dynatrace.Client, error) {
	credentials, err := resolveNativeDynatraceCredentials(ctx, s.runner.secretResolverForProject(snapshot.ProjectID), snapshot, selections...)
	if err != nil {
		return nil, err
	}
	cfg, err := s.dynatraceQuotaConfig(ctx, snapshot, credentials, executor, selections...)
	if err != nil {
		return nil, err
	}
	return dynatrace.NewClient(cfg, credentials)
}
func (s *Server) dynatraceQuotaConfig(ctx context.Context, snapshot *storage.DynatraceConnectionSnapshot, credentials dynatrace.Credentials, executor string, selections ...*dynatrace.Selection) (dynatrace.Config, error) {
	cfg := snapshot.Config.Defaults()
	if ctx.Err() != nil {
		return cfg, ctx.Err()
	}
	quotas, ok := s.store.(interface {
		AllocateDynatraceQuota(string, string, time.Time) (bool, error)
	})
	if !ok {
		return cfg, errors.New("Dynatrace quota storage unavailable")
	}
	// Share leases by the actual upstream principal, independently for each API.
	// Unrelated credentials and unused configured origins cannot split or consume
	// a selected capability's request allowance.
	allowed := dynatraceCollectionCapabilities(snapshot, selections...)
	for _, api := range []struct{ platform, used bool }{
		{false, allowed.Metrics || allowed.Problems || allowed.Events || allowed.ClassicTopology},
		{true, allowed.Logs || allowed.Spans || allowed.GrailMetrics || allowed.GrailEvents || allowed.Smartscape},
	} {
		if !api.used {
			continue
		}
		admitted, e := quotas.AllocateDynatraceQuota(dynatrace.RequestQuotaIdentity(cfg, credentials, api.platform), executor, time.Now().UTC())
		if e != nil || !admitted {
			return cfg, errors.New("Dynatrace executor quota is busy")
		}
	}
	cfg.RequestsPerMinute = max(1, cfg.RequestsPerMinute/2)
	cfg.PlatformRequestsPerMinute = max(1, cfg.PlatformRequestsPerMinute/2)
	return cfg, nil
}

type DynatraceOperationRequest struct {
	Kind      string                     `json:"kind"`
	SourceID  string                     `json:"source_id,omitempty"`
	Options   dynatrace.DiscoveryRequest `json:"options,omitempty"`
	Selection *dynatrace.Selection       `json:"selection,omitempty"`
}
type dynatraceOperationWork struct {
	SourceID           string                               `json:"source_id,omitempty"`
	SourceRevision     int64                                `json:"source_revision,omitempty"`
	Connection         *storage.DynatraceConnectionSnapshot `json:"connection"`
	Options            dynatrace.DiscoveryRequest           `json:"options,omitempty"`
	Selection          *dynatrace.Selection                 `json:"selection,omitempty"`
	CollectorPublicKey string                               `json:"collector_public_key,omitempty"`
	WantedCapabilities []string                             `json:"wanted_capabilities,omitempty"`
	PreviewFromUTC     time.Time                            `json:"preview_from_utc,omitempty"`
	PreviewUntilUTC    time.Time                            `json:"preview_until_utc,omitempty"`
}
type DynatraceOperationResponse struct {
	ID                 string          `json:"id"`
	ConnectionID       string          `json:"connection_id"`
	ConnectionRevision int64           `json:"connection_revision"`
	Status             string          `json:"status"`
	Kind               string          `json:"kind"`
	CreatedAt          time.Time       `json:"created_at"`
	ExpiresAt          time.Time       `json:"expires_at"`
	Message            string          `json:"message,omitempty"`
	Result             json.RawMessage `json:"result,omitempty"`
}
type dynatraceOperationResult struct {
	Capabilities dynatrace.Capabilities `json:"capabilities"`
	Items        []dynatrace.Entity     `json:"items,omitempty"`
	NextCursor   string                 `json:"next_cursor,omitempty"`
	Truncated    bool                   `json:"truncated,omitempty"`
	Message      string                 `json:"message,omitempty"`
	Preview      *dynatrace.Result      `json:"preview,omitempty"`
}

func dynatraceOperationResponse(op *storage.DynatraceOperation) DynatraceOperationResponse {
	state := op.State
	if (state == "pending" || state == "running") && !op.ExpiresAt.After(time.Now().UTC()) {
		state = "expired"
	}
	var publicResult json.RawMessage
	if op.ResultJSON != "" {
		var result dynatraceOperationResult
		if json.Unmarshal([]byte(op.ResultJSON), &result) == nil {
			// Provider pagination/query tokens stay private. The public handle
			// resolves this immutable result under the next request's scope.
			if result.NextCursor != "" {
				result.NextCursor = ""
				if op.Kind == "discover" && op.State == "complete" && op.CreatedAt.After(time.Now().UTC().Add(-5*time.Minute)) {
					result.NextCursor = "dynatrace-page:" + op.ID
				}
			}
			if result.Preview != nil {
				result.Preview.Progress.Cursor = ""
				result.Preview.Progress.CursorExpiresAt = nil
				result.Preview.Progress.ScanReservationIDs = nil
			}
			publicResult, _ = json.Marshal(result)
		}
	}
	return DynatraceOperationResponse{ID: op.ID, ConnectionID: op.ConnectionID, ConnectionRevision: op.ConnectionRevision, Status: state, Kind: op.Kind, CreatedAt: op.CreatedAt, ExpiresAt: op.ExpiresAt, Message: op.Error, Result: publicResult}
}

func resolveDynatraceDiscoveryCursor(store storage.DynatraceOperationStore, connection *storage.DynatraceConnection, options *dynatrace.DiscoveryRequest) error {
	if options.Cursor == "" {
		return nil
	}
	const prefix = "dynatrace-page:"
	if !strings.HasPrefix(options.Cursor, prefix) {
		return errors.New("invalid Dynatrace discovery continuation")
	}
	op, err := store.GetDynatraceOperation(strings.TrimPrefix(options.Cursor, prefix))
	if err != nil || op == nil || connection == nil || op.Kind != "discover" || op.State != "complete" || op.ProjectID != connection.ProjectID || op.ConnectionID != connection.ID || op.ConnectionRevision != connection.Revision || op.CollectorID != connection.CollectorAgentID || op.CreatedAt.Before(time.Now().UTC().Add(-5*time.Minute)) {
		return errors.New("Dynatrace discovery continuation is expired or belongs to another configuration")
	}
	var previous dynatraceOperationWork
	var result dynatraceOperationResult
	if json.Unmarshal([]byte(op.PayloadJSON), &previous) != nil || json.Unmarshal([]byte(op.ResultJSON), &result) != nil || result.NextCursor == "" || len(result.NextCursor) > 16384 {
		return errors.New("Dynatrace discovery continuation unavailable")
	}
	want, prior := *options, previous.Options
	want.Cursor, prior.Cursor, want.Limit, prior.Limit = "", "", 0, 0
	if want != prior {
		return errors.New("Dynatrace discovery continuation cannot change query scope")
	}
	options.Cursor = result.NextCursor
	return nil
}

func dynatraceOperationSelectionAllowed(s *dynatrace.Selection, allowed dynatrace.Capabilities) bool {
	if s == nil || len(s.Metrics) > 0 && !allowed.Metrics || s.Problems && !allowed.Problems || s.Events && !allowed.Events || s.TopologyMode == "classic" && !allowed.ClassicTopology || s.TopologyMode == "smartscape" && !allowed.Smartscape {
		return false
	}
	for _, q := range s.Grail {
		if q.Kind == "log" && !allowed.Logs || (q.Kind == "span" || q.Kind == "trace") && !allowed.Spans || (q.Kind == "metric" || q.Kind == "aggregate") && !allowed.GrailMetrics || q.Kind == "event" && !allowed.GrailEvents {
			return false
		}
	}
	return true
}

func (s *Server) dynatraceOperationAdmin(r *http.Request, project string) bool {
	user, _ := AuthenticatedUserFromContext(r.Context())
	return s.authSettingsForRequest().Mode == authModeDev || user != nil && (user.hasGlobalRole(RoleSystemAdmin, RoleProjectAdmin) || userHasProjectOrParentRole(s.store, user, project, RoleProjectAdmin))
}

func (s *Server) approvedDynatracePreviewSource(c *storage.DynatraceConnection, id string) (*storage.MonitorSource, error) {
	store, ok := s.store.(storage.RunMonitorStore)
	if !ok || c == nil || c.Readiness() != "ready" {
		return nil, errors.New("approved Dynatrace source unavailable")
	}
	sources, err := store.ListMonitorSources(c.ProjectID)
	if err != nil {
		return nil, errors.New("approved Dynatrace source unavailable")
	}
	for _, source := range sources {
		if source.ID == id && source.Enabled && source.Dynatrace != nil && storage.ValidateDynatraceSourceConnection(source, c.Snapshot()) == nil && storage.ValidateDynatraceVerifiedSelection(source.Dynatrace, c.Snapshot()) == nil {
			return &source, nil
		}
	}
	return nil, errors.New("approved Dynatrace source unavailable")
}

func (s *Server) handleCreateDynatraceOperation(w http.ResponseWriter, r *http.Request) {
	c, _, ok := s.loadProjectDynatraceConnection(w, r)
	if !ok {
		return
	}
	store, ok := s.store.(storage.DynatraceOperationStore)
	if !ok {
		writeError(w, 503, "Dynatrace operation storage unavailable")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 96<<10)
	var req DynatraceOperationRequest
	if !decodeAppDynamicsJSON(w, r, &req) {
		return
	}
	admin := s.dynatraceOperationAdmin(r, c.ProjectID)
	if !admin && (req.Kind != "preview" || req.SourceID == "") {
		writeError(w, 403, "editors may preview approved Dynatrace sources only")
		return
	}
	var source *storage.MonitorSource
	if req.SourceID != "" {
		if req.Kind != "preview" || req.Selection != nil || req.Options != (dynatrace.DiscoveryRequest{}) {
			writeError(w, 400, "source previews cannot override the saved selection")
			return
		}
		var err error
		source, err = s.approvedDynatracePreviewSource(c, req.SourceID)
		if err != nil {
			writeError(w, 409, err.Error())
			return
		}
		// Clone the approved selection: runtime fields are cleared below without
		// mutating the saved source or accepting caller-controlled query text.
		selection := *source.Dynatrace
		req.Selection = &selection
	}
	if req.Kind != "verify" && req.Kind != "discover" && req.Kind != "preview" {
		writeError(w, 400, "kind must be verify, discover or preview")
		return
	}
	if c.RuntimeConfig().Validate() != nil {
		writeError(w, 409, "Dynatrace transport is incomplete")
		return
	}
	if req.Selection != nil {
		req.Selection.ProjectID = c.ProjectID
		req.Selection.Cursor = ""
		req.Selection.QueryOffset = 0
		if req.Selection.Validate() != nil {
			writeError(w, 400, "invalid Dynatrace selection")
			return
		}
		if !dynatraceOperationSelectionAllowed(req.Selection, c.Snapshot().AllowedCapabilities) {
			writeError(w, 409, "Dynatrace selection exceeds configured capabilities")
			return
		}
	}
	if req.Kind == "preview" && req.Selection == nil {
		writeError(w, 400, "preview selection is required")
		return
	}
	if req.Options.Limit < 0 || req.Options.Limit > 100 || len(req.Options.Search) > 512 || len(req.Options.Selector) > 4096 || len(req.Options.Cursor) > 16384 {
		writeError(w, 400, "discovery options exceed limits")
		return
	}
	if req.Options.Limit == 0 {
		req.Options.Limit = 100
	}
	if req.Options.Cursor != "" {
		if req.Kind != "discover" || resolveDynatraceDiscoveryCursor(store, c, &req.Options) != nil {
			writeError(w, 400, "invalid, expired or out-of-scope Dynatrace discovery continuation")
			return
		}
	}
	snapshot := dynatraceOperationSnapshot(c.Snapshot(), req.Kind, req.Options)
	if _, err := resolveNativeDynatraceCredentials(r.Context(), s.runner.secretResolverForProject(c.ProjectID), snapshot, req.Selection); err != nil {
		writeError(w, 409, err.Error())
		return
	}
	work := dynatraceOperationWork{Connection: snapshot, Options: req.Options, Selection: req.Selection}
	if source != nil {
		work.SourceID, work.SourceRevision = source.ID, source.Revision
	}
	if req.Kind == "preview" {
		work.PreviewUntilUTC = time.Now().UTC()
		work.PreviewFromUTC = work.PreviewUntilUTC.Add(-time.Minute)
	}
	if req.Kind == "verify" && req.Selection == nil {
		work.WantedCapabilities = dynatraceWantedCapabilities(c)
	}
	collector := ""
	if c.CollectionMode == "collector" {
		collector = c.CollectorAgentID
		key, err := store.DynatraceCollectorPublicKey(c.ProjectID, collector, storage.DynatraceDestination(c.Snapshot()), time.Now().UTC())
		if err != nil {
			writeError(w, 409, "assigned Dynatrace collector is not enrolled and ready")
			return
		}
		work.CollectorPublicKey = key
		if _, err = monitorSigningKey(); err != nil {
			writeError(w, 503, "collector signing configuration unavailable")
			return
		}
	}
	body, err := json.Marshal(work)
	if err != nil {
		writeError(w, 400, "invalid operation configuration")
		return
	}
	now := time.Now().UTC()
	op := &storage.DynatraceOperation{ID: "dynatrace-operation-" + uuid.NewString(), ProjectID: c.ProjectID, ConnectionID: c.ID, ConnectionRevision: c.Revision, CollectorID: collector, Kind: req.Kind, PayloadJSON: string(body), CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	if err = store.CreateDynatraceOperation(op); err != nil {
		writeError(w, 503, "cannot queue Dynatrace operation")
		return
	}
	s.recordAuditEvent(r, "monitoring.dynatrace."+req.Kind, "queued", "dynatrace_connection", c.ID, c.ProjectID, map[string]any{"operation_id": op.ID, "revision": c.Revision})
	writeJSON(w, 202, dynatraceOperationResponse(op))
	if collector == "" {
		s.resumeDynatraceOperations(context.Background())
	}
}
func (s *Server) handleGetDynatraceOperation(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(storage.DynatraceOperationStore)
	if !ok {
		writeError(w, 503, "Dynatrace operation storage unavailable")
		return
	}
	op, err := store.GetDynatraceOperation(r.PathValue("operation_id"))
	if err != nil || op.ProjectID != r.PathValue("id") || op.ConnectionID != r.PathValue("connection_id") {
		writeError(w, 404, "Dynatrace operation not found")
		return
	}
	if !s.dynatraceOperationAdmin(r, op.ProjectID) {
		var work dynatraceOperationWork
		if op.Kind != "preview" || json.Unmarshal([]byte(op.PayloadJSON), &work) != nil || work.SourceID == "" {
			writeError(w, 403, "editors may read approved-source previews only")
			return
		}
		connections, ok := s.store.(storage.DynatraceConnectionStore)
		if !ok {
			writeError(w, 503, "Dynatrace connection storage unavailable")
			return
		}
		connection, err := connections.GetDynatraceConnection(op.ConnectionID)
		if err != nil || connection == nil || connection.ProjectID != op.ProjectID {
			writeError(w, 409, "approved Dynatrace source unavailable")
			return
		}
		source, err := s.approvedDynatracePreviewSource(connection, work.SourceID)
		if err != nil || source.Revision != work.SourceRevision || connection.Revision != op.ConnectionRevision {
			writeError(w, 409, "approved Dynatrace source configuration changed")
			return
		}
	}
	writeJSON(w, 200, dynatraceOperationResponse(op))
	if op.CollectorID == "" && (op.State == "pending" || op.State == "running") {
		s.resumeDynatraceOperations(context.Background())
	}
}
func (s *Server) resumeDynatraceOperations(parent context.Context) {
	if s.runner == nil || !s.runner.isExecutionLeader() {
		return
	}
	store, ok := s.store.(storage.DynatraceOperationStore)
	if !ok {
		return
	}
	ops, err := store.ListDynatraceOperations("", "", 4)
	if err != nil {
		return
	}
	for _, op := range ops {
		owner := uuid.NewString()
		now := time.Now().UTC()
		claimed, err := store.ClaimDynatraceOperation(op.ID, owner, now, now.Add(time.Minute))
		if err != nil || !claimed {
			continue
		}
		s.launchBackground(func() { s.executeDirectDynatraceOperation(parent, store, op, owner) })
	}
}
func (s *Server) executeDirectDynatraceOperation(parent context.Context, store storage.DynatraceOperationStore, op *storage.DynatraceOperation, owner string) {
	term, err := s.runner.captureExecutionLeaseTerm()
	if err != nil {
		return
	}
	ctx, cancel := s.runner.executionOperationContext(parent)
	defer cancel()
	ctx, stop := context.WithTimeout(ctx, 45*time.Second)
	defer stop()
	var work dynatraceOperationWork
	result := dynatraceOperationResult{}
	message := ""
	if json.Unmarshal([]byte(op.PayloadJSON), &work) != nil || work.Connection == nil {
		message = "invalid operation configuration"
	} else {
		client, err := s.nativeDynatraceClient(ctx, work.Connection, "direct:"+s.controllerID, work.Selection)
		if err == nil {
			switch op.Kind {
			case "verify":
				if work.Selection != nil {
					result.Capabilities, err = client.TestSelection(ctx, *work.Selection)
				} else {
					result.Capabilities, err = client.Test(ctx)
				}
			case "discover":
				var discovery dynatrace.DiscoveryResult
				discovery, err = client.Discover(ctx, work.Options)
				result.Items, result.NextCursor, result.Truncated, result.Message = discovery.Items, discovery.NextCursor, discovery.Truncated, discovery.Message
			case "preview":
				var preview dynatrace.Result
				preview, err = client.Preview(ctx, *work.Selection, work.PreviewFromUTC, work.PreviewUntilUTC)
				if err == nil {
					err = sanitizeDynatracePreview(work, &preview)
				}
				if err == nil {
					result.Preview = &preview
				}
			default:
				err = errors.New("invalid operation kind")
			}
		}
		if err != nil {
			message = "Dynatrace operation failed"
		}
		if op.Kind == "verify" && message == "" {
			if work.Selection != nil {
				if validatePreparedDynatraceCapabilities(*work.Selection, result.Capabilities) != nil {
					message = "selected Dynatrace capabilities unavailable"
				}
			} else if !dynatraceWantedCapabilitiesAvailable(work.WantedCapabilities, result.Capabilities) {
				message = "selected Dynatrace capabilities unavailable"
			}
		}
	}
	if ctx.Err() != nil || !s.runner.executionLeaseTermCurrent(term) {
		return
	}
	if work.Connection != nil {
		result.Capabilities = sanitizeDynatraceCapabilitiesForConfig(result.Capabilities, work.Connection.Config)
	} else {
		result.Capabilities = dynatrace.Capabilities{}
	}
	result.Items = sanitizeDynatraceEntities(result.Items)
	body, _ := json.Marshal(result)
	if len(body) > 1<<20 {
		body = []byte(`{}`)
		message = "Dynatrace operation result exceeds limits"
	}
	leaseName, leaseOwner, generation := "", "", uint64(0)
	if s.runner.durableExecutionFence {
		leaseName, leaseOwner, generation = executionLeaseName, term.ownerID, term.generation
	}
	committed := false
	if fenced, ok := store.(interface {
		CompleteDynatraceOperationFenced(string, string, string, string, time.Time, string, string, uint64) (bool, error)
	}); ok {
		committed, err = fenced.CompleteDynatraceOperationFenced(op.ID, owner, string(body), message, time.Now().UTC(), leaseName, leaseOwner, generation)
	} else if !s.runner.durableExecutionFence {
		committed, err = store.CompleteDynatraceOperation(op.ID, owner, string(body), message, time.Now().UTC())
	}
	if err == nil && committed && op.Kind == "verify" && work.Selection == nil {
		if fenced, ok := s.store.(interface {
			UpdateDynatraceConnectionHealthIfRevisionFenced(string, string, int64, string, string, dynatrace.Capabilities, time.Time, string, string, uint64) (bool, error)
		}); ok {
			status := "connected"
			if message != "" {
				status = "error"
			}
			_, _ = fenced.UpdateDynatraceConnectionHealthIfRevisionFenced(op.ConnectionID, op.ProjectID, op.ConnectionRevision, status, message, result.Capabilities, time.Now().UTC(), leaseName, leaseOwner, generation)
		} else if !s.runner.durableExecutionFence {
			s.publishDynatraceOperationHealth(op, message, result.Capabilities)
		}
	}
}
func (s *Server) publishDynatraceOperationHealth(op *storage.DynatraceOperation, message string, caps dynatrace.Capabilities) {
	if store, ok := s.store.(interface {
		UpdateDynatraceConnectionHealthIfRevision(string, string, int64, string, string, dynatrace.Capabilities, time.Time) (bool, error)
	}); ok {
		status := "connected"
		if message != "" {
			status = "error"
		}
		_, _ = store.UpdateDynatraceConnectionHealthIfRevision(op.ConnectionID, op.ProjectID, op.ConnectionRevision, status, message, caps, time.Now().UTC())
	}
}
func sanitizeDynatraceCapabilities(c dynatrace.Capabilities) dynatrace.Capabilities {
	c.EnvironmentID, c.PlatformEnvironmentID, c.TenantID = "", "", ""
	c.EnvironmentOrigin, c.PlatformOrigin, c.VerifiedOrigins = "", "", nil
	statuses := map[string]string{}
	for _, key := range []string{"environment", "metrics", "metric_discovery", "entities", "problems", "events", "platform", "grail_metrics", "logs", "grail_events", "spans", "smartscape", "classic_topology"} {
		switch c.Status[key] {
		case "available", "available_no_data", "empty", "not_verified", "not_configured", "not_selected", "unauthorized", "forbidden", "unsupported", "rate_limited", "budget_exhausted", "unlicensed", "unreachable", "error", "authentication_denied", "permission_denied", "throttled", "query_rejected", "query_budget", "upstream_error", "query_expired", "pending", "partial":
			statuses[key] = c.Status[key]
		}
	}
	for key, value := range c.Status {
		if (strings.HasPrefix(key, "metrics:") || strings.HasPrefix(key, "grail:")) && len(key) <= 256 {
			switch value {
			case "available", "available_no_data", "not_verified", "not_selected", "unauthorized", "forbidden", "unsupported", "budget_exhausted", "rate_limited", "unlicensed", "unreachable", "error", "pending", "partial", "authentication_denied", "permission_denied", "throttled", "query_rejected", "query_budget", "upstream_error", "query_expired":
				statuses[key] = value
			}
		}
	}
	c.Status = statuses
	return c
}

func sanitizeDynatraceCapabilitiesForConfig(raw dynatrace.Capabilities, cfg dynatrace.Config) dynatrace.Capabilities {
	c := sanitizeDynatraceCapabilities(raw)
	identity := func(origin, suffix string) string {
		u, err := url.Parse(origin)
		if err != nil || !strings.HasSuffix(u.Hostname(), suffix) {
			return ""
		}
		return strings.TrimSuffix(u.Hostname(), suffix)
	}
	envID, platformID := identity(cfg.EnvironmentURL, ".live.dynatrace.com"), identity(cfg.PlatformURL, ".apps.dynatrace.com")
	if envID != "" && raw.EnvironmentID == envID && raw.EnvironmentOrigin == cfg.EnvironmentURL {
		c.EnvironmentID, c.EnvironmentOrigin, c.TenantID = envID, cfg.EnvironmentURL, envID
		c.VerifiedOrigins = append(c.VerifiedOrigins, cfg.EnvironmentURL)
	} else {
		c.Metrics, c.Problems, c.Events, c.ClassicTopology = false, false, false, false
	}
	if platformID != "" && raw.PlatformEnvironmentID == platformID && raw.PlatformOrigin == cfg.PlatformURL {
		c.PlatformEnvironmentID, c.PlatformOrigin, c.TenantID = platformID, cfg.PlatformURL, platformID
		c.VerifiedOrigins = append(c.VerifiedOrigins, cfg.PlatformURL)
	} else {
		c.Logs, c.Spans, c.GrailMetrics, c.GrailEvents, c.Smartscape = false, false, false, false, false
	}
	return c
}

func dynatraceWantedCapabilities(c *storage.DynatraceConnection) []string {
	out := []string{}
	for _, item := range []struct {
		selected bool
		name     string
	}{{c.MetricsEnabled, "metrics"}, {c.ProblemsEnabled, "problems"}, {c.EventsEnabled, "events"}, {c.LogsEnabled, "logs"}, {c.SpansEnabled, "spans"}, {c.GrailMetricsEnabled, "grail_metrics"}, {c.GrailEventsEnabled, "grail_events"}, {c.TopologyMode == "classic", "classic_topology"}, {c.TopologyMode == "smartscape", "smartscape"}} {
		if item.selected {
			out = append(out, item.name)
		}
	}
	return out
}
func dynatraceWantedCapabilitiesAvailable(wanted []string, caps dynatrace.Capabilities) bool {
	if len(wanted) == 0 {
		return false
	}
	for _, kind := range wanted {
		available := false
		switch kind {
		case "metrics":
			available = caps.Metrics
		case "problems":
			available = caps.Problems
		case "events":
			available = caps.Events
		case "logs":
			available = caps.Logs
		case "spans":
			available = caps.Spans
		case "grail_metrics":
			available = caps.GrailMetrics
		case "grail_events":
			available = caps.GrailEvents
		case "classic_topology":
			available = caps.ClassicTopology
		case "smartscape":
			available = caps.Smartscape
		}
		if !available {
			return false
		}
	}
	return true
}
