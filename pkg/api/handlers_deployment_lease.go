package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	deploymentLeaseDefaultTTL          = 10 * time.Minute
	deploymentLeaseMinTTL              = time.Minute
	deploymentLeaseMaxTTL              = 60 * time.Minute
	deploymentLeaseMaxBody             = 32 << 10
	deploymentLeaseTokenHeader         = "X-APEXLoad-Deployment-Lease-Token"
	deploymentActiveTestsOverrideScope = "deployments:override-active-tests"
)

var (
	deploymentLeaseOwnerPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@-]{2,255}$`)
	deploymentLeaseTokenPattern = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
)

type deploymentLeaseRequest struct {
	OwnerID          string                         `json:"owner_id"`
	Token            string                         `json:"-"`
	TTLSeconds       int                            `json:"ttl_seconds,omitempty"`
	AllowActiveTests bool                           `json:"allow_active_tests,omitempty"`
	Context          storage.DeploymentLeaseContext `json:"context,omitempty"`
}

type deploymentLeaseResponse struct {
	Status      string                   `json:"status"`
	Lease       *storage.DeploymentLease `json:"lease,omitempty"`
	ActiveTests int                      `json:"active_tests,omitempty"`
	Error       string                   `json:"error,omitempty"`
}

func (s *Server) handleGetDeploymentLease(w http.ResponseWriter, _ *http.Request) {
	store, ok := s.store.(storage.DeploymentLeaseStore)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "deployment lease persistence is unavailable")
		return
	}
	lease, err := store.GetDeploymentLease(storage.DefaultDeploymentLeaseName)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "could not read deployment lease")
		return
	}
	writeJSON(w, http.StatusOK, deploymentLeaseResponse{Status: "ok", Lease: lease})
}

func (s *Server) handleAcquireDeploymentLease(w http.ResponseWriter, r *http.Request) {
	req, ttl, ok := decodeAndValidateDeploymentLeaseRequest(w, r, true)
	if !ok {
		return
	}
	if req.AllowActiveTests && strings.TrimSpace(req.Context.ApprovalID) == "" {
		writeError(w, http.StatusBadRequest, "allow_active_tests requires a reviewed approval_id")
		return
	}
	if req.AllowActiveTests {
		account, authenticated := AuthenticatedServiceAccountFromContext(r.Context())
		if !authenticated || !serviceAccountHasExplicitScope(account, deploymentActiveTestsOverrideScope) {
			s.recordAuditEvent(r, "system.deployment_lease.acquire", "denied", "deployment_lease", req.OwnerID, "", deploymentLeaseAuditDetails(req.OwnerID, req.Context, nil))
			writeError(w, http.StatusForbidden, "allow_active_tests requires a dedicated service account with the explicit deployments:override-active-tests scope")
			return
		}
	}
	store, supported := s.store.(storage.DeploymentLeaseStore)
	if !supported {
		writeError(w, http.StatusServiceUnavailable, "deployment lease persistence is unavailable")
		return
	}
	lease, activeTests, err := store.AcquireDeploymentLease(storage.DefaultDeploymentLeaseName, req.OwnerID, deploymentLeaseTokenHash(req.Token), ttl, req.Context, req.AllowActiveTests)
	details := deploymentLeaseAuditDetails(req.OwnerID, req.Context, lease)
	if activeTests > 0 {
		details["active_tests"] = activeTests
	}
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrDeploymentLeaseActiveTests):
			s.recordAuditEvent(r, "system.deployment_lease.acquire", "blocked", "deployment_lease", req.OwnerID, "", details)
			writeJSON(w, http.StatusConflict, deploymentLeaseResponse{Status: "blocked", ActiveTests: activeTests, Error: "active tests prevent deployment lease acquisition"})
		case errors.Is(err, storage.ErrDeploymentLeaseOwned):
			s.recordAuditEvent(r, "system.deployment_lease.acquire", "conflict", "deployment_lease", req.OwnerID, "", details)
			writeJSON(w, http.StatusConflict, deploymentLeaseResponse{Status: "conflict", Lease: lease, Error: "deployment lease is already held"})
		default:
			s.recordAuditEvent(r, "system.deployment_lease.acquire", "failure", "deployment_lease", req.OwnerID, "", details)
			writeError(w, http.StatusServiceUnavailable, "could not acquire deployment lease")
		}
		return
	}
	s.recordAuditEvent(r, "system.deployment_lease.acquire", "success", "deployment_lease", req.OwnerID, "", details)
	writeJSON(w, http.StatusOK, deploymentLeaseResponse{Status: "acquired", Lease: lease, ActiveTests: activeTests})
}

func (s *Server) handleRenewDeploymentLease(w http.ResponseWriter, r *http.Request) {
	req, ttl, ok := decodeAndValidateDeploymentLeaseRequest(w, r, false)
	if !ok {
		return
	}
	store, supported := s.store.(storage.DeploymentLeaseStore)
	if !supported {
		writeError(w, http.StatusServiceUnavailable, "deployment lease persistence is unavailable")
		return
	}
	lease, err := store.RenewDeploymentLease(storage.DefaultDeploymentLeaseName, req.OwnerID, deploymentLeaseTokenHash(req.Token), ttl)
	details := deploymentLeaseAuditDetails(req.OwnerID, storage.DeploymentLeaseContext{}, lease)
	if err != nil {
		status := http.StatusConflict
		message := "deployment lease is not held by this owner"
		if errors.Is(err, storage.ErrDeploymentLeaseExpired) {
			message = "deployment lease ownership has expired"
		} else if !errors.Is(err, storage.ErrDeploymentLeaseNotHeld) {
			status = http.StatusServiceUnavailable
			message = "could not renew deployment lease"
		}
		s.recordAuditEvent(r, "system.deployment_lease.renew", "failure", "deployment_lease", req.OwnerID, "", details)
		writeJSON(w, status, deploymentLeaseResponse{Status: "failed", Error: message})
		return
	}
	s.recordAuditEvent(r, "system.deployment_lease.renew", "success", "deployment_lease", req.OwnerID, "", details)
	writeJSON(w, http.StatusOK, deploymentLeaseResponse{Status: "renewed", Lease: lease})
}

func (s *Server) handleReleaseDeploymentLease(w http.ResponseWriter, r *http.Request) {
	req, _, ok := decodeAndValidateDeploymentLeaseRequest(w, r, false)
	if !ok {
		return
	}
	store, supported := s.store.(storage.DeploymentLeaseStore)
	if !supported {
		writeError(w, http.StatusServiceUnavailable, "deployment lease persistence is unavailable")
		return
	}
	lease, err := store.ReleaseDeploymentLease(storage.DefaultDeploymentLeaseName, req.OwnerID, deploymentLeaseTokenHash(req.Token))
	details := deploymentLeaseAuditDetails(req.OwnerID, storage.DeploymentLeaseContext{}, lease)
	if err != nil {
		status := http.StatusConflict
		message := "deployment lease is not held by this owner"
		if errors.Is(err, storage.ErrDeploymentLeaseExpired) {
			message = "deployment lease ownership has expired"
		} else if !errors.Is(err, storage.ErrDeploymentLeaseNotHeld) {
			status = http.StatusServiceUnavailable
			message = "could not release deployment lease"
		}
		s.recordAuditEvent(r, "system.deployment_lease.release", "failure", "deployment_lease", req.OwnerID, "", details)
		writeJSON(w, status, deploymentLeaseResponse{Status: "failed", Error: message})
		return
	}
	s.recordAuditEvent(r, "system.deployment_lease.release", "success", "deployment_lease", req.OwnerID, "", details)
	writeJSON(w, http.StatusOK, deploymentLeaseResponse{Status: "released", Lease: lease})
}

func decodeAndValidateDeploymentLeaseRequest(w http.ResponseWriter, r *http.Request, allowContext bool) (deploymentLeaseRequest, time.Duration, bool) {
	var req deploymentLeaseRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, deploymentLeaseMaxBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid deployment lease request")
		return req, 0, false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "deployment lease request must contain one JSON object")
		return req, 0, false
	}
	req.OwnerID = strings.TrimSpace(req.OwnerID)
	req.Token = strings.TrimSpace(r.Header.Get(deploymentLeaseTokenHeader))
	if !deploymentLeaseOwnerPattern.MatchString(req.OwnerID) {
		writeError(w, http.StatusBadRequest, "invalid deployment lease owner_id")
		return req, 0, false
	}
	if !deploymentLeaseTokenPattern.MatchString(req.Token) {
		writeError(w, http.StatusBadRequest, "invalid deployment lease token")
		return req, 0, false
	}
	minTTLSeconds := int(deploymentLeaseMinTTL / time.Second)
	maxTTLSeconds := int(deploymentLeaseMaxTTL / time.Second)
	if req.TTLSeconds != 0 && (req.TTLSeconds < minTTLSeconds || req.TTLSeconds > maxTTLSeconds) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("ttl_seconds must be between %d and %d", int(deploymentLeaseMinTTL.Seconds()), int(deploymentLeaseMaxTTL.Seconds())))
		return req, 0, false
	}
	ttl := deploymentLeaseDefaultTTL
	if req.TTLSeconds != 0 {
		ttl = time.Duration(req.TTLSeconds) * time.Second
	}
	if !allowContext && (req.AllowActiveTests || req.Context != (storage.DeploymentLeaseContext{})) {
		writeError(w, http.StatusBadRequest, "allow_active_tests and context are accepted only when acquiring a deployment lease")
		return req, 0, false
	}
	req.Context = normalizeDeploymentLeaseContext(req.Context)
	if err := validateDeploymentLeaseContext(req.Context); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return req, 0, false
	}
	return req, ttl, true
}

func normalizeDeploymentLeaseContext(context storage.DeploymentLeaseContext) storage.DeploymentLeaseContext {
	context.Release = strings.TrimSpace(context.Release)
	context.Environment = strings.TrimSpace(context.Environment)
	context.Actor = strings.TrimSpace(context.Actor)
	context.Repository = strings.TrimSpace(context.Repository)
	context.ServerURL = strings.TrimSpace(context.ServerURL)
	context.RunID = strings.TrimSpace(context.RunID)
	context.RunAttempt = strings.TrimSpace(context.RunAttempt)
	context.ApprovalID = strings.TrimSpace(context.ApprovalID)
	return context
}

func validateDeploymentLeaseContext(context storage.DeploymentLeaseContext) error {
	fields := map[string]struct {
		value string
		max   int
	}{
		"release": {context.Release, 128}, "environment": {context.Environment, 128}, "actor": {context.Actor, 256},
		"repository": {context.Repository, 256}, "server_url": {context.ServerURL, 512}, "run_id": {context.RunID, 64},
		"run_attempt": {context.RunAttempt, 32}, "approval_id": {context.ApprovalID, 128},
	}
	for name, field := range fields {
		if len(field.value) > field.max || strings.ContainsAny(field.value, "\r\n\x00") {
			return fmt.Errorf("invalid deployment lease context field %s", name)
		}
	}
	return nil
}

func deploymentLeaseTokenHash(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

func serviceAccountHasExplicitScope(account *AuthenticatedServiceAccount, required string) bool {
	if account == nil {
		return false
	}
	for _, scope := range normalizeAuthenticatedServiceAccountScopes(account.Scopes) {
		if scope == required {
			return true
		}
	}
	return false
}

func deploymentLeaseAuditDetails(ownerID string, context storage.DeploymentLeaseContext, lease *storage.DeploymentLease) map[string]any {
	if lease != nil && context == (storage.DeploymentLeaseContext{}) {
		context = lease.Context
	}
	details := map[string]any{"owner_id": ownerID, "context": context}
	if lease != nil {
		details["expires_at"] = lease.ExpiresAt
		details["active"] = lease.Active
	}
	return details
}
