package api

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

func (s *Server) recordAuditEvent(r *http.Request, action string, status string, resourceType string, resourceID string, projectID string, details map[string]any) {
	if s.store == nil {
		return
	}
	normalizedProjectID := normalizeProjectID(projectID)
	domainID := ""
	if normalizedProjectID != "" && normalizedProjectID != storage.DefaultProjectID {
		if project, err := s.store.GetProject(normalizedProjectID); err == nil && project != nil {
			domainID = normalizeDomainID(project.DomainID)
		}
	}
	s.recordAuditEventWithProjectSnapshot(r, action, status, resourceType, resourceID, normalizedProjectID, domainID, details)
}

// recordAuditEventWithProjectSnapshot preserves scope after the project row has
// been deleted. Destructive handlers capture the project/domain before commit
// and use this helper so the governance record is not reassigned to the default
// project or stripped of its original domain.
func (s *Server) recordAuditEventWithProjectSnapshot(r *http.Request, action string, status string, resourceType string, resourceID string, projectID string, domainID string, details map[string]any) {
	if s.store == nil {
		return
	}
	projectID = normalizeProjectID(projectID)
	domainID = strings.TrimSpace(domainID)
	if domainID != "" {
		domainID = normalizeDomainID(domainID)
	}
	actorType, actorSubject, actorDisplayName := s.resolveAuditActor(r)
	if err := s.store.SaveAuditEvent(&storage.AuditEvent{
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
	}); err != nil {
		slog.Warn("failed to persist audit event", "action", action, "resource_type", resourceType, "resource_id", resourceID, "error", err)
	}
}

func (s *Server) resolveAuditActor(r *http.Request) (string, string, string) {
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
	if isAuthBypassMode(normalizeAuthSettings(s.runner.GetSystemSettings().Auth).Mode) {
		return "dev_mode", "dev-mode", "Development Mode"
	}
	return "anonymous", "anonymous", ""
}
