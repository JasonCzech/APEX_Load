package api

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

var errCategoryDomainMismatch = errors.New("category does not belong to domain")

type domainHierarchyCreator interface {
	CreateDomainHierarchy(domain *storage.Domain, repository *storage.ApplicationRepository, category *storage.RepositoryCategory) error
}

type domainRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type repositoryRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type repositoryCategoryRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	SortOrder   int    `json:"sort_order,omitempty"`
}

func (s *Server) handleListDomains(w http.ResponseWriter, _ *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusOK, map[string]any{"domains": []*storage.Domain{}, "count": 0})
		return
	}
	domains, err := s.store.ListDomains()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"domains": domains, "count": len(domains)})
}

func (s *Server) handleCreateDomain(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	var req domainRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "domain name is required")
		return
	}
	now := time.Now()
	domain := &storage.Domain{
		ID:          uuid.New().String(),
		Name:        strings.TrimSpace(req.Name),
		Description: strings.TrimSpace(req.Description),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	repository := &storage.ApplicationRepository{
		ID:          uuid.New().String(),
		DomainID:    domain.ID,
		Name:        "Application Repository",
		Description: "Default application repository for this domain.",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	category := &storage.RepositoryCategory{
		ID:           uuid.New().String(),
		DomainID:     domain.ID,
		RepositoryID: repository.ID,
		Name:         "Uncategorized",
		Description:  "Default category for projects that have not been grouped yet.",
		SortOrder:    0,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := createDomainHierarchy(s.store, domain, repository, category); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordAuditEvent(r, "domain.create", "success", "domain", domain.ID, "", map[string]any{"name": domain.Name, "repository_id": repository.ID, "category_id": category.ID})
	writeJSON(w, http.StatusCreated, map[string]any{"domain": domain, "application_repository": repository, "default_category": category})
}

func createDomainHierarchy(store storage.Store, domain *storage.Domain, repository *storage.ApplicationRepository, category *storage.RepositoryCategory) error {
	if creator, ok := store.(domainHierarchyCreator); ok {
		return creator.CreateDomainHierarchy(domain, repository, category)
	}
	// Lightweight adapters retain compatibility while compensating on failure.
	// Production SQLite/PostgreSQL stores implement the atomic path above.
	if err := store.SaveDomain(domain); err != nil {
		return err
	}
	if err := store.SaveApplicationRepository(repository); err != nil {
		if cleanupErr := store.DeleteDomain(domain.ID); cleanupErr != nil {
			return errors.Join(err, fmt.Errorf("rollback domain hierarchy: %w", cleanupErr))
		}
		return err
	}
	if err := store.SaveRepositoryCategory(category); err != nil {
		if cleanupErr := store.DeleteDomain(domain.ID); cleanupErr != nil {
			return errors.Join(err, fmt.Errorf("rollback domain hierarchy: %w", cleanupErr))
		}
		return err
	}
	return nil
}

func (s *Server) handleGetDomain(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	domain, err := s.store.GetDomain(r.PathValue("id"))
	if err != nil {
		status := http.StatusInternalServerError
		if err == sql.ErrNoRows {
			status = http.StatusNotFound
		}
		writeError(w, status, "domain not found")
		return
	}
	writeJSON(w, http.StatusOK, domain)
}

func (s *Server) handleUpdateDomain(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	domain, err := s.store.GetDomain(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "domain not found")
		return
	}
	var req domainRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) != "" {
		domain.Name = strings.TrimSpace(req.Name)
	}
	domain.Description = strings.TrimSpace(req.Description)
	updater, supported := s.store.(interface {
		UpdateDomainIfUnchanged(domain *storage.Domain, expectedUpdatedAt time.Time) (bool, error)
	})
	if !supported {
		writeError(w, http.StatusNotImplemented, "optimistic domain updates are not supported by this storage backend")
		return
	}
	updated, err := updater.UpdateDomainIfUnchanged(domain, domain.UpdatedAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !updated {
		writeError(w, http.StatusConflict, "domain changed while the update was in flight; reload and retry")
		return
	}
	s.recordAuditEvent(r, "domain.update", "success", "domain", domain.ID, "", map[string]any{"name": domain.Name})
	writeJSON(w, http.StatusOK, domain)
}

func (s *Server) handleDeleteDomain(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	id := r.PathValue("id")
	if err := s.store.DeleteDomain(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "domain.delete", "success", "domain", id, "", nil)
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

func (s *Server) handleGetApplicationRepository(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	repository, err := s.store.GetApplicationRepository(normalizeDomainID(r.PathValue("id")))
	if err != nil {
		writeError(w, http.StatusNotFound, "application repository not found")
		return
	}
	writeJSON(w, http.StatusOK, repository)
}

func (s *Server) handleUpdateApplicationRepository(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	repository, err := s.store.GetApplicationRepository(normalizeDomainID(r.PathValue("id")))
	if err != nil {
		writeError(w, http.StatusNotFound, "application repository not found")
		return
	}
	var req repositoryRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) != "" {
		repository.Name = strings.TrimSpace(req.Name)
	}
	repository.Description = strings.TrimSpace(req.Description)
	updater, supported := s.store.(interface {
		UpdateApplicationRepositoryIfUnchanged(repository *storage.ApplicationRepository, expectedUpdatedAt time.Time) (bool, error)
	})
	if !supported {
		writeError(w, http.StatusNotImplemented, "optimistic application repository updates are not supported by this storage backend")
		return
	}
	updated, err := updater.UpdateApplicationRepositoryIfUnchanged(repository, repository.UpdatedAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !updated {
		writeError(w, http.StatusConflict, "application repository changed while the update was in flight; reload and retry")
		return
	}
	s.recordAuditEvent(r, "application_repository.update", "success", "application_repository", repository.ID, "", map[string]any{"domain_id": repository.DomainID, "name": repository.Name})
	writeJSON(w, http.StatusOK, repository)
}

func (s *Server) handleListRepositoryCategories(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusOK, map[string]any{"categories": []*storage.RepositoryCategory{}, "count": 0})
		return
	}
	categories, err := s.store.ListRepositoryCategories(normalizeDomainID(r.PathValue("id")))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"categories": categories, "count": len(categories)})
}

func (s *Server) handleCreateRepositoryCategory(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	domainID := normalizeDomainID(r.PathValue("id"))
	repository, err := s.store.GetApplicationRepository(domainID)
	if err != nil {
		writeError(w, http.StatusNotFound, "application repository not found")
		return
	}
	var req repositoryCategoryRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "category name is required")
		return
	}
	now := time.Now()
	category := &storage.RepositoryCategory{
		ID:           uuid.New().String(),
		DomainID:     domainID,
		RepositoryID: repository.ID,
		Name:         strings.TrimSpace(req.Name),
		Description:  strings.TrimSpace(req.Description),
		SortOrder:    req.SortOrder,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := s.store.SaveRepositoryCategory(category); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "repository_category.create", "success", "repository_category", category.ID, "", map[string]any{"domain_id": domainID, "repository_id": repository.ID, "name": category.Name})
	writeJSON(w, http.StatusCreated, category)
}

func (s *Server) handleUpdateRepositoryCategory(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	category, err := s.store.GetRepositoryCategory(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "category not found")
		return
	}
	var req repositoryCategoryRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) != "" {
		category.Name = strings.TrimSpace(req.Name)
	}
	category.Description = strings.TrimSpace(req.Description)
	category.SortOrder = req.SortOrder
	updater, supported := s.store.(interface {
		UpdateRepositoryCategoryIfUnchanged(category *storage.RepositoryCategory, expectedUpdatedAt time.Time) (bool, error)
	})
	if !supported {
		writeError(w, http.StatusNotImplemented, "optimistic repository category updates are not supported by this storage backend")
		return
	}
	updated, err := updater.UpdateRepositoryCategoryIfUnchanged(category, category.UpdatedAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !updated {
		writeError(w, http.StatusConflict, "repository category changed while the update was in flight; reload and retry")
		return
	}
	s.recordAuditEvent(r, "repository_category.update", "success", "repository_category", category.ID, "", map[string]any{"domain_id": category.DomainID, "name": category.Name})
	writeJSON(w, http.StatusOK, category)
}

func (s *Server) handleDeleteRepositoryCategory(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	id := r.PathValue("id")
	if err := s.store.DeleteRepositoryCategory(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "repository_category.delete", "success", "repository_category", id, "", nil)
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

func normalizeDomainID(domainID string) string {
	if strings.TrimSpace(domainID) == "" {
		return storage.DefaultDomainID
	}
	return strings.TrimSpace(domainID)
}

func normalizeRepositoryCategoryID(categoryID string) string {
	if strings.TrimSpace(categoryID) == "" {
		return storage.DefaultRepositoryCategoryID
	}
	return strings.TrimSpace(categoryID)
}

func (s *Server) resolveProjectPlacement(domainID string, categoryID string) (string, string, error) {
	domainID = normalizeDomainID(domainID)
	categoryID = strings.TrimSpace(categoryID)
	if categoryID == "" {
		categories, err := s.store.ListRepositoryCategories(domainID)
		if err != nil {
			return "", "", err
		}
		if len(categories) == 0 {
			return "", "", sql.ErrNoRows
		}
		categoryID = categories[0].ID
	}
	category, err := s.store.GetRepositoryCategory(categoryID)
	if err != nil {
		return "", "", err
	}
	if normalizeDomainID(category.DomainID) != domainID {
		return "", "", errCategoryDomainMismatch
	}
	return domainID, category.ID, nil
}
