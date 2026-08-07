package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// Superuser surface for choosing which cloud a domain's fleet pools deploy
// into: Kubernetes, Azure AKS, or AWS EKS.
//
// These routes live under /api/v1/system/ rather than /api/v1/domains/{id}/...
// for two concrete reasons. access_policy.go carries a prefix catch-all for
// /api/v1/domains/ whose GET branch grants viewer/editor/project_admin, and a
// deployment target names infrastructure that only a system administrator has
// any reason to read. And serviceAccountScopeRule supports only exact and
// prefix matches, so a settings:manage rule under the domains prefix would
// wrongly tighten the application-repository routes alongside it.

// DomainDeploymentTargetRequest is the write shape.
type DomainDeploymentTargetRequest struct {
	Provider         string `json:"provider"`
	DefaultClusterID string `json:"default_cluster_id"`
	// UpdatedAt is the optimistic-concurrency token, echoed from a prior read.
	// It must be zero (or omitted) when configuring a domain for the first
	// time, so a stale client cannot clobber a target someone else just made.
	UpdatedAt time.Time `json:"updated_at"`
}

// DomainDeploymentTargetResponse is the read shape. An unconfigured domain
// returns 200 with Configured=false rather than 404: "no constraint" is a
// normal state, and a 404 would make the dashboard render it as an error.
type DomainDeploymentTargetResponse struct {
	DomainID           string    `json:"domain_id"`
	DomainName         string    `json:"domain_name,omitempty"`
	Configured         bool      `json:"configured"`
	Provider           string    `json:"provider,omitempty"`
	DefaultClusterID   string    `json:"default_cluster_id,omitempty"`
	DefaultClusterName string    `json:"default_cluster_name,omitempty"`
	ScopedPoolCount    int       `json:"scoped_pool_count"`
	UpdatedBySubject   string    `json:"updated_by_subject,omitempty"`
	CreatedAt          time.Time `json:"created_at,omitempty"`
	UpdatedAt          time.Time `json:"updated_at,omitempty"`
}

// DomainDeploymentTargetListResponse is the collection shape, plus the
// vocabulary the UI renders as choices so it never hardcodes it.
type DomainDeploymentTargetListResponse struct {
	Targets             []DomainDeploymentTargetResponse `json:"targets"`
	Total               int                              `json:"total"`
	SelectableProviders []string                         `json:"selectable_providers"`
}

// ConflictingFleetPool names a pool that a proposed target would invalidate.
type ConflictingFleetPool struct {
	PoolID      string `json:"pool_id"`
	PoolName    string `json:"pool_name"`
	ClusterID   string `json:"cluster_id,omitempty"`
	ClusterName string `json:"cluster_name,omitempty"`
	Provider    string `json:"provider,omitempty"`
	Reason      string `json:"reason"`
}

// DomainDeploymentTargetConflictResponse is the 409 body on a retarget.
type DomainDeploymentTargetConflictResponse struct {
	Provider         string                 `json:"provider"`
	ConflictingPools []ConflictingFleetPool `json:"conflicting_pools"`
}

// requireDomainTargetStore returns the store or writes the error response.
func (s *Server) requireDomainTargetStore(w http.ResponseWriter) (storage.DomainDeploymentTargetStore, bool) {
	targets := s.domainTargetStore()
	if targets == nil {
		writeError(w, http.StatusServiceUnavailable,
			"this controller's storage backend does not support domain deployment targets")
		return nil, false
	}
	return targets, true
}

func (s *Server) domainDeploymentTargetResponse(targets storage.DomainDeploymentTargetStore, domainID string, target *storage.DomainDeploymentTarget) DomainDeploymentTargetResponse {
	response := DomainDeploymentTargetResponse{DomainID: domainID}
	if domain, err := s.store.GetDomain(domainID); err == nil && domain != nil {
		response.DomainName = domain.Name
	}
	if pools, err := targets.ListFleetPoolsForDomain(domainID); err == nil {
		response.ScopedPoolCount = len(pools)
	}
	if target == nil {
		return response
	}
	response.Configured = true
	response.Provider = target.Provider
	response.DefaultClusterID = target.DefaultClusterID
	response.UpdatedBySubject = target.UpdatedBySubject
	response.CreatedAt = target.CreatedAt
	response.UpdatedAt = target.UpdatedAt
	if clusters, ok := s.store.(storage.ClusterConnectionStore); ok && target.DefaultClusterID != "" {
		if conn, err := clusters.GetClusterConnection(target.DefaultClusterID); err == nil && conn != nil {
			response.DefaultClusterName = conn.Name
		}
	}
	return response
}

func (s *Server) handleListDomainDeploymentTargets(w http.ResponseWriter, r *http.Request) {
	targets, ok := s.requireDomainTargetStore(w)
	if !ok {
		return
	}
	stored, err := targets.ListDomainDeploymentTargets()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list domain deployment targets")
		return
	}
	response := DomainDeploymentTargetListResponse{
		Targets:             make([]DomainDeploymentTargetResponse, 0, len(stored)),
		SelectableProviders: storage.SelectableDeploymentProviders(),
	}
	for _, target := range stored {
		response.Targets = append(response.Targets, s.domainDeploymentTargetResponse(targets, target.DomainID, target))
	}
	response.Total = len(response.Targets)
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleGetDomainDeploymentTarget(w http.ResponseWriter, r *http.Request) {
	targets, ok := s.requireDomainTargetStore(w)
	if !ok {
		return
	}
	domainID := normalizeDomainID(r.PathValue("id"))
	if domain, err := s.store.GetDomain(domainID); err != nil || domain == nil {
		writeError(w, http.StatusNotFound, "domain not found")
		return
	}
	target, err := targets.GetDomainDeploymentTarget(domainID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load the domain deployment target")
		return
	}
	writeJSON(w, http.StatusOK, s.domainDeploymentTargetResponse(targets, domainID, target))
}

func (s *Server) handleUpdateDomainDeploymentTarget(w http.ResponseWriter, r *http.Request) {
	targets, ok := s.requireDomainTargetStore(w)
	if !ok {
		return
	}
	domainID := normalizeDomainID(r.PathValue("id"))
	if domain, err := s.store.GetDomain(domainID); err != nil || domain == nil {
		writeError(w, http.StatusNotFound, "domain not found")
		return
	}
	var req DomainDeploymentTargetRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	provider := strings.TrimSpace(req.Provider)
	if !storage.IsSelectableDeploymentProvider(provider) {
		writeError(w, http.StatusBadRequest,
			"provider must be one of "+strings.Join(storage.SelectableDeploymentProviders(), ", "))
		return
	}

	clusterID := strings.TrimSpace(req.DefaultClusterID)
	if clusterID != "" {
		clusters, supported := s.store.(storage.ClusterConnectionStore)
		if !supported {
			writeError(w, http.StatusServiceUnavailable, "this controller's storage backend does not support cluster connections")
			return
		}
		conn, err := clusters.GetClusterConnection(clusterID)
		if err != nil || conn == nil {
			writeError(w, http.StatusBadRequest, "the default cluster connection could not be loaded")
			return
		}
		// A default cluster that contradicts the provider would hand every new
		// pool an immediately-invalid binding.
		if connProvider, known := storage.ProviderForClusterKind(conn.Kind); !known || connProvider != provider {
			writeError(w, http.StatusBadRequest,
				"the default cluster is "+firstNonEmpty(connProvider, conn.Kind)+", which does not match the selected provider "+provider)
			return
		}
	}

	// Refuse a retarget that would strand existing pools. Allowing it would
	// leave every conflicting pool failing closed at its next scale, which the
	// administrator would discover during an incident rather than now, while
	// the pool list is still in front of them.
	conflicts, err := s.conflictingPoolsForProvider(domainID, provider)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not check the domain's fleet pools")
		return
	}
	if len(conflicts) > 0 {
		writeJSONError(w, http.StatusConflict,
			"fleet pools scoped to this domain are bound to clusters of a different provider; rebind or remove them first",
			DomainDeploymentTargetConflictResponse{Provider: provider, ConflictingPools: conflicts})
		return
	}

	_, subject, _ := s.resolveAuditActor(r)
	target := &storage.DomainDeploymentTarget{
		DomainID:         domainID,
		Provider:         provider,
		DefaultClusterID: clusterID,
		UpdatedBySubject: subject,
	}
	saved, err := targets.UpsertDomainDeploymentTargetIfUnchanged(target, req.UpdatedAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !saved {
		writeError(w, http.StatusConflict,
			"the domain deployment target changed while the update was in flight; reload and retry")
		return
	}
	s.recordAuditEvent(r, "domain.deployment_target.update", "success", "domain", domainID, "", map[string]any{
		"provider": provider, "default_cluster_id": clusterID,
	})
	writeJSON(w, http.StatusOK, s.domainDeploymentTargetResponse(targets, domainID, target))
}

func (s *Server) handleDeleteDomainDeploymentTarget(w http.ResponseWriter, r *http.Request) {
	targets, ok := s.requireDomainTargetStore(w)
	if !ok {
		return
	}
	domainID := normalizeDomainID(r.PathValue("id"))
	// Deleting is allowed unconditionally, unlike a retarget. Removing a
	// constraint can only widen what a pool may do, so it can never leave an
	// existing pool in a conflicting state.
	if err := targets.DeleteDomainDeploymentTarget(domainID); err != nil {
		writeError(w, http.StatusInternalServerError, "could not clear the domain deployment target")
		return
	}
	s.recordAuditEvent(r, "domain.deployment_target.delete", "success", "domain", domainID, "", nil)
	writeJSON(w, http.StatusOK, s.domainDeploymentTargetResponse(targets, domainID, nil))
}
