package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

var errAuditPersistenceUnavailable = errors.New("audit persistence is unavailable")

// beginRequiredAuditOperation durably records intent before a governed
// mutation. The operation identifier correlates the later outcome without ever
// placing request payloads or credential material in the audit stream.
func (s *Server) beginRequiredAuditOperation(r *http.Request, action, resourceType, resourceID, projectID string, details map[string]any) (string, error) {
	operationID := uuid.New().String()
	if err := s.persistAuditEvent(r, action, "pending", resourceType, resourceID, projectID, operationAuditDetails(operationID, "pending", details)); err != nil {
		return "", err
	}
	return operationID, nil
}

func (s *Server) completeRequiredAuditOperation(r *http.Request, operationID, action, status, outcome, resourceType, resourceID, projectID string, details map[string]any) error {
	operationID = strings.TrimSpace(operationID)
	if operationID == "" {
		return errors.New("audit operation id is required")
	}
	err := s.persistAuditEvent(r, action, status, resourceType, resourceID, projectID, operationAuditDetails(operationID, outcome, details))
	if err == nil {
		s.recordObjectActivity(r, action, status, resourceType, resourceID, projectID, details)
	}
	return err
}

func operationAuditDetails(operationID, outcome string, details map[string]any) map[string]any {
	result := make(map[string]any, len(details)+2)
	for key, value := range details {
		result[key] = value
	}
	result["operation_id"] = strings.TrimSpace(operationID)
	result["outcome"] = strings.TrimSpace(outcome)
	return result
}

func (s *Server) recordAuditEvent(r *http.Request, action string, status string, resourceType string, resourceID string, projectID string, details map[string]any) {
	if err := s.persistAuditEvent(r, action, status, resourceType, resourceID, projectID, details); err != nil {
		slog.Warn("failed to persist audit event", "action", action, "resource_type", resourceType, "resource_id", resourceID, "error", err)
		s.activityWarning(r, err)
	}
	s.recordObjectActivity(r, action, status, resourceType, resourceID, projectID, details)
}

// persistAuditEvent is the fail-closed counterpart to recordAuditEvent. Callers
// that govern credential or policy mutations use it before and after the
// external/database operation so they can withhold a successful response when
// the audit trail is not durable.
func (s *Server) persistAuditEvent(r *http.Request, action string, status string, resourceType string, resourceID string, projectID string, details map[string]any) error {
	if s == nil || s.store == nil {
		return errAuditPersistenceUnavailable
	}
	normalizedProjectID := normalizeProjectID(projectID)
	domainID := ""
	if normalizedProjectID != "" && normalizedProjectID != storage.DefaultProjectID {
		if project, err := s.store.GetProject(normalizedProjectID); err == nil && project != nil {
			domainID = normalizeDomainID(project.DomainID)
		}
	}
	return s.persistAuditEventWithProjectSnapshot(r, action, status, resourceType, resourceID, normalizedProjectID, domainID, details)
}

// recordAuditEventWithProjectSnapshot preserves scope after the project row has
// been deleted. Destructive handlers capture the project/domain before commit
// and use this helper so the governance record is not reassigned to the default
// project or stripped of its original domain.
func (s *Server) recordAuditEventWithProjectSnapshot(r *http.Request, action string, status string, resourceType string, resourceID string, projectID string, domainID string, details map[string]any) {
	if err := s.persistAuditEventWithProjectSnapshot(r, action, status, resourceType, resourceID, projectID, domainID, details); err != nil {
		slog.Warn("failed to persist audit event", "action", action, "resource_type", resourceType, "resource_id", resourceID, "error", err)
	}
}

func (s *Server) persistAuditEventWithProjectSnapshot(r *http.Request, action string, status string, resourceType string, resourceID string, projectID string, domainID string, details map[string]any) error {
	if s == nil || s.store == nil {
		return errAuditPersistenceUnavailable
	}
	projectID = normalizeProjectID(projectID)
	domainID = strings.TrimSpace(domainID)
	if domainID != "" {
		domainID = normalizeDomainID(domainID)
	}
	actorType, actorSubject, actorDisplayName := s.resolveAuditActor(r)
	return s.store.SaveAuditEvent(&storage.AuditEvent{
		ID:               uuid.New().String(),
		Action:           action,
		ActorType:        actorType,
		ActorSubject:     actorSubject,
		ActorDisplayName: actorDisplayName,
		ProjectID:        projectID,
		DomainID:         domainID,
		ResourceType:     resourceType,
		ResourceID:       strings.TrimSpace(resourceID),
		Status:           strings.TrimSpace(status),
		Details:          details,
		CreatedAt:        time.Now().UTC(),
	})
}

func (s *Server) resolveAuditActor(r *http.Request) (string, string, string) {
	if claims, ok := ciClaimsFromContext(r.Context()); ok {
		return "service_account", "github-ci:" + claims.RepositoryID + ":" + claims.WorkflowRef, "GitHub Actions · " + claims.Repository
	}

	if account, ok := AuthenticatedServiceAccountFromContext(r.Context()); ok {
		return "service_account", "service-account:" + account.ID, account.Name
	}
	if user, ok := AuthenticatedUserFromContext(r.Context()); ok {
		return "user", user.Subject, user.DisplayName
	}
	if s.store != nil {
		if key := extractAPIKey(r); key != "" {
			if account, err := s.store.GetServiceAccountByKeyHash(hashAPIKey(key)); err == nil && account != nil {
				return "service_account", "service-account:" + account.ID, account.Name
			}
		}
	}
	if isAuthBypassMode(s.authSettingsForRequest().Mode) {
		return "dev_mode", "dev-mode", "Development Mode"
	}
	return "anonymous", "anonymous", ""
}
