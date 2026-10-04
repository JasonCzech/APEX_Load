package api

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/appdynamics"
	"github.com/jasonczech/apexload/pkg/collectoragent"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

type AppDynamicsConnectionRequest struct {
	Name               string             `json:"name"`
	Description        string             `json:"description"`
	Enabled            bool               `json:"enabled"`
	CollectionMode     string             `json:"collection_mode"`
	CollectorAgentID   string             `json:"collector_agent_id"`
	Config             appdynamics.Config `json:"config"`
	Revision           int64              `json:"revision"`
	ClientIDSecret     *string            `json:"client_id_secret,omitempty"`
	ClientSecretSecret *string            `json:"client_secret_secret,omitempty"`
	UsernameSecret     *string            `json:"username_secret,omitempty"`
	PasswordSecret     *string            `json:"password_secret,omitempty"`
	EventsAPIKeySecret *string            `json:"events_api_key_secret,omitempty"`
}
type AppDynamicsConnectionResponse struct {
	*storage.AppDynamicsConnection
	Status          string `json:"status"`
	HasClientID     bool   `json:"has_client_id"`
	HasClientSecret bool   `json:"has_client_secret"`
	HasUsername     bool   `json:"has_username"`
	HasPassword     bool   `json:"has_password"`
	HasEventsAPIKey bool   `json:"has_events_api_key"`
	HasCustomCA     bool   `json:"has_custom_ca"`
}

func decodeAppDynamicsJSON(w http.ResponseWriter, r *http.Request, value any) bool {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	err := decoder.Decode(value)
	if err == nil {
		var trailing any
		if decoder.Decode(&trailing) != io.EOF {
			err = errors.New("trailing JSON")
		}
	}
	if err == nil {
		return true
	}
	var bound *http.MaxBytesError
	if errors.As(err, &bound) {
		writeError(w, 413, "AppDynamics request exceeds the configured size limit")
	} else {
		writeError(w, 400, "invalid AppDynamics request")
	}
	return false
}

func appDynamicsConnectionResponse(c *storage.AppDynamicsConnection) AppDynamicsConnectionResponse {
	public := *c
	public.Config.CAPEM, public.Config.ApprovedCIDRs = "", nil
	return AppDynamicsConnectionResponse{AppDynamicsConnection: &public, Status: c.ConfigurationStatus(), HasClientID: c.ClientIDSecretName != "", HasClientSecret: c.ClientSecretSecretName != "", HasUsername: c.UsernameSecretName != "", HasPassword: c.PasswordSecretName != "", HasEventsAPIKey: c.EventsAPIKeySecretName != "", HasCustomCA: c.Config.CAPEM != ""}
}
func applyAppDynamicsConnection(c *storage.AppDynamicsConnection, r AppDynamicsConnectionRequest) {
	previous := c.Config.Defaults()
	next := r.Config.Defaults()
	sameIdentity := previous.Endpoint == next.Endpoint && previous.ContextPath == next.ContextPath && previous.AccountName == next.AccountName && previous.AuthType == next.AuthType
	sameEventsIdentity := previous.EventsEndpoint == next.EventsEndpoint && previous.EventsAccountName == next.EventsAccountName
	if previous.Endpoint == next.Endpoint {
		if r.Config.CAPEM == "" {
			r.Config.CAPEM = c.Config.CAPEM
		}
		if r.Config.ApprovedCIDRs == nil {
			r.Config.ApprovedCIDRs = append([]string(nil), c.Config.ApprovedCIDRs...)
		}
	}
	if !sameIdentity {
		c.ClientIDSecretName, c.ClientSecretSecretName, c.UsernameSecretName, c.PasswordSecretName = "", "", "", ""
	}
	if !sameEventsIdentity {
		c.EventsAPIKeySecretName = ""
	}
	if r.ClientIDSecret != nil && strings.TrimSpace(*r.ClientIDSecret) != "" && strings.TrimSpace(*r.ClientIDSecret) != c.ClientIDSecretName {
		c.ClientSecretSecretName = ""
	}
	if r.UsernameSecret != nil && strings.TrimSpace(*r.UsernameSecret) != "" && strings.TrimSpace(*r.UsernameSecret) != c.UsernameSecretName {
		c.PasswordSecretName = ""
	}
	c.Name, c.Description, c.Enabled = r.Name, r.Description, r.Enabled
	c.Config = r.Config
	c.CollectionMode, c.CollectorAgentID = r.CollectionMode, r.CollectorAgentID
	for _, v := range []struct {
		in  *string
		out *string
	}{{r.ClientIDSecret, &c.ClientIDSecretName}, {r.ClientSecretSecret, &c.ClientSecretSecretName}, {r.UsernameSecret, &c.UsernameSecretName}, {r.PasswordSecret, &c.PasswordSecretName}, {r.EventsAPIKeySecret, &c.EventsAPIKeySecretName}} {
		if v.in != nil && strings.TrimSpace(*v.in) != "" {
			*v.out = strings.TrimSpace(*v.in)
		}
	}
}
func (s *Server) appDynamicsTrustAllowed(r *http.Request, req AppDynamicsConnectionRequest, existing *storage.AppDynamicsConnection) bool {
	effective := &storage.AppDynamicsConnection{}
	if existing != nil {
		*effective = *existing
	}
	applyAppDynamicsConnection(effective, req)
	if len(effective.Config.ApprovedCIDRs) == 0 || existing != nil && reflect.DeepEqual(effective.Config.ApprovedCIDRs, existing.Config.ApprovedCIDRs) && effective.Config.Endpoint == existing.Config.Endpoint && effective.Config.EventsEndpoint == existing.Config.EventsEndpoint {
		return true
	}
	user, _ := AuthenticatedUserFromContext(r.Context())
	return s.authSettingsForRequest().Mode == authModeDev || user != nil && user.hasGlobalRole(RoleSystemAdmin)
}
func appDynamicsVerificationIdentity(c *storage.AppDynamicsConnection) any {
	return struct {
		Config                                                                 appdynamics.Config
		Mode, Collector, ClientID, ClientSecret, Username, Password, EventsKey string
	}{c.Config.Defaults(), c.CollectionMode, c.CollectorAgentID, c.ClientIDSecretName, c.ClientSecretSecretName, c.UsernameSecretName, c.PasswordSecretName, c.EventsAPIKeySecretName}
}
func (s *Server) appDynamicsConnectionStore(w http.ResponseWriter) storage.AppDynamicsConnectionStore {
	store, ok := s.store.(storage.AppDynamicsConnectionStore)
	if !ok {
		writeError(w, 503, "AppDynamics connection storage unavailable")
		return nil
	}
	if err := store.EnsureAppDynamicsSchema(); err != nil {
		writeError(w, 503, "AppDynamics connection storage unavailable")
		return nil
	}
	return store
}
func (s *Server) loadAppDynamicsConnection(w http.ResponseWriter, r *http.Request) (*storage.AppDynamicsConnection, storage.AppDynamicsConnectionStore) {
	store := s.appDynamicsConnectionStore(w)
	if store == nil {
		return nil, nil
	}
	c, err := store.GetAppDynamicsConnection(r.PathValue("connection_id"))
	if errors.Is(err, sql.ErrNoRows) || err == nil && (c == nil || c.ProjectID != r.PathValue("id")) {
		writeError(w, 404, "AppDynamics connection not found")
		return nil, nil
	}
	if err != nil {
		writeError(w, 500, "AppDynamics connection unavailable")
		return nil, nil
	}
	return c, store
}
func (s *Server) handleCreateAppDynamicsConnection(w http.ResponseWriter, r *http.Request) {
	release, err := s.acquireSecretReferenceMutation(r.PathValue("id"))
	if err != nil {
		writeError(w, 503, "project credential references are temporarily unavailable")
		return
	}
	defer release()
	store := s.appDynamicsConnectionStore(w)
	if store == nil {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 96<<10)
	var req AppDynamicsConnectionRequest
	if !decodeAppDynamicsJSON(w, r, &req) {
		return
	}
	if req.Enabled {
		writeError(w, 409, "save the connection disabled and verify it before enabling")
		return
	}
	if !s.appDynamicsTrustAllowed(r, req, nil) {
		writeError(w, 403, "system administrator approval is required for private AppDynamics egress")
		return
	}
	c := &storage.AppDynamicsConnection{ID: uuid.NewString(), ProjectID: r.PathValue("id")}
	applyAppDynamicsConnection(c, req)
	if err := validateAppDynamicsSecretReferences(c); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if err := c.Validate(); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if err := store.CreateAppDynamicsConnection(c); err != nil {
		writeError(w, 409, "cannot create AppDynamics connection")
		return
	}
	s.recordAuditEvent(r, "monitoring.appdynamics.create", "success", "appdynamics_connection", c.ID, c.ProjectID, map[string]any{"revision": c.Revision})
	writeJSON(w, 201, appDynamicsConnectionResponse(c))
}
func (s *Server) handleListAppDynamicsConnections(w http.ResponseWriter, r *http.Request) {
	store := s.appDynamicsConnectionStore(w)
	if store == nil {
		return
	}
	project := r.PathValue("id")
	items, err := store.ListAppDynamicsConnections(project)
	if err != nil {
		writeError(w, 500, "cannot list AppDynamics connections")
		return
	}
	user, _ := AuthenticatedUserFromContext(r.Context())
	admin := s.authSettingsForRequest().Mode == authModeDev || user != nil && (user.hasGlobalRole(RoleSystemAdmin, RoleProjectAdmin) || userHasProjectOrParentRole(s.store, user, project, RoleProjectAdmin))
	if admin {
		out := []AppDynamicsConnectionResponse{}
		for _, c := range items {
			out = append(out, appDynamicsConnectionResponse(c))
		}
		writeJSON(w, 200, map[string]any{"connections": out, "count": len(out)})
		return
	}
	out := []map[string]any{}
	for _, c := range items {
		out = append(out, map[string]any{"id": c.ID, "project_id": c.ProjectID, "name": c.Name, "status": c.ConfigurationStatus(), "revision": c.Revision, "enabled": c.Enabled, "collection_mode": c.CollectionMode, "collector_agent_id": c.CollectorAgentID, "capabilities": c.Capabilities})
	}
	writeJSON(w, 200, map[string]any{"connections": out, "count": len(out)})
}
func (s *Server) handleGetAppDynamicsConnection(w http.ResponseWriter, r *http.Request) {
	if c, _ := s.loadAppDynamicsConnection(w, r); c != nil {
		writeJSON(w, 200, appDynamicsConnectionResponse(c))
	}
}
func (s *Server) handleUpdateAppDynamicsConnection(w http.ResponseWriter, r *http.Request) {
	release, err := s.acquireSecretReferenceMutation(r.PathValue("id"))
	if err != nil {
		writeError(w, 503, "project credential references are temporarily unavailable")
		return
	}
	defer release()
	c, store := s.loadAppDynamicsConnection(w, r)
	if c == nil {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 96<<10)
	var req AppDynamicsConnectionRequest
	if !decodeAppDynamicsJSON(w, r, &req) {
		return
	}
	if req.Revision < 1 {
		writeError(w, 400, "revision is required")
		return
	}
	if !s.appDynamicsTrustAllowed(r, req, c) {
		writeError(w, 403, "system administrator approval is required for private AppDynamics egress")
		return
	}
	previousIdentity := appDynamicsVerificationIdentity(c)
	previousVerified := c.Revision == c.VerifiedRevision && c.LastCheckStatus == "connected"
	applyAppDynamicsConnection(c, req)
	if err := validateAppDynamicsSecretReferences(c); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if err := c.Validate(); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if c.Enabled && (!previousVerified || !reflect.DeepEqual(previousIdentity, appDynamicsVerificationIdentity(c))) {
		writeError(w, 409, "save configuration disabled and verify the selected execution location before enabling")
		return
	}
	updated, err := store.UpdateAppDynamicsConnectionIfRevision(c, req.Revision)
	if err != nil || !updated {
		writeError(w, 409, "AppDynamics connection changed; reload and retry")
		return
	}
	s.recordAuditEvent(r, "monitoring.appdynamics.update", "success", "appdynamics_connection", c.ID, c.ProjectID, map[string]any{"revision": c.Revision})
	writeJSON(w, 200, appDynamicsConnectionResponse(c))
}
func (s *Server) handleDeleteAppDynamicsConnection(w http.ResponseWriter, r *http.Request) {
	release, err := s.acquireSecretReferenceMutation(r.PathValue("id"))
	if err != nil {
		writeError(w, 503, "project credential references are temporarily unavailable")
		return
	}
	defer release()
	c, store := s.loadAppDynamicsConnection(w, r)
	if c == nil {
		return
	}
	revision, err := strconv.ParseInt(r.URL.Query().Get("revision"), 10, 64)
	if err != nil || revision < 1 {
		writeError(w, 400, "revision is required")
		return
	}
	deleted, err := store.DeleteAppDynamicsConnectionIfRevision(c.ID, c.ProjectID, revision)
	if err != nil {
		writeError(w, 409, "cannot delete AppDynamics connection")
		return
	}
	if !deleted {
		writeError(w, 409, "AppDynamics connection changed; reload and retry")
		return
	}
	s.recordAuditEvent(r, "monitoring.appdynamics.delete", "success", "appdynamics_connection", c.ID, c.ProjectID, nil)
	w.WriteHeader(204)
}

func resolveAppDynamicsCredentials(ctx context.Context, resolver engine.SecretResolver, c *storage.AppDynamicsConnectionSnapshot) (appdynamics.Credentials, error) {
	var out appdynamics.Credentials
	if c == nil {
		return out, errors.New("AppDynamics connection snapshot unavailable")
	}
	for _, v := range []struct {
		name  string
		value *string
	}{{c.ClientIDSecretName, &out.ClientID}, {c.ClientSecretSecretName, &out.ClientSecret}, {c.UsernameSecretName, &out.Username}, {c.PasswordSecretName, &out.Password}, {c.EventsAPIKeySecretName, &out.EventsAPIKey}} {
		if v.name == "" {
			continue
		}
		value, found := engine.ResolveSecret(ctx, resolver, v.name)
		if !found {
			return appdynamics.Credentials{}, errors.New("AppDynamics project credential unavailable")
		}
		*v.value = value
	}
	return out, nil
}

// populateAppDynamicsGrant resolves only the immutable job snapshot; profile
// updates cannot redirect an already running collector's credentials.
func (s *Server) populateAppDynamicsGrant(ctx context.Context, grant *collectoragent.RunJob, job storage.RunMonitorJob) error {
	if job.Source.Pack != "appdynamics" {
		return nil
	}
	if job.ConnectionSnapshot == nil || job.Source.AppDynamics == nil {
		return errors.New("AppDynamics job snapshot unavailable")
	}
	snapshot := job.ConnectionSnapshot
	if snapshot.ProjectID != job.ProjectID {
		return errors.New("AppDynamics snapshot project mismatch")
	}
	grant.AppDynamics = &appdynamics.JobConfig{Config: snapshot.Config, Selection: *job.Source.AppDynamics}
	grant.AppDynamics.Selection.ProjectID = job.ProjectID
	grant.AppDynamics.Selection.QueryOffset = job.QueryOffset
	grant.QueryFromUTC, grant.QueryUntilUTC = appDynamicsQueryWindow(job, time.Now().UTC())
	grant.LoadEndedAt, grant.DrainDeadline, grant.QueryWatermark = job.LoadEndedAt, job.DrainDeadline, job.QueryWatermark
	grant.Reconciling = job.State == "reconciling"
	grant.MaxBatches = 32
	grant.Destination = snapshot.Config.Endpoint
	credentials, err := resolveAppDynamicsCredentials(ctx, s.runner.secretResolverForProject(job.ProjectID), snapshot)
	if err != nil {
		return err
	}
	grant.AppDynamics.Config, err = s.appDynamicsQuotaConfig(ctx, snapshot, credentials, "collector:"+job.Source.CollectorID)
	if err != nil {
		return err
	}
	publicKey, err := base64.StdEncoding.DecodeString(job.Source.CollectorPublicKey)
	if err != nil {
		return errors.New("collector encryption identity unavailable")
	}
	grant.Credentials, err = collectoragent.SealCredentials(*grant, publicKey, collectoragent.AppDynamicsCredentialBundle(credentials))
	return err
}
func appDynamicsCollectorAllowed(c *storage.MonitoringCollector, project string) bool {
	return c != nil && c.ProjectID == project && slices.Contains(c.AllowedMetricPacks, "appdynamics")
}
