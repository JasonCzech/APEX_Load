package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/prometheus"
	"github.com/jasonczech/apexload/pkg/secretstore"
	"github.com/jasonczech/apexload/pkg/storage"
)

type PrometheusConnectionRequest struct {
	Name                    string            `json:"name"`
	Description             string            `json:"description"`
	Enabled                 bool              `json:"enabled"`
	CollectionMode          string            `json:"collection_mode"`
	CollectorAgentID        string            `json:"collector_agent_id"`
	Config                  prometheus.Config `json:"config"`
	Revision                int64             `json:"revision"`
	TokenSecret             *string           `json:"token_secret,omitempty"`
	ClientSecretSecret      *string           `json:"client_secret_secret,omitempty"`
	UsernameSecret          *string           `json:"username_secret,omitempty"`
	PasswordSecret          *string           `json:"password_secret,omitempty"`
	ClientCertificateSecret *string           `json:"client_certificate_secret,omitempty"`
	ClientKeySecret         *string           `json:"client_key_secret,omitempty"`
}
type PrometheusConnectionResponse struct {
	*storage.PrometheusConnection
	Status               string `json:"status"`
	HasToken             bool   `json:"has_token"`
	HasClientSecret      bool   `json:"has_client_secret"`
	HasUsername          bool   `json:"has_username"`
	HasPassword          bool   `json:"has_password"`
	HasClientCertificate bool   `json:"has_client_certificate"`
	HasClientKey         bool   `json:"has_client_key"`
	HasCustomCA          bool   `json:"has_custom_ca"`
}

func decodePrometheusJSON(w http.ResponseWriter, r *http.Request, value any) bool {
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
		writeError(w, 413, "Prometheus request exceeds the configured size limit")
	} else {
		writeError(w, 400, "invalid Prometheus request")
	}
	return false
}

func prometheusConnectionResponse(c *storage.PrometheusConnection) PrometheusConnectionResponse {
	public := *c
	public.Config.CAPEM, public.Config.ApprovedCIDRs = "", nil
	return PrometheusConnectionResponse{PrometheusConnection: &public, Status: c.ConfigurationStatus(), HasToken: c.TokenSecretName != "", HasClientSecret: c.ClientSecretSecretName != "", HasUsername: c.UsernameSecretName != "", HasPassword: c.PasswordSecretName != "", HasClientCertificate: c.ClientCertificateSecretName != "", HasCustomCA: c.Config.CAPEM != "", HasClientKey: c.ClientKeySecretName != ""}
}
func applyPrometheusConnection(c *storage.PrometheusConnection, r PrometheusConnectionRequest) {
	previous, next := c.Config.Defaults(), r.Config.Defaults()
	sameIdentity := previous.Endpoint == next.Endpoint && previous.ContextPath == next.ContextPath && previous.AuthType == next.AuthType && previous.BackendProfile == next.BackendProfile && previous.ClientID == next.ClientID && previous.AzureTenantID == next.AzureTenantID && previous.AzureCredentialMode == next.AzureCredentialMode && previous.MTLS == next.MTLS
	if previous.Endpoint == next.Endpoint {
		if r.Config.CAPEM == "" {
			r.Config.CAPEM = c.Config.CAPEM
		}
		if r.Config.ApprovedCIDRs == nil {
			r.Config.ApprovedCIDRs = append([]string(nil), c.Config.ApprovedCIDRs...)
		}
	}
	if !sameIdentity {
		c.TokenSecretName, c.ClientSecretSecretName, c.UsernameSecretName, c.PasswordSecretName, c.ClientCertificateSecretName, c.ClientKeySecretName = "", "", "", "", "", ""
	}
	if r.UsernameSecret != nil && strings.TrimSpace(*r.UsernameSecret) != "" && strings.TrimSpace(*r.UsernameSecret) != c.UsernameSecretName {
		c.PasswordSecretName = ""
	}
	if r.ClientCertificateSecret != nil && strings.TrimSpace(*r.ClientCertificateSecret) != "" && strings.TrimSpace(*r.ClientCertificateSecret) != c.ClientCertificateSecretName {
		c.ClientKeySecretName = ""
	}
	c.Name, c.Description, c.Enabled = r.Name, r.Description, r.Enabled
	c.Config = r.Config
	c.CollectionMode, c.CollectorAgentID = r.CollectionMode, r.CollectorAgentID
	for _, v := range []struct {
		in  *string
		out *string
	}{{r.TokenSecret, &c.TokenSecretName}, {r.ClientSecretSecret, &c.ClientSecretSecretName}, {r.UsernameSecret, &c.UsernameSecretName}, {r.PasswordSecret, &c.PasswordSecretName}, {r.ClientCertificateSecret, &c.ClientCertificateSecretName}, {r.ClientKeySecret, &c.ClientKeySecretName}} {
		if v.in != nil && strings.TrimSpace(*v.in) != "" {
			*v.out = strings.TrimSpace(*v.in)
		}
	}
}

func (s *Server) prometheusTrustAllowed(r *http.Request, req PrometheusConnectionRequest, existing *storage.PrometheusConnection) bool {
	effective := &storage.PrometheusConnection{}
	if existing != nil {
		*effective = *existing
	}
	applyPrometheusConnection(effective, req)
	if len(effective.Config.ApprovedCIDRs) == 0 && !effective.Config.AllowPrivateHTTP || existing != nil && reflect.DeepEqual(effective.Config.ApprovedCIDRs, existing.Config.ApprovedCIDRs) && effective.Config.Endpoint == existing.Config.Endpoint && effective.Config.AllowPrivateHTTP == existing.Config.AllowPrivateHTTP {
		return true
	}
	user, _ := AuthenticatedUserFromContext(r.Context())
	return s.authSettingsForRequest().Mode == authModeDev || user != nil && user.hasGlobalRole(RoleSystemAdmin)
}
func prometheusVerificationIdentity(c *storage.PrometheusConnection) any {
	return struct {
		Config                                                                                 prometheus.Config
		Mode, Collector, Token, ClientSecret, Username, Password, ClientCertificate, ClientKey string
	}{c.Config.Defaults(), c.CollectionMode, c.CollectorAgentID, c.TokenSecretName, c.ClientSecretSecretName, c.UsernameSecretName, c.PasswordSecretName, c.ClientCertificateSecretName, c.ClientKeySecretName}
}

func (s *Server) prometheusConnectionStore(w http.ResponseWriter) storage.PrometheusConnectionStore {
	store, ok := s.store.(storage.PrometheusConnectionStore)
	if !ok {
		writeError(w, 503, "Prometheus connection storage unavailable")
		return nil
	}
	if err := store.EnsurePrometheusSchema(); err != nil {
		writeError(w, 503, "Prometheus connection storage unavailable")
		return nil
	}
	return store
}
func (s *Server) loadPrometheusConnection(w http.ResponseWriter, r *http.Request) (*storage.PrometheusConnection, storage.PrometheusConnectionStore) {
	store := s.prometheusConnectionStore(w)
	if store == nil {
		return nil, nil
	}
	c, err := store.GetPrometheusConnection(r.PathValue("connection_id"))
	if errors.Is(err, sql.ErrNoRows) || err == nil && (c == nil || c.ProjectID != r.PathValue("id")) {
		writeError(w, 404, "Prometheus connection not found")
		return nil, nil
	}
	if err != nil {
		writeError(w, 500, "Prometheus connection unavailable")
		return nil, nil
	}
	return c, store
}
func (s *Server) handleCreatePrometheusConnection(w http.ResponseWriter, r *http.Request) {
	release, err := s.acquireSecretReferenceMutation(r.PathValue("id"))
	if err != nil {
		writeError(w, 503, "project credential references are temporarily unavailable")
		return
	}
	defer release()
	store := s.prometheusConnectionStore(w)
	if store == nil {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 96<<10)
	var req PrometheusConnectionRequest
	if !decodePrometheusJSON(w, r, &req) {
		return
	}
	if req.Enabled {
		writeError(w, 409, "save the connection disabled and verify it before enabling")
		return
	}
	if !s.prometheusTrustAllowed(r, req, nil) {
		writeError(w, 403, "system administrator approval is required for private Prometheus egress")
		return
	}
	c := &storage.PrometheusConnection{ID: uuid.NewString(), ProjectID: r.PathValue("id")}
	applyPrometheusConnection(c, req)
	if err := validatePrometheusSecretReferences(c); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if err := c.Validate(); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if err := store.CreatePrometheusConnection(c); err != nil {
		writeError(w, 409, "cannot create Prometheus connection")
		return
	}
	s.recordAuditEvent(r, "monitoring.prometheus.create", "success", "prometheus_connection", c.ID, c.ProjectID, map[string]any{"revision": c.Revision})
	writeJSON(w, 201, prometheusConnectionResponse(c))
}
func (s *Server) handleListPrometheusConnections(w http.ResponseWriter, r *http.Request) {
	store := s.prometheusConnectionStore(w)
	if store == nil {
		return
	}
	project := r.PathValue("id")
	items, err := store.ListPrometheusConnections(project)
	if err != nil {
		writeError(w, 500, "cannot list Prometheus connections")
		return
	}
	user, _ := AuthenticatedUserFromContext(r.Context())
	admin := s.authSettingsForRequest().Mode == authModeDev || user != nil && (user.hasGlobalRole(RoleSystemAdmin, RoleProjectAdmin) || userHasProjectOrParentRole(s.store, user, project, RoleProjectAdmin))
	if admin {
		out := []PrometheusConnectionResponse{}
		for _, c := range items {
			out = append(out, prometheusConnectionResponse(c))
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
func (s *Server) handleGetPrometheusConnection(w http.ResponseWriter, r *http.Request) {
	if c, _ := s.loadPrometheusConnection(w, r); c != nil {
		writeJSON(w, 200, prometheusConnectionResponse(c))
	}
}
func (s *Server) handleUpdatePrometheusConnection(w http.ResponseWriter, r *http.Request) {
	release, err := s.acquireSecretReferenceMutation(r.PathValue("id"))
	if err != nil {
		writeError(w, 503, "project credential references are temporarily unavailable")
		return
	}
	defer release()
	c, store := s.loadPrometheusConnection(w, r)
	if c == nil {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 96<<10)
	var req PrometheusConnectionRequest
	if !decodePrometheusJSON(w, r, &req) {
		return
	}
	if req.Revision < 1 {
		writeError(w, 400, "revision is required")
		return
	}
	if !s.prometheusTrustAllowed(r, req, c) {
		writeError(w, 403, "system administrator approval is required for private Prometheus egress")
		return
	}
	previousIdentity := prometheusVerificationIdentity(c)
	previousVerified := c.Revision == c.VerifiedRevision && c.LastCheckStatus == "connected"
	applyPrometheusConnection(c, req)
	if err := validatePrometheusSecretReferences(c); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if err := c.Validate(); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if c.Enabled && (!previousVerified || !reflect.DeepEqual(previousIdentity, prometheusVerificationIdentity(c))) {
		writeError(w, 409, "save configuration disabled and verify the selected execution location before enabling")
		return
	}
	updated, err := store.UpdatePrometheusConnectionIfRevision(c, req.Revision)
	if err != nil || !updated {
		writeError(w, 409, "Prometheus connection changed; reload and retry")
		return
	}
	s.recordAuditEvent(r, "monitoring.prometheus.update", "success", "prometheus_connection", c.ID, c.ProjectID, map[string]any{"revision": c.Revision})
	writeJSON(w, 200, prometheusConnectionResponse(c))
}
func (s *Server) handleDeletePrometheusConnection(w http.ResponseWriter, r *http.Request) {
	release, err := s.acquireSecretReferenceMutation(r.PathValue("id"))
	if err != nil {
		writeError(w, 503, "project credential references are temporarily unavailable")
		return
	}
	defer release()
	c, store := s.loadPrometheusConnection(w, r)
	if c == nil {
		return
	}
	revision, err := strconv.ParseInt(r.URL.Query().Get("revision"), 10, 64)
	if err != nil || revision < 1 {
		writeError(w, 400, "revision is required")
		return
	}
	deleted, err := store.DeletePrometheusConnectionIfRevision(c.ID, c.ProjectID, revision)
	if err != nil {
		writeError(w, 409, "cannot delete Prometheus connection")
		return
	}
	if !deleted {
		writeError(w, 409, "Prometheus connection changed; reload and retry")
		return
	}
	s.recordAuditEvent(r, "monitoring.prometheus.delete", "success", "prometheus_connection", c.ID, c.ProjectID, nil)
	w.WriteHeader(204)
}

// Monitoring credentials never use the engine's development placeholders or
// its diagnostic logs, which include logical secret-reference names.
func (tr *TestRunner) prometheusSecretResolverForProject(project string) engine.SecretResolver {
	if tr == nil {
		return engine.CacheSecretResolverForRun(engine.NewProjectSecretResolver(project))
	}
	return engine.CacheSecretResolverForRun(secretstore.EngineResolver(tr.projectSecretStore, project))
}

func resolvePrometheusCredentials(ctx context.Context, resolver engine.SecretResolver, c *storage.PrometheusConnectionSnapshot) (prometheus.Credentials, error) {
	var out prometheus.Credentials
	if c == nil {
		return out, errors.New("Prometheus connection snapshot unavailable")
	}
	for _, v := range []struct {
		name  string
		value *string
	}{{c.TokenSecretName, &out.Token}, {c.ClientSecretSecretName, &out.ClientSecret}, {c.UsernameSecretName, &out.Username}, {c.PasswordSecretName, &out.Password}, {c.ClientCertificateSecretName, &out.ClientCertificate}, {c.ClientKeySecretName, &out.ClientKey}} {
		if v.name == "" {
			continue
		}
		value, found := engine.ResolveSecret(ctx, resolver, v.name)
		if !found {
			return prometheus.Credentials{}, errors.New("Prometheus project credential unavailable")
		}
		*v.value = value
	}
	return out, nil
}

func validatePrometheusSecretReferences(connection *storage.PrometheusConnection) error {
	for _, name := range []string{connection.TokenSecretName, connection.ClientSecretSecretName, connection.UsernameSecretName, connection.PasswordSecretName, connection.ClientCertificateSecretName, connection.ClientKeySecretName} {
		if name != "" {
			if _, err := normalizeProjectSecretName(name); err != nil {
				return errors.New("Prometheus credentials require valid logical project secret names")
			}
		}
	}
	return nil
}
