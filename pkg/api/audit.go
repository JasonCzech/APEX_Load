package api

import (
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
	actorType, actorSubject, actorDisplayName := s.resolveAuditActor(r)
	_ = s.store.SaveAuditEvent(&storage.AuditEvent{
		ID:               uuid.New().String(),
		Action:           action,
		ActorType:        actorType,
		ActorSubject:     actorSubject,
		ActorDisplayName: actorDisplayName,
		ProjectID:        normalizeProjectID(projectID),
		ResourceType:     resourceType,
		ResourceID:       strings.TrimSpace(resourceID),
		Status:           strings.TrimSpace(status),
		Details:          details,
		CreatedAt:        time.Now().UTC(),
	})
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