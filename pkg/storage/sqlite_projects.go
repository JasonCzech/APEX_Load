package storage

// SQLite store — projects. Split from sqlite.go (same package, behavior-preserving).

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

func validateFolderCategory(category string) error {
	switch category {
	case "scripts", "scenarios", "results", "recordings", "general":
		return nil
	default:
		return fmt.Errorf("invalid folder category: %s", category)
	}
}

func (s *SQLiteStore) SaveDomain(domain *Domain) error {
	if domain == nil {
		return errors.New("domain is required")
	}
	if strings.TrimSpace(domain.ID) == "" {
		return errors.New("domain id is required")
	}
	if strings.TrimSpace(domain.Name) == "" {
		return errors.New("domain name is required")
	}
	if domain.CreatedAt.IsZero() {
		domain.CreatedAt = time.Now()
	}
	domain.UpdatedAt = time.Now()
	_, err := s.db.Exec(`INSERT INTO domains (id, name, description, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, description=excluded.description, updated_at=excluded.updated_at`,
		domain.ID, strings.TrimSpace(domain.Name), strings.TrimSpace(domain.Description), domain.CreatedAt, domain.UpdatedAt)
	return err
}

// UpdateDomainIfUnchanged rejects stale administrative edits instead of
// silently overwriting a concurrent rename or description change.
func (s *SQLiteStore) UpdateDomainIfUnchanged(domain *Domain, expectedUpdatedAt time.Time) (bool, error) {
	if domain == nil || strings.TrimSpace(domain.ID) == "" {
		return false, errors.New("domain id is required")
	}
	if strings.TrimSpace(domain.Name) == "" {
		return false, errors.New("domain name is required")
	}
	if expectedUpdatedAt.IsZero() {
		return false, errors.New("expected domain updated_at is required")
	}
	return updateNamedEntityIfUnchanged(s, "domains", domain.ID, strings.TrimSpace(domain.Name), strings.TrimSpace(domain.Description), expectedUpdatedAt, &domain.UpdatedAt)
}

func updateNamedEntityIfUnchanged(s *SQLiteStore, table, id, name, description string, expectedUpdatedAt time.Time, updatedAt *time.Time) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var currentUpdatedAt time.Time
	var currentUpdatedRaw string
	query := fmt.Sprintf(`SELECT updated_at, CAST(updated_at AS TEXT) FROM %s WHERE id = ?`, table)
	if err := tx.QueryRow(query, id).Scan(&currentUpdatedAt, &currentUpdatedRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if !currentUpdatedAt.Equal(expectedUpdatedAt) {
		return false, nil
	}
	now := time.Now()
	query = fmt.Sprintf(`UPDATE %s SET name = ?, description = ?, updated_at = ? WHERE id = ? AND CAST(updated_at AS TEXT) = ?`, table)
	result, err := tx.Exec(query, name, description, now, id, currentUpdatedRaw)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	*updatedAt = now
	return true, nil
}

// CreateDomainHierarchy atomically creates a domain and its required default
// application repository/category. A partially-created hierarchy is unusable
// by project placement and must never become visible after a later insert fails.
func (s *SQLiteStore) CreateDomainHierarchy(domain *Domain, repository *ApplicationRepository, category *RepositoryCategory) error {
	if domain == nil || repository == nil || category == nil {
		return errors.New("domain, application repository, and repository category are required")
	}
	domain.ID = strings.TrimSpace(domain.ID)
	domain.Name = strings.TrimSpace(domain.Name)
	repository.ID = strings.TrimSpace(repository.ID)
	repository.DomainID = normalizeDomainID(repository.DomainID)
	repository.Name = strings.TrimSpace(repository.Name)
	category.ID = strings.TrimSpace(category.ID)
	category.DomainID = normalizeDomainID(category.DomainID)
	category.RepositoryID = normalizeRepositoryID(category.RepositoryID)
	category.Name = strings.TrimSpace(category.Name)
	if domain.ID == "" || domain.Name == "" {
		return errors.New("domain id and name are required")
	}
	if repository.ID == "" || repository.Name == "" || repository.DomainID != domain.ID {
		return errors.New("application repository must belong to the new domain")
	}
	if category.ID == "" || category.Name == "" || category.DomainID != domain.ID || category.RepositoryID != repository.ID {
		return errors.New("repository category must belong to the new domain and application repository")
	}
	now := time.Now()
	if domain.CreatedAt.IsZero() {
		domain.CreatedAt = now
	}
	if repository.CreatedAt.IsZero() {
		repository.CreatedAt = now
	}
	if category.CreatedAt.IsZero() {
		category.CreatedAt = now
	}
	domain.UpdatedAt = now
	repository.UpdatedAt = now
	category.UpdatedAt = now

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO domains (id, name, description, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		domain.ID, domain.Name, strings.TrimSpace(domain.Description), domain.CreatedAt, domain.UpdatedAt); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO application_repositories (id, domain_id, name, description, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		repository.ID, repository.DomainID, repository.Name, strings.TrimSpace(repository.Description), repository.CreatedAt, repository.UpdatedAt); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO repository_categories (id, domain_id, repository_id, name, description, sort_order, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		category.ID, category.DomainID, category.RepositoryID, category.Name, strings.TrimSpace(category.Description), category.SortOrder, category.CreatedAt, category.UpdatedAt); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) GetDomain(id string) (*Domain, error) {
	var domain Domain
	err := s.db.QueryRow(`SELECT id, name, COALESCE(description, ''), created_at, updated_at FROM domains WHERE id = ?`, normalizeDomainID(id)).Scan(
		&domain.ID, &domain.Name, &domain.Description, &domain.CreatedAt, &domain.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &domain, nil
}

func (s *SQLiteStore) ListDomains() ([]*Domain, error) {
	rows, err := s.db.Query(`SELECT id, name, COALESCE(description, ''), created_at, updated_at FROM domains ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var domains []*Domain
	for rows.Next() {
		var domain Domain
		if err := rows.Scan(&domain.ID, &domain.Name, &domain.Description, &domain.CreatedAt, &domain.UpdatedAt); err != nil {
			return nil, err
		}
		domains = append(domains, &domain)
	}
	return domains, rows.Err()
}

func (s *SQLiteStore) DeleteDomain(id string) error {
	id = normalizeDomainID(id)
	if id == DefaultDomainID {
		return errors.New("default domain cannot be deleted")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	locked, err := tx.Exec(`UPDATE domains SET updated_at = updated_at WHERE id = ?`, id)
	if err != nil {
		return err
	}
	affected, err := locked.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return nil
	}
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM projects WHERE domain_id = ?`, id).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return errors.New("domain has projects")
	}
	var repositoryID string
	if err := tx.QueryRow(`SELECT id FROM application_repositories WHERE domain_id = ?`, id).Scan(&repositoryID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err := deleteDomainServiceAccountGrants(tx, id, repositoryID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM user_role_bindings
		WHERE (scope_type = 'domain' AND scope_id = ?)
		   OR (scope_type = 'application_repository' AND scope_id = ?)`, id, repositoryID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM repository_categories WHERE domain_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM application_repositories WHERE domain_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM domains WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) SaveApplicationRepository(repository *ApplicationRepository) error {
	if repository == nil {
		return errors.New("application repository is required")
	}
	if strings.TrimSpace(repository.ID) == "" {
		return errors.New("application repository id is required")
	}
	repository.DomainID = normalizeDomainID(repository.DomainID)
	if strings.TrimSpace(repository.Name) == "" {
		return errors.New("application repository name is required")
	}
	if repository.CreatedAt.IsZero() {
		repository.CreatedAt = time.Now()
	}
	repository.UpdatedAt = time.Now()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockNamedPlacementRow(tx, "domains", repository.DomainID, "application repository domain"); err != nil {
		return err
	}
	result, err := tx.Exec(`INSERT INTO application_repositories (id, domain_id, name, description, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, description=excluded.description, updated_at=excluded.updated_at
		WHERE application_repositories.domain_id = excluded.domain_id`,
		repository.ID, repository.DomainID, strings.TrimSpace(repository.Name), strings.TrimSpace(repository.Description), repository.CreatedAt, repository.UpdatedAt)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return errors.New("application repository id belongs to another domain")
	}
	return tx.Commit()
}

// UpdateApplicationRepositoryIfUnchanged preserves immutable domain ownership
// and rejects stale full-row updates.
func (s *SQLiteStore) UpdateApplicationRepositoryIfUnchanged(repository *ApplicationRepository, expectedUpdatedAt time.Time) (bool, error) {
	if repository == nil || strings.TrimSpace(repository.ID) == "" {
		return false, errors.New("application repository id is required")
	}
	if strings.TrimSpace(repository.Name) == "" {
		return false, errors.New("application repository name is required")
	}
	if expectedUpdatedAt.IsZero() {
		return false, errors.New("expected application repository updated_at is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var currentDomainID string
	var currentUpdatedAt time.Time
	var currentUpdatedRaw string
	if err := tx.QueryRow(`SELECT domain_id, updated_at, CAST(updated_at AS TEXT) FROM application_repositories WHERE id = ?`, repository.ID).Scan(&currentDomainID, &currentUpdatedAt, &currentUpdatedRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if normalizeDomainID(currentDomainID) != normalizeDomainID(repository.DomainID) || !currentUpdatedAt.Equal(expectedUpdatedAt) {
		return false, nil
	}
	repository.DomainID = normalizeDomainID(currentDomainID)
	repository.UpdatedAt = time.Now()
	result, err := tx.Exec(`UPDATE application_repositories SET name = ?, description = ?, updated_at = ?
		WHERE id = ? AND domain_id = ? AND CAST(updated_at AS TEXT) = ?`, strings.TrimSpace(repository.Name), strings.TrimSpace(repository.Description), repository.UpdatedAt, repository.ID, repository.DomainID, currentUpdatedRaw)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return false, err
	}
	return true, tx.Commit()
}

func (s *SQLiteStore) GetApplicationRepository(domainID string) (*ApplicationRepository, error) {
	var repository ApplicationRepository
	err := s.db.QueryRow(`SELECT id, domain_id, name, COALESCE(description, ''), created_at, updated_at FROM application_repositories WHERE domain_id = ?`, normalizeDomainID(domainID)).Scan(
		&repository.ID, &repository.DomainID, &repository.Name, &repository.Description, &repository.CreatedAt, &repository.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &repository, nil
}

func (s *SQLiteStore) SaveRepositoryCategory(category *RepositoryCategory) error {
	if category == nil {
		return errors.New("repository category is required")
	}
	if strings.TrimSpace(category.ID) == "" {
		return errors.New("repository category id is required")
	}
	category.DomainID = normalizeDomainID(category.DomainID)
	category.RepositoryID = normalizeRepositoryID(category.RepositoryID)
	if strings.TrimSpace(category.Name) == "" {
		return errors.New("repository category name is required")
	}
	if category.CreatedAt.IsZero() {
		category.CreatedAt = time.Now()
	}
	category.UpdatedAt = time.Now()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockRepositoryPlacement(tx, category.DomainID, category.RepositoryID); err != nil {
		return err
	}
	result, err := tx.Exec(`INSERT INTO repository_categories (id, domain_id, repository_id, name, description, sort_order, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, description=excluded.description, sort_order=excluded.sort_order, updated_at=excluded.updated_at
		WHERE repository_categories.domain_id = excluded.domain_id
			AND repository_categories.repository_id = excluded.repository_id`,
		category.ID, category.DomainID, category.RepositoryID, strings.TrimSpace(category.Name), strings.TrimSpace(category.Description), category.SortOrder, category.CreatedAt, category.UpdatedAt)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return errors.New("repository category id belongs to another hierarchy")
	}
	return tx.Commit()
}

func lockNamedPlacementRow(tx *storeTx, table, id, label string) error {
	result, err := tx.Exec(`UPDATE `+table+` SET updated_at = updated_at WHERE id = ?`, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("%s %q does not exist", label, id)
	}
	return nil
}

// lockRepositoryPlacement validates the composite domain/repository ownership
// invariant under the same transaction as the dependent write. Individual
// foreign keys cannot express that both identifiers belong to one hierarchy.
func lockRepositoryPlacement(tx *storeTx, domainID, repositoryID string) error {
	if err := lockNamedPlacementRow(tx, "domains", domainID, "domain"); err != nil {
		return err
	}
	result, err := tx.Exec(`UPDATE application_repositories SET updated_at = updated_at WHERE id = ? AND domain_id = ?`, repositoryID, domainID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return errors.New("application repository does not belong to domain")
	}
	return nil
}

func lockProjectPlacement(tx *storeTx, domainID, categoryID string) error {
	if err := lockNamedPlacementRow(tx, "domains", domainID, "project domain"); err != nil {
		return err
	}
	var repositoryID string
	if err := tx.QueryRow(`SELECT repository_id FROM repository_categories WHERE id = ? AND domain_id = ?`, categoryID, domainID).Scan(&repositoryID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("project category does not belong to domain")
		}
		return err
	}
	if err := lockRepositoryPlacement(tx, domainID, repositoryID); err != nil {
		return err
	}
	result, err := tx.Exec(`UPDATE repository_categories SET updated_at = updated_at WHERE id = ? AND domain_id = ? AND repository_id = ?`, categoryID, domainID, repositoryID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return errors.New("project category placement changed concurrently")
	}
	return nil
}

// UpdateRepositoryCategoryIfUnchanged preserves the immutable domain/repository
// relationship while rejecting stale administrative edits.
func (s *SQLiteStore) UpdateRepositoryCategoryIfUnchanged(category *RepositoryCategory, expectedUpdatedAt time.Time) (bool, error) {
	if category == nil || strings.TrimSpace(category.ID) == "" {
		return false, errors.New("repository category id is required")
	}
	if strings.TrimSpace(category.Name) == "" {
		return false, errors.New("repository category name is required")
	}
	if expectedUpdatedAt.IsZero() {
		return false, errors.New("expected repository category updated_at is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var currentDomainID, currentRepositoryID string
	var currentUpdatedAt time.Time
	var currentUpdatedRaw string
	if err := tx.QueryRow(`SELECT domain_id, repository_id, updated_at, CAST(updated_at AS TEXT) FROM repository_categories WHERE id = ?`, category.ID).Scan(&currentDomainID, &currentRepositoryID, &currentUpdatedAt, &currentUpdatedRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if normalizeDomainID(currentDomainID) != normalizeDomainID(category.DomainID) || normalizeRepositoryID(currentRepositoryID) != normalizeRepositoryID(category.RepositoryID) || !currentUpdatedAt.Equal(expectedUpdatedAt) {
		return false, nil
	}
	category.DomainID = normalizeDomainID(currentDomainID)
	category.RepositoryID = normalizeRepositoryID(currentRepositoryID)
	if err := lockRepositoryPlacement(tx, category.DomainID, category.RepositoryID); err != nil {
		return false, err
	}
	category.UpdatedAt = time.Now()
	result, err := tx.Exec(`UPDATE repository_categories SET name = ?, description = ?, sort_order = ?, updated_at = ?
		WHERE id = ? AND domain_id = ? AND repository_id = ? AND CAST(updated_at AS TEXT) = ?`, strings.TrimSpace(category.Name), strings.TrimSpace(category.Description), category.SortOrder, category.UpdatedAt, category.ID, category.DomainID, category.RepositoryID, currentUpdatedRaw)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return false, err
	}
	return true, tx.Commit()
}

func (s *SQLiteStore) GetRepositoryCategory(id string) (*RepositoryCategory, error) {
	var category RepositoryCategory
	err := s.db.QueryRow(`SELECT id, domain_id, repository_id, name, COALESCE(description, ''), COALESCE(sort_order, 0), created_at, updated_at FROM repository_categories WHERE id = ?`, normalizeRepositoryCategoryID(id)).Scan(
		&category.ID, &category.DomainID, &category.RepositoryID, &category.Name, &category.Description, &category.SortOrder, &category.CreatedAt, &category.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &category, nil
}

func (s *SQLiteStore) ListRepositoryCategories(domainID string) ([]*RepositoryCategory, error) {
	rows, err := s.db.Query(`SELECT id, domain_id, repository_id, name, COALESCE(description, ''), COALESCE(sort_order, 0), created_at, updated_at FROM repository_categories WHERE domain_id = ? ORDER BY sort_order ASC, name ASC`, normalizeDomainID(domainID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var categories []*RepositoryCategory
	for rows.Next() {
		var category RepositoryCategory
		if err := rows.Scan(&category.ID, &category.DomainID, &category.RepositoryID, &category.Name, &category.Description, &category.SortOrder, &category.CreatedAt, &category.UpdatedAt); err != nil {
			return nil, err
		}
		categories = append(categories, &category)
	}
	return categories, rows.Err()
}

func (s *SQLiteStore) DeleteRepositoryCategory(id string) error {
	id = normalizeRepositoryCategoryID(id)
	if id == DefaultRepositoryCategoryID {
		return errors.New("default repository category cannot be deleted")
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM projects WHERE category_id = ?`, id).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return errors.New("repository category has projects")
	}
	_, err := s.db.Exec(`DELETE FROM repository_categories WHERE id = ?`, id)
	return err
}

func (s *SQLiteStore) SaveApplication(application *Application) error {
	if application == nil {
		return errors.New("application is required")
	}
	application.ID = strings.TrimSpace(application.ID)
	if application.ID == "" {
		return errors.New("application id is required")
	}
	application.ProjectID = normalizeProjectID(strings.TrimSpace(application.ProjectID))
	application.Name = strings.TrimSpace(application.Name)
	if application.Name == "" {
		return errors.New("application name is required")
	}
	if application.CreatedAt.IsZero() {
		application.CreatedAt = time.Now()
	}
	application.UpdatedAt = time.Now()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockRequiredPlacementParent(tx,
		`UPDATE projects SET updated_at = updated_at WHERE id = ?`, application.ProjectID, "application", "project"); err != nil {
		return err
	}
	locked, err := tx.Exec(`UPDATE applications SET updated_at = updated_at WHERE id = ?`, application.ID)
	if err != nil {
		return err
	}
	affected, err := locked.RowsAffected()
	if err != nil {
		return err
	}
	applicationExisted := affected == 1
	if applicationExisted {
		var currentProjectID string
		if err := tx.QueryRow(`SELECT COALESCE(project_id, '') FROM applications WHERE id = ?`, application.ID).Scan(&currentProjectID); err != nil {
			return err
		}
		if normalizeProjectID(currentProjectID) != application.ProjectID {
			return fmt.Errorf("application %q already belongs to project %q, not %q", application.ID, normalizeProjectID(currentProjectID), application.ProjectID)
		}
	}
	result, err := tx.Exec(`INSERT INTO applications (id, project_id, name, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, updated_at=excluded.updated_at
		WHERE COALESCE(NULLIF(applications.project_id, ''), '`+DefaultProjectID+`')=excluded.project_id`,
		application.ID, application.ProjectID, application.Name, application.CreatedAt, application.UpdatedAt)
	if err != nil {
		return err
	}
	affected, err = result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("application %q belongs to another project", application.ID)
	}
	// Project release families are represented by one application-owned row per
	// application. A newly-created application inherits every existing family so
	// selecting it never makes project releases disappear.
	if !applicationExisted {
		rows, err := tx.Query(`SELECT COALESCE(NULLIF(group_id, ''), id), name, created_at, updated_at, archived_at
			FROM releases
			WHERE project_id = ?
			ORDER BY COALESCE(NULLIF(group_id, ''), id) ASC, updated_at DESC, id ASC`, application.ProjectID)
		if err != nil {
			return err
		}
		type familySeed struct {
			groupID    string
			name       string
			createdAt  time.Time
			updatedAt  time.Time
			archivedAt *time.Time
		}
		seeds := make([]familySeed, 0)
		seenFamilies := make(map[string]struct{})
		for rows.Next() {
			var seed familySeed
			if err := rows.Scan(&seed.groupID, &seed.name, &seed.createdAt, &seed.updatedAt, &seed.archivedAt); err != nil {
				rows.Close()
				return err
			}
			if _, exists := seenFamilies[seed.groupID]; exists {
				continue
			}
			seenFamilies[seed.groupID] = struct{}{}
			seeds = append(seeds, seed)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, seed := range seeds {
			if _, err := tx.Exec(`INSERT INTO releases (id, group_id, application_id, project_id, name, created_at, updated_at, archived_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, uuid.NewString(), seed.groupID, application.ID, application.ProjectID, seed.name, seed.createdAt, application.UpdatedAt, seed.archivedAt); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// UpdateApplicationIfUnchanged updates the application name only when the
// durable row still matches the caller's snapshot. Project ownership is an
// immutable authorization boundary.
func (s *SQLiteStore) UpdateApplicationIfUnchanged(application *Application, expectedUpdatedAt time.Time) (bool, error) {
	if application == nil || strings.TrimSpace(application.ID) == "" {
		return false, errors.New("application id is required")
	}
	if strings.TrimSpace(application.Name) == "" {
		return false, errors.New("application name is required")
	}
	if expectedUpdatedAt.IsZero() {
		return false, errors.New("expected application updated_at is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var currentProjectID string
	var currentUpdatedAt time.Time
	var currentUpdatedRaw string
	if err := tx.QueryRow(`SELECT COALESCE(project_id, ''), updated_at, CAST(updated_at AS TEXT) FROM applications WHERE id = ?`, application.ID).Scan(&currentProjectID, &currentUpdatedAt, &currentUpdatedRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if normalizeProjectID(currentProjectID) != normalizeProjectID(application.ProjectID) || !currentUpdatedAt.Equal(expectedUpdatedAt) {
		return false, nil
	}
	application.ProjectID = normalizeProjectID(currentProjectID)
	application.UpdatedAt = time.Now()
	result, err := tx.Exec(`UPDATE applications SET name = ?, updated_at = ? WHERE id = ? AND CAST(updated_at AS TEXT) = ?`, application.Name, application.UpdatedAt, application.ID, currentUpdatedRaw)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return false, err
	}
	return true, tx.Commit()
}

func (s *SQLiteStore) GetApplication(id string) (*Application, error) {
	var application Application
	err := s.db.QueryRow(`SELECT id, COALESCE(project_id, ''), name, created_at, updated_at FROM applications WHERE id = ?`, id).Scan(
		&application.ID, &application.ProjectID, &application.Name, &application.CreatedAt, &application.UpdatedAt)
	if err != nil {
		return nil, err
	}
	application.ProjectID = normalizeProjectID(application.ProjectID)
	return &application, nil
}

func (s *SQLiteStore) ListApplications() ([]*Application, error) {
	rows, err := s.db.Query(`SELECT id, COALESCE(project_id, ''), name, created_at, updated_at FROM applications ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var applications []*Application
	for rows.Next() {
		var application Application
		if err := rows.Scan(&application.ID, &application.ProjectID, &application.Name, &application.CreatedAt, &application.UpdatedAt); err != nil {
			return nil, err
		}
		application.ProjectID = normalizeProjectID(application.ProjectID)
		applications = append(applications, &application)
	}
	return applications, rows.Err()
}

func (s *SQLiteStore) DeleteApplication(id string) error {
	id = strings.TrimSpace(id)
	if id == LegacyApplicationID {
		return errors.New("legacy application cannot be deleted")
	}
	if id == "" {
		return errors.New("application id is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var projectID string
	if err := tx.QueryRow(`SELECT COALESCE(project_id, '') FROM applications WHERE id = ?`, id).Scan(&projectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	projectID = normalizeProjectID(projectID)
	if id == legacyApplicationID(projectID) {
		return errors.New("legacy application cannot be deleted")
	}
	// Match the project -> application -> release lock order used by every
	// hierarchy writer so a concurrent child insert cannot outlive this delete.
	if _, err := tx.Exec(`UPDATE projects SET updated_at = updated_at WHERE id = ?`, projectID); err != nil {
		return err
	}
	if err := lockRequiredPlacementParent(tx,
		`UPDATE applications SET updated_at = updated_at WHERE id = ?`, id, "delete application", "application"); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE releases SET updated_at = updated_at WHERE application_id = ?`, id); err != nil {
		return err
	}
	var dependentCount int
	if err := tx.QueryRow(`SELECT
		(SELECT COUNT(*) FROM tests WHERE application_id = ? OR release_id IN (SELECT id FROM releases WHERE application_id = ?)) +
		(SELECT COUNT(*) FROM external_result_imports WHERE application_id = ? OR release_id IN (SELECT id FROM releases WHERE application_id = ?)) +
		(SELECT COUNT(*) FROM scenario_schedules WHERE release_id IN (SELECT id FROM releases WHERE application_id = ?)) +
		(SELECT COUNT(*) FROM release_sla_waivers WHERE release_id IN (SELECT id FROM releases WHERE application_id = ?))`,
		id, id, id, id, id, id).Scan(&dependentCount); err != nil {
		return err
	}
	if dependentCount > 0 {
		return errors.New("application has associated results, schedules, or governance records")
	}
	if _, err := tx.Exec(`DELETE FROM releases WHERE application_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM applications WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) SaveRelease(release *Release) error {
	if release == nil {
		return errors.New("release is required")
	}
	release.ID = strings.TrimSpace(release.ID)
	if release.ID == "" {
		return errors.New("release id is required")
	}
	release.GroupID = strings.TrimSpace(release.GroupID)
	if release.GroupID == "" {
		release.GroupID = release.ID
	}
	release.ApplicationID = normalizeApplicationID(strings.TrimSpace(release.ApplicationID))
	if release.ApplicationID == "" {
		return errors.New("release application id is required")
	}
	var applicationProjectID string
	if err := s.db.QueryRow(`SELECT COALESCE(project_id, '') FROM applications WHERE id = ?`, release.ApplicationID).Scan(&applicationProjectID); err != nil {
		return fmt.Errorf("load release application: %w", err)
	}
	if strings.TrimSpace(release.ProjectID) == "" {
		release.ProjectID = applicationProjectID
	}
	release.ProjectID = normalizeProjectID(strings.TrimSpace(release.ProjectID))
	if normalizeProjectID(applicationProjectID) != release.ProjectID {
		return errors.New("release project does not match application project")
	}
	release.Name = strings.TrimSpace(release.Name)
	if release.Name == "" {
		return errors.New("release name is required")
	}
	if release.CreatedAt.IsZero() {
		release.CreatedAt = time.Now()
	}
	release.UpdatedAt = time.Now()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockRequiredPlacementParent(tx,
		`UPDATE projects SET updated_at = updated_at WHERE id = ?`, release.ProjectID, "release", "project"); err != nil {
		return err
	}
	if err := lockRequiredPlacementParent(tx,
		`UPDATE applications SET updated_at = updated_at WHERE id = ?`, release.ApplicationID, "release", "application"); err != nil {
		return err
	}
	if err := tx.QueryRow(`SELECT COALESCE(project_id, '') FROM applications WHERE id = ?`, release.ApplicationID).Scan(&applicationProjectID); err != nil {
		return err
	}
	if normalizeProjectID(applicationProjectID) != release.ProjectID {
		return errors.New("release project does not match application project")
	}
	locked, err := tx.Exec(`UPDATE releases SET updated_at = updated_at WHERE id = ?`, release.ID)
	if err != nil {
		return err
	}
	affected, err := locked.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 1 {
		var currentApplicationID, currentProjectID string
		if err := tx.QueryRow(`SELECT application_id, COALESCE(project_id, '') FROM releases WHERE id = ?`, release.ID).Scan(&currentApplicationID, &currentProjectID); err != nil {
			return err
		}
		if currentApplicationID != release.ApplicationID || normalizeProjectID(currentProjectID) != release.ProjectID {
			return fmt.Errorf("release %q already belongs to application %q in project %q", release.ID, currentApplicationID, normalizeProjectID(currentProjectID))
		}
	}
	result, err := tx.Exec(`INSERT INTO releases (id, group_id, application_id, project_id, name, created_at, updated_at, archived_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET group_id=excluded.group_id, name=excluded.name, updated_at=excluded.updated_at, archived_at=excluded.archived_at
		WHERE releases.application_id=excluded.application_id
		AND COALESCE(NULLIF(releases.project_id, ''), '`+DefaultProjectID+`')=excluded.project_id`,
		release.ID, release.GroupID, release.ApplicationID, release.ProjectID, release.Name, release.CreatedAt, release.UpdatedAt, release.ArchivedAt)
	if err != nil {
		return err
	}
	affected, err = result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("release %q belongs to another application or project", release.ID)
	}
	return tx.Commit()
}

// UpdateReleaseIfUnchanged updates editor-owned fields while preserving the
// immutable application/project relationship and rejecting stale snapshots.
func (s *SQLiteStore) UpdateReleaseIfUnchanged(release *Release, expectedUpdatedAt time.Time) (bool, error) {
	if release == nil || strings.TrimSpace(release.ID) == "" {
		return false, errors.New("release id is required")
	}
	if strings.TrimSpace(release.Name) == "" {
		return false, errors.New("release name is required")
	}
	if expectedUpdatedAt.IsZero() {
		return false, errors.New("expected release updated_at is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var preliminaryProjectID string
	if err := tx.QueryRow(`SELECT COALESCE(project_id, '') FROM releases
		WHERE id = ? OR COALESCE(NULLIF(group_id, ''), id) = ?
		ORDER BY CASE WHEN id = ? THEN 0 ELSE 1 END, application_id ASC, id ASC LIMIT 1`, release.ID, release.ID, release.ID).Scan(&preliminaryProjectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	preliminaryProjectID = normalizeProjectID(preliminaryProjectID)
	if err := lockRequiredPlacementParent(tx,
		`UPDATE projects SET updated_at = updated_at WHERE id = ?`, preliminaryProjectID, "release update", "project"); err != nil {
		return false, err
	}
	var currentApplicationID, currentProjectID string
	var currentUpdatedAt time.Time
	var currentUpdatedRaw string
	var currentID, groupID string
	if err := tx.QueryRow(`SELECT id, COALESCE(NULLIF(group_id, ''), id), application_id, COALESCE(project_id, ''), updated_at, CAST(updated_at AS TEXT)
		FROM releases
		WHERE id = ? OR COALESCE(NULLIF(group_id, ''), id) = ?
		ORDER BY CASE WHEN id = ? THEN 0 ELSE 1 END, application_id ASC, id ASC LIMIT 1`, release.ID, release.ID, release.ID).Scan(
		&currentID, &groupID, &currentApplicationID, &currentProjectID, &currentUpdatedAt, &currentUpdatedRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if currentApplicationID != normalizeApplicationID(release.ApplicationID) || normalizeProjectID(currentProjectID) != normalizeProjectID(release.ProjectID) || !currentUpdatedAt.Equal(expectedUpdatedAt) {
		return false, nil
	}
	release.ApplicationID = currentApplicationID
	release.ProjectID = normalizeProjectID(currentProjectID)
	release.ID = currentID
	release.GroupID = groupID
	release.UpdatedAt = time.Now()
	result, err := tx.Exec(`UPDATE releases SET name = ?, updated_at = ?
		WHERE COALESCE(NULLIF(group_id, ''), id) = ? AND project_id = ?
		AND EXISTS (SELECT 1 FROM releases current WHERE current.id = ? AND CAST(current.updated_at AS TEXT) = ?)`,
		release.Name, release.UpdatedAt, groupID, release.ProjectID, currentID, currentUpdatedRaw)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected < 1 {
		return false, err
	}
	return true, tx.Commit()
}

func resolveReleaseFamilyIdentityTx(tx *storeTx, id string) (projectID string, groupID string, err error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", "", errors.New("release id is required")
	}
	err = tx.QueryRow(`SELECT COALESCE(project_id, ''), COALESCE(NULLIF(group_id, ''), id)
		FROM releases
		WHERE id = ? OR COALESCE(NULLIF(group_id, ''), id) = ?
		ORDER BY CASE WHEN id = ? THEN 0 ELSE 1 END, application_id ASC, id ASC LIMIT 1`, id, id, id).Scan(&projectID, &groupID)
	if err != nil {
		return "", "", err
	}
	return normalizeProjectID(projectID), groupID, nil
}

func (s *SQLiteStore) GetRelease(id string) (*Release, error) {
	var release Release
	err := s.db.QueryRow(`SELECT id, COALESCE(NULLIF(group_id, ''), id), application_id, COALESCE(project_id, ''), name, created_at, updated_at, archived_at
		FROM releases
		WHERE id = ? OR COALESCE(NULLIF(group_id, ''), id) = ?
		ORDER BY CASE WHEN id = ? THEN 0 ELSE 1 END, application_id ASC, id ASC
		LIMIT 1`, id, id, id).Scan(
		&release.ID, &release.GroupID, &release.ApplicationID, &release.ProjectID, &release.Name, &release.CreatedAt, &release.UpdatedAt, &release.ArchivedAt)
	if err != nil {
		return nil, err
	}
	release.ProjectID = normalizeProjectID(release.ProjectID)
	return &release, nil
}

func (s *SQLiteStore) ListReleases(applicationID string, includeArchived bool) ([]*Release, error) {
	args := []any{}
	query := `SELECT id, COALESCE(NULLIF(group_id, ''), id), application_id, COALESCE(project_id, ''), name, created_at, updated_at, archived_at FROM releases`
	filters := []string{}
	if applicationID != "" {
		filters = append(filters, `application_id = ?`)
		args = append(args, applicationID)
	}
	if !includeArchived {
		filters = append(filters, `archived_at IS NULL`)
	}
	if len(filters) > 0 {
		query += ` WHERE ` + strings.Join(filters, ` AND `)
	}
	query += ` ORDER BY created_at DESC, name ASC`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var releases []*Release
	for rows.Next() {
		var release Release
		if err := rows.Scan(&release.ID, &release.GroupID, &release.ApplicationID, &release.ProjectID, &release.Name, &release.CreatedAt, &release.UpdatedAt, &release.ArchivedAt); err != nil {
			return nil, err
		}
		release.ProjectID = normalizeProjectID(release.ProjectID)
		releases = append(releases, &release)
	}
	return releases, rows.Err()
}

func (s *SQLiteStore) ListReleasesByProject(projectID string, includeArchived bool) ([]*Release, error) {
	projectID = normalizeProjectID(projectID)
	rows, err := s.db.Query(`SELECT id, COALESCE(NULLIF(group_id, ''), id), application_id, COALESCE(project_id, ''), name, created_at, updated_at, archived_at
		FROM releases
		WHERE project_id = ?
		ORDER BY COALESCE(NULLIF(group_id, ''), id) ASC, updated_at DESC, id ASC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type familyState struct {
		release       *Release
		memberCount   int
		archivedCount int
	}
	families := make(map[string]*familyState)
	for rows.Next() {
		var release Release
		if err := rows.Scan(&release.ID, &release.GroupID, &release.ApplicationID, &release.ProjectID, &release.Name, &release.CreatedAt, &release.UpdatedAt, &release.ArchivedAt); err != nil {
			return nil, err
		}
		release.ProjectID = normalizeProjectID(release.ProjectID)
		family := families[release.GroupID]
		if family == nil {
			family = &familyState{release: &Release{
				ID: release.GroupID, GroupID: release.GroupID, ProjectID: release.ProjectID,
				Name: release.Name, CreatedAt: release.CreatedAt, UpdatedAt: release.UpdatedAt,
				ArchivedAt: release.ArchivedAt, MemberIDs: make(map[string]string),
			}}
			families[release.GroupID] = family
		}
		family.memberCount++
		if release.ArchivedAt != nil {
			family.archivedCount++
		}
		family.release.MemberIDs[release.ApplicationID] = release.ID
		if release.CreatedAt.Before(family.release.CreatedAt) {
			family.release.CreatedAt = release.CreatedAt
		}
		// Rows are ordered newest-first within a family, so the first row is the
		// authoritative projection if an older installation ever contains a
		// partially-reconciled family.
		if family.release.ApplicationID == "" {
			family.release.ApplicationID = release.ApplicationID
			family.release.ApplicationReleaseID = release.ID
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	releases := make([]*Release, 0, len(families))
	for _, family := range families {
		if !includeArchived && family.archivedCount == family.memberCount {
			continue
		}
		if family.archivedCount != family.memberCount {
			family.release.ArchivedAt = nil
		}
		releases = append(releases, family.release)
	}
	sort.Slice(releases, func(i, j int) bool {
		if releases[i].CreatedAt.Equal(releases[j].CreatedAt) {
			return releases[i].Name < releases[j].Name
		}
		return releases[i].CreatedAt.After(releases[j].CreatedAt)
	})
	return releases, nil
}

// ListReleaseFamilyMembers resolves either a physical release id or a canonical
// family id and returns every application-owned member in deterministic order.
// It is intentionally optional on Store so lightweight API test stores retain
// their exact-id behavior while SQL-backed controllers get family semantics.
func (s *SQLiteStore) ListReleaseFamilyMembers(id string) ([]*Release, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, errors.New("release id is required")
	}
	rows, err := s.db.Query(`SELECT id, COALESCE(NULLIF(group_id, ''), id), application_id, COALESCE(project_id, ''), name, created_at, updated_at, archived_at
		FROM releases
		WHERE COALESCE(NULLIF(group_id, ''), id) = (
			SELECT COALESCE(NULLIF(group_id, ''), id)
			FROM releases
			WHERE id = ? OR COALESCE(NULLIF(group_id, ''), id) = ?
			ORDER BY CASE WHEN id = ? THEN 0 ELSE 1 END, application_id ASC, id ASC
			LIMIT 1
		)
		ORDER BY application_id ASC, id ASC`, id, id, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	members := make([]*Release, 0)
	for rows.Next() {
		var release Release
		if err := rows.Scan(&release.ID, &release.GroupID, &release.ApplicationID, &release.ProjectID, &release.Name, &release.CreatedAt, &release.UpdatedAt, &release.ArchivedAt); err != nil {
			return nil, err
		}
		release.ProjectID = normalizeProjectID(release.ProjectID)
		members = append(members, &release)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(members) == 0 {
		return nil, sql.ErrNoRows
	}
	return members, nil
}

// CreateProjectReleaseFamily creates one application-owned release row for
// every application in the project and returns the row for the preferred
// application. The shared GroupID lets project-level updates remain singular
// while run/test placement continues to reference an application-local row.
func (s *SQLiteStore) CreateProjectReleaseFamily(projectID, name, preferredApplicationID string, createdAt time.Time) (*Release, error) {
	projectID = normalizeProjectID(projectID)
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("release name is required")
	}
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := lockRequiredPlacementParent(tx, `UPDATE projects SET updated_at = updated_at WHERE id = ?`, projectID, "release family", "project"); err != nil {
		return nil, err
	}
	rows, err := tx.Query(`SELECT id FROM applications WHERE project_id = ? ORDER BY name ASC, id ASC`, projectID)
	if err != nil {
		return nil, err
	}
	applicationIDs := make([]string, 0)
	for rows.Next() {
		var applicationID string
		if err := rows.Scan(&applicationID); err != nil {
			rows.Close()
			return nil, err
		}
		applicationIDs = append(applicationIDs, applicationID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(applicationIDs) == 0 {
		return nil, errors.New("project has no applications")
	}
	selectedApplicationID := applicationIDs[0]
	if preferredApplicationID != "" {
		selectedApplicationID = ""
		for _, applicationID := range applicationIDs {
			if applicationID == preferredApplicationID {
				selectedApplicationID = applicationID
				break
			}
		}
		if selectedApplicationID == "" {
			return nil, errors.New("application does not belong to project")
		}
	}
	groupID := uuid.NewString()
	var selected *Release
	for _, applicationID := range applicationIDs {
		release := &Release{
			ID: uuid.NewString(), GroupID: groupID, ApplicationID: applicationID,
			ProjectID: projectID, Name: name, CreatedAt: createdAt, UpdatedAt: createdAt,
		}
		if _, err := tx.Exec(`INSERT INTO releases (id, group_id, application_id, project_id, name, created_at, updated_at, archived_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, NULL)`, release.ID, release.GroupID, release.ApplicationID, release.ProjectID, release.Name, release.CreatedAt, release.UpdatedAt); err != nil {
			return nil, err
		}
		if applicationID == selectedApplicationID {
			selected = release
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return selected, nil
}

func (s *SQLiteStore) ArchiveRelease(id string) error {
	id = strings.TrimSpace(id)
	if id == LegacyReleaseID {
		return errors.New("legacy release cannot be archived")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	projectID, groupID, err := resolveReleaseFamilyIdentityTx(tx, id)
	if err != nil {
		return err
	}
	if id == legacyReleaseID(projectID) || groupID == legacyReleaseID(projectID) {
		return errors.New("legacy release cannot be archived")
	}
	// Family mutations use the same project -> application -> release hierarchy
	// as child creation. Lock every member before checking dependencies so a
	// concurrent writer cannot add a test or schedule behind the snapshot.
	if err := lockRequiredPlacementParent(tx,
		`UPDATE projects SET updated_at = updated_at WHERE id = ?`, projectID, "archive release", "project"); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE applications SET updated_at = updated_at WHERE project_id = ?`, projectID); err != nil {
		return err
	}
	locked, err := tx.Exec(`UPDATE releases SET updated_at = updated_at
		WHERE project_id = ? AND COALESCE(NULLIF(group_id, ''), id) = ?`, projectID, groupID)
	if err != nil {
		return err
	}
	affected, err := locked.RowsAffected()
	if err != nil {
		return err
	}
	if affected < 1 {
		return sql.ErrNoRows
	}
	var count int
	if err := tx.QueryRow(`SELECT
		(SELECT COUNT(*) FROM tests WHERE release_id IN (SELECT id FROM releases WHERE project_id = ? AND COALESCE(NULLIF(group_id, ''), id) = ?)) +
		(SELECT COUNT(*) FROM external_result_imports WHERE release_id IN (SELECT id FROM releases WHERE project_id = ? AND COALESCE(NULLIF(group_id, ''), id) = ?)) +
		(SELECT COUNT(*) FROM scenario_schedules WHERE release_id IN (SELECT id FROM releases WHERE project_id = ? AND COALESCE(NULLIF(group_id, ''), id) = ?))`,
		projectID, groupID, projectID, groupID, projectID, groupID).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return errors.New("release has associated results or schedules; remove those references first")
	}
	now := time.Now()
	if _, err := tx.Exec(`UPDATE releases SET archived_at = ?, updated_at = ? WHERE project_id = ? AND COALESCE(NULLIF(group_id, ''), id) = ?`, now, now, projectID, groupID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) DeleteRelease(id string) error {
	id = strings.TrimSpace(id)
	if id == LegacyReleaseID {
		return errors.New("legacy release cannot be deleted")
	}
	if id == "" {
		return errors.New("release id is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	projectID, groupID, err := resolveReleaseFamilyIdentityTx(tx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	projectID = normalizeProjectID(projectID)
	if id == legacyReleaseID(projectID) || groupID == legacyReleaseID(projectID) {
		return errors.New("legacy release cannot be deleted")
	}
	// Use the same project -> application -> release order as SaveRelease and
	// child writers. Once the release row is held, every reference check and the
	// delete occur in this transaction.
	if _, err := tx.Exec(`UPDATE projects SET updated_at = updated_at WHERE id = ?`, projectID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE applications SET updated_at = updated_at WHERE project_id = ?`, projectID); err != nil {
		return err
	}
	locked, err := tx.Exec(`UPDATE releases SET updated_at = updated_at
		WHERE project_id = ? AND COALESCE(NULLIF(group_id, ''), id) = ?`, projectID, groupID)
	if err != nil {
		return err
	}
	if affected, err := locked.RowsAffected(); err != nil {
		return err
	} else if affected < 1 {
		return sql.ErrNoRows
	}
	var dependentCount int
	if err := tx.QueryRow(`SELECT
		(SELECT COUNT(*) FROM tests WHERE release_id IN (SELECT id FROM releases WHERE project_id = ? AND COALESCE(NULLIF(group_id, ''), id) = ?)) +
		(SELECT COUNT(*) FROM external_result_imports WHERE release_id IN (SELECT id FROM releases WHERE project_id = ? AND COALESCE(NULLIF(group_id, ''), id) = ?)) +
		(SELECT COUNT(*) FROM scenario_schedules WHERE release_id IN (SELECT id FROM releases WHERE project_id = ? AND COALESCE(NULLIF(group_id, ''), id) = ?)) +
		(SELECT COUNT(*) FROM release_sla_waivers WHERE release_id IN (SELECT id FROM releases WHERE project_id = ? AND COALESCE(NULLIF(group_id, ''), id) = ?))`,
		projectID, groupID, projectID, groupID, projectID, groupID, projectID, groupID).Scan(&dependentCount); err != nil {
		return err
	}
	if dependentCount > 0 {
		return errors.New("release has associated results, schedules, or governance records")
	}
	if _, err := tx.Exec(`DELETE FROM releases WHERE project_id = ? AND COALESCE(NULLIF(group_id, ''), id) = ?`, projectID, groupID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) SaveProject(project *Project) error {
	return s.saveProject(project, false)
}

// CreateProjectWithLegacyHierarchy creates a project and its deterministic
// compatibility application/release in one transaction. API project creation
// uses this path; SaveProject remains suitable for migrations and fixtures that
// intentionally assemble a hierarchy in separate steps.
func (s *SQLiteStore) CreateProjectWithLegacyHierarchy(project *Project) error {
	return s.saveProject(project, true)
}

func (s *SQLiteStore) saveProject(project *Project, provisionLegacyHierarchy bool) error {
	if project.ID == "" {
		return errors.New("project id is required")
	}
	if strings.TrimSpace(project.Name) == "" {
		return errors.New("project name is required")
	}
	project.DomainID = normalizeDomainID(project.DomainID)
	project.CategoryID = normalizeRepositoryCategoryID(project.CategoryID)
	if project.CreatedAt.IsZero() {
		project.CreatedAt = time.Now()
	}
	project.UpdatedAt = time.Now()
	settingsJSON, err := json.Marshal(project.Settings)
	if err != nil {
		return fmt.Errorf("marshal project settings: %w", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockProjectPlacement(tx, project.DomainID, project.CategoryID); err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO projects (id, domain_id, category_id, name, settings_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET domain_id=excluded.domain_id, category_id=excluded.category_id, name=excluded.name, settings_json=excluded.settings_json, updated_at=excluded.updated_at`,
		project.ID, project.DomainID, project.CategoryID, project.Name, string(settingsJSON), project.CreatedAt, project.UpdatedAt)
	if err != nil {
		return err
	}
	if provisionLegacyHierarchy {
		if err := ensureLegacyProjectHierarchyTx(tx, project.ID, project.CreatedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UpdateProjectIfUnchanged atomically applies an administrator edit only when
// no concurrent writer changed placement, quotas, or other project settings.
func (s *SQLiteStore) UpdateProjectIfUnchanged(project *Project, expectedUpdatedAt time.Time) (bool, error) {
	if project == nil || strings.TrimSpace(project.ID) == "" {
		return false, errors.New("project id is required")
	}
	if strings.TrimSpace(project.Name) == "" {
		return false, errors.New("project name is required")
	}
	if expectedUpdatedAt.IsZero() {
		return false, errors.New("expected project updated_at is required")
	}
	project.DomainID = normalizeDomainID(project.DomainID)
	project.CategoryID = normalizeRepositoryCategoryID(project.CategoryID)
	settingsJSON, err := json.Marshal(project.Settings)
	if err != nil {
		return false, fmt.Errorf("marshal project settings: %w", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if err := lockProjectPlacement(tx, project.DomainID, project.CategoryID); err != nil {
		return false, err
	}
	var currentUpdatedAt time.Time
	var currentUpdatedRaw string
	if err := tx.QueryRow(`SELECT updated_at, CAST(updated_at AS TEXT) FROM projects WHERE id = ?`, project.ID).Scan(&currentUpdatedAt, &currentUpdatedRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if !currentUpdatedAt.Equal(expectedUpdatedAt) {
		return false, nil
	}
	project.UpdatedAt = time.Now()
	result, err := tx.Exec(`UPDATE projects SET domain_id = ?, category_id = ?, name = ?, settings_json = ?, updated_at = ?
		WHERE id = ? AND CAST(updated_at AS TEXT) = ?`, project.DomainID, project.CategoryID, project.Name, string(settingsJSON), project.UpdatedAt, project.ID, currentUpdatedRaw)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return false, err
	}
	return true, tx.Commit()
}

func (s *SQLiteStore) GetProject(id string) (*Project, error) {
	var project Project
	var settingsJSON string
	err := s.db.QueryRow(`SELECT id, COALESCE(domain_id, ''), COALESCE(category_id, ''), name, COALESCE(settings_json, ''), created_at, updated_at FROM projects WHERE id = ?`, id).Scan(
		&project.ID, &project.DomainID, &project.CategoryID, &project.Name, &settingsJSON, &project.CreatedAt, &project.UpdatedAt)
	if err != nil {
		return nil, err
	}
	project.DomainID = normalizeDomainID(project.DomainID)
	project.CategoryID = normalizeRepositoryCategoryID(project.CategoryID)
	if settingsJSON != "" && settingsJSON != "null" {
		if err := json.Unmarshal([]byte(settingsJSON), &project.Settings); err != nil {
			return nil, fmt.Errorf("unmarshal project settings: %w", err)
		}
	}
	return &project, nil
}

func (s *SQLiteStore) ListProjects() ([]*Project, error) {
	rows, err := s.db.Query(`SELECT id, COALESCE(domain_id, ''), COALESCE(category_id, ''), name, COALESCE(settings_json, ''), created_at, updated_at FROM projects ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var projects []*Project
	for rows.Next() {
		var project Project
		var settingsJSON string
		if err := rows.Scan(&project.ID, &project.DomainID, &project.CategoryID, &project.Name, &settingsJSON, &project.CreatedAt, &project.UpdatedAt); err != nil {
			return nil, err
		}
		project.DomainID = normalizeDomainID(project.DomainID)
		project.CategoryID = normalizeRepositoryCategoryID(project.CategoryID)
		if settingsJSON != "" && settingsJSON != "null" {
			if err := json.Unmarshal([]byte(settingsJSON), &project.Settings); err != nil {
				return nil, fmt.Errorf("unmarshal project settings: %w", err)
			}
		}
		projects = append(projects, &project)
	}
	return projects, rows.Err()
}

func lockProjectDeletionRows(tx *storeTx, dialect storeDialect, id string, rejectActive bool) (LeaseFencedDeleteResult, error) {
	projectQuery := `SELECT id FROM projects WHERE id = ?`
	if dialect == storeDialectPostgres {
		projectQuery += ` FOR UPDATE`
	}
	var projectID string
	if err := tx.QueryRow(projectQuery, id).Scan(&projectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return LeaseFencedDeleteMissing, nil
		}
		return LeaseFencedDeleteFenced, err
	}

	// Lock every project test in deterministic order before deciding whether the
	// graph is safe to delete. SaveTestIfLeaseHeld takes the execution lease first,
	// so the fenced caller uses the same lock order and cannot deadlock a takeover.
	lockQuery := `SELECT id, COALESCE(status, '') FROM tests WHERE project_id = ? ORDER BY id`
	if dialect == storeDialectPostgres {
		lockQuery += ` FOR UPDATE`
	}
	lockedRows, err := tx.Query(lockQuery, id)
	if err != nil {
		return LeaseFencedDeleteFenced, err
	}
	defer lockedRows.Close()
	for lockedRows.Next() {
		var testID, status string
		if err := lockedRows.Scan(&testID, &status); err != nil {
			return LeaseFencedDeleteFenced, err
		}
		if rejectActive && activeTestStatusForDeletion(status) {
			return LeaseFencedDeleteActive, nil
		}
	}
	if err := lockedRows.Err(); err != nil {
		return LeaseFencedDeleteFenced, err
	}
	return LeaseFencedDeleteDeleted, nil
}

func (s *SQLiteStore) DeleteProject(id string) error {
	id = normalizeProjectID(id)
	if id == DefaultProjectID {
		return errors.New("default project cannot be deleted")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := lockProjectDeletionRows(tx, s.db.dialect, id, false)
	if err != nil {
		return err
	}
	if result == LeaseFencedDeleteMissing {
		return sql.ErrNoRows
	}
	if err := deleteProjectGraphTx(tx, id); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteProjectIfLeaseHeld deletes a project graph only while leaseOwnerID
// still owns the unexpired execution lease and no durable project test is
// active. All predicates and deletions share one transaction.
func (s *SQLiteStore) DeleteProjectIfLeaseHeld(id, leaseName, leaseOwnerID string) (LeaseFencedDeleteResult, error) {
	id = normalizeProjectID(id)
	if id == DefaultProjectID {
		return LeaseFencedDeleteFenced, errors.New("default project cannot be deleted")
	}
	leaseName, leaseOwnerID, err := validateLeaseFencedDelete(leaseName, leaseOwnerID)
	if err != nil {
		return LeaseFencedDeleteFenced, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return LeaseFencedDeleteFenced, err
	}
	defer tx.Rollback()
	held, err := schedulerLeaseHeldInTransaction(tx, s.db.dialect, leaseName, leaseOwnerID)
	if err != nil {
		return LeaseFencedDeleteFenced, err
	}
	if !held {
		return LeaseFencedDeleteFenced, nil
	}
	result, err := lockProjectDeletionRows(tx, s.db.dialect, id, true)
	if err != nil || result != LeaseFencedDeleteDeleted {
		return result, err
	}
	if err := deleteProjectGraphTx(tx, id); err != nil {
		return LeaseFencedDeleteFenced, err
	}
	if err := tx.Commit(); err != nil {
		return LeaseFencedDeleteFenced, err
	}
	return LeaseFencedDeleteDeleted, nil
}

func deleteProjectGraphTx(tx *storeTx, id string) error {
	if err := rejectActiveGitHubProjectSyncClaimsTx(tx, id); err != nil {
		return err
	}
	archiveRecords, err := listProjectRunArchivesTx(tx, id)
	if err != nil {
		return err
	}
	cataloged := make(map[string]*RunArchiveRecord, len(archiveRecords))
	for _, record := range archiveRecords {
		if err := enqueueArchiveCleanupTx(tx, id, record); err != nil {
			return err
		}
		cataloged[record.TestID] = record
	}
	archiveFinalizationJobs, err := listProjectArchiveFinalizationJobsTx(tx, id)
	if err != nil {
		return err
	}
	for _, job := range archiveFinalizationJobs {
		if job == nil {
			continue
		}
		if published, ok := cataloged[job.TestID]; ok {
			// Same intersection as deleteTestGraphTx: a refresh already pointed
			// the intent at a new object while the catalog still names the old
			// one. Queue the divergent object too instead of dropping it with
			// the intent row.
			if err := enqueuePendingArchiveObjectCleanupTx(tx, id, published, job); err != nil {
				return err
			}
			continue
		}
		if err := enqueueArchiveCleanupTx(tx, id, &job.ArchiveRecord); err != nil {
			return err
		}
	}
	externalArchives, err := listProjectExternalResultArchivesTx(tx, id)
	if err != nil {
		return err
	}
	for _, record := range externalArchives {
		// Imported results are publications, not test-owned singleton archives:
		// several imports can legitimately contain the same source TestID. Key
		// cleanup by immutable publication identity instead of source TestID.
		if err := enqueueArchiveVersionCleanupTx(tx, id, record); err != nil {
			return err
		}
	}

	// Remove embedded service-account grants in the same transaction as the
	// project and role-binding rows. These grants are JSON rather than rows, so
	// a simple DELETE by project_id cannot cover them.
	if err := deleteProjectServiceAccountGrants(tx, id); err != nil {
		return err
	}

	// Scenario ownership is derived from its scripts. Select only scenarios
	// whose scripts all belong to this project, avoiding deletion of malformed
	// cross-project scenarios while still removing the normal project-owned case.
	scenarioSubquery := `SELECT DISTINCT ss.scenario_id
		FROM scenario_scripts ss
		JOIN scripts sc ON sc.id = ss.script_id
		WHERE sc.project_id = ?
		AND NOT EXISTS (
			SELECT 1 FROM scenario_scripts other_ss
			JOIN scripts other_sc ON other_sc.id = other_ss.script_id
			WHERE other_ss.scenario_id = ss.scenario_id AND other_sc.project_id <> ?
		)`
	ownedScenarioRows, err := tx.Query(scenarioSubquery, id, id)
	if err != nil {
		return err
	}
	ownedScenarioIDs := []string{}
	for ownedScenarioRows.Next() {
		var scenarioID string
		if err := ownedScenarioRows.Scan(&scenarioID); err != nil {
			ownedScenarioRows.Close()
			return err
		}
		ownedScenarioIDs = append(ownedScenarioIDs, scenarioID)
	}
	if err := ownedScenarioRows.Err(); err != nil {
		ownedScenarioRows.Close()
		return err
	}
	ownedScenarioRows.Close()

	type projectDelete struct {
		query string
		args  []any
	}
	deletes := []projectDelete{
		// Children that can reference tests must be removed before tests.
		{`UPDATE capacity_reservations SET bound_test_id = NULL, bound_test_status = NULL, updated_at = ? WHERE bound_test_id IN (SELECT id FROM tests WHERE project_id = ?)`, []any{time.Now().UTC(), id}},
		{`DELETE FROM github_check_intents WHERE project_id = ?`, []any{id}},
		{`DELETE FROM powerbi_publishes WHERE project_id = ? OR test_id IN (SELECT id FROM tests WHERE project_id = ?)`, []any{id, id}},
		{`DELETE FROM app_settings WHERE EXISTS (
			SELECT 1 FROM tests t WHERE t.project_id = ?
			AND SUBSTR(app_settings.key, 1, LENGTH('report:annotations:' || t.id || ':')) = 'report:annotations:' || t.id || ':'
		)`, []any{id}},
		{`DELETE FROM github_check_links WHERE test_id IN (SELECT id FROM tests WHERE project_id = ?) OR script_id IN (SELECT id FROM scripts WHERE project_id = ?)`, []any{id, id}},
		{`DELETE FROM test_history WHERE test_id IN (SELECT id FROM tests WHERE project_id = ?)`, []any{id}},
		{`DELETE FROM test_notes WHERE test_id IN (SELECT id FROM tests WHERE project_id = ?)`, []any{id}},
		{`DELETE FROM test_interval_summaries WHERE test_id IN (SELECT id FROM tests WHERE project_id = ?)`, []any{id}},
		{`DELETE FROM grafana_dashboard_snapshots WHERE project_id = ? OR test_id IN (SELECT id FROM tests WHERE project_id = ?)`, []any{id, id}},
		{`DELETE FROM run_archives WHERE test_id IN (SELECT id FROM tests WHERE project_id = ?)`, []any{id}},
		{`DELETE FROM archive_finalization_jobs WHERE project_id = ? OR test_id IN (SELECT id FROM tests WHERE project_id = ?)`, []any{id, id}},
		{`DELETE FROM archive_refresh_jobs WHERE project_id = ? OR test_id IN (SELECT id FROM tests WHERE project_id = ?)`, []any{id, id}},
		{`UPDATE scenario_schedules SET last_run_test_id = NULL WHERE last_run_test_id IN (SELECT id FROM tests WHERE project_id = ?)`, []any{id}},
		{`DELETE FROM scenario_schedule_occurrences WHERE project_id = ? OR test_id IN (SELECT id FROM tests WHERE project_id = ?) OR schedule_id IN (SELECT id FROM scenario_schedules WHERE scenario_id IN (` + scenarioSubquery + `))`, []any{id, id, id, id}},
		{`DELETE FROM tests WHERE project_id = ?`, []any{id}},

		// Recurring scenario governance and the scenario itself.
		{`DELETE FROM scenario_schedule_approvals WHERE project_id = ? OR schedule_id IN (SELECT id FROM scenario_schedules WHERE scenario_id IN (` + scenarioSubquery + `))`, []any{id, id, id}},
		{`DELETE FROM scenario_schedules WHERE project_id = ? OR scenario_id IN (` + scenarioSubquery + `)`, []any{id, id, id}},
		{`DELETE FROM scenario_data_pool_bindings WHERE scenario_id IN (` + scenarioSubquery + `) OR pool_id IN (SELECT id FROM shared_data_pools WHERE project_id = ?)`, []any{id, id, id}},
		{`DELETE FROM scenario_scripts WHERE scenario_id IN (` + scenarioSubquery + `)`, []any{id, id}},

		// Shared data and authoring assets.
		{`DELETE FROM shared_data_pool_rows WHERE pool_id IN (SELECT id FROM shared_data_pools WHERE project_id = ?)`, []any{id}},
		{`DELETE FROM shared_data_pools WHERE project_id = ?`, []any{id}},
		{`DELETE FROM authoring_sessions WHERE project_id = ?`, []any{id}},
		{`DELETE FROM reusable_blocks WHERE project_id = ?`, []any{id}},
		{`DELETE FROM object_repositories WHERE project_id = ?`, []any{id}},
		{`DELETE FROM correlation_rulesets WHERE project_id = ?`, []any{id}},
		{`DELETE FROM monitoring_collectors WHERE project_id = ?`, []any{id}},
		{`DELETE FROM grafana_dashboards WHERE project_id = ?`, []any{id}},
		{`DELETE FROM grafana_import_sessions WHERE project_id = ?`, []any{id}},

		// Execution-plan schedules, approvals, occurrences, and run state.
		{`DELETE FROM execution_plan_schedule_occurrences WHERE project_id = ? OR schedule_id IN (SELECT id FROM execution_plan_schedules WHERE project_id = ? OR plan_id IN (SELECT id FROM execution_plans WHERE project_id = ?))`, []any{id, id, id}},
		{`DELETE FROM execution_plan_schedules WHERE project_id = ? OR plan_id IN (SELECT id FROM execution_plans WHERE project_id = ?)`, []any{id, id}},
		{`DELETE FROM execution_plan_approvals WHERE project_id = ? OR plan_id IN (SELECT id FROM execution_plans WHERE project_id = ?)`, []any{id, id}},
		{`DELETE FROM execution_plan_runs WHERE project_id = ? OR plan_id IN (SELECT id FROM execution_plans WHERE project_id = ?)`, []any{id, id}},
		{`DELETE FROM execution_plans WHERE project_id = ?`, []any{id}},
		{`DELETE FROM capacity_reservations WHERE project_id = ?`, []any{id}},

		// Imports, accounting, fleet requests, and repository integration state.
		{`DELETE FROM provisioning_events WHERE request_id IN (SELECT id FROM provisioning_requests WHERE project_id = ?)`, []any{id}},
		{`DELETE FROM provisioning_requests WHERE project_id = ?`, []any{id}},
		{`DELETE FROM github_sync_jobs WHERE project_id = ?`, []any{id}},
		{`DELETE FROM external_result_imports WHERE project_id = ?`, []any{id}},
		{`DELETE FROM direct_import_receipts WHERE project_id = ?`, []any{id}},
		{`DELETE FROM usage_accounting_snapshots WHERE project_id = ?`, []any{id}},
		// Audit history is intentionally retained after project deletion. It is an
		// immutable governance trail, not project-owned mutable content.

		// Application/release hierarchy and its governance records.
		{`DELETE FROM release_sla_waivers WHERE project_id = ? OR release_id IN (SELECT id FROM releases WHERE project_id = ? OR application_id IN (SELECT id FROM applications WHERE project_id = ?))`, []any{id, id, id}},
		{`DELETE FROM releases WHERE project_id = ? OR application_id IN (SELECT id FROM applications WHERE project_id = ?)`, []any{id, id}},
		{`DELETE FROM applications WHERE project_id = ?`, []any{id}},

		// Script-derived sensitive records and project repository contents.
		{`DELETE FROM github_script_sync_target_claims WHERE project_id = ? OR script_id IN (SELECT id FROM scripts WHERE project_id = ?)`, []any{id, id}},
		{`DELETE FROM github_script_sync_claims WHERE project_id = ? OR script_id IN (SELECT id FROM scripts WHERE project_id = ?)`, []any{id, id}},
		{`DELETE FROM github_script_sync_target_owners WHERE project_id = ? OR script_id IN (SELECT id FROM scripts WHERE project_id = ?)`, []any{id, id}},
		{`DELETE FROM github_script_links WHERE script_id IN (SELECT id FROM scripts WHERE project_id = ?)`, []any{id}},
		{`DELETE FROM script_recordings WHERE script_id IN (SELECT id FROM scripts WHERE project_id = ?)`, []any{id}},
		{`DELETE FROM bruno_source_revisions WHERE project_id = ?`, []any{id}},
		{`DELETE FROM bruno_native_import_batches WHERE project_id = ?`, []any{id}},
		{`DELETE FROM bruno_source_artifacts WHERE project_id = ?`, []any{id}},
		{`DELETE FROM protected_source_secret_sessions WHERE project_id = ?`, []any{id}},
		{`DELETE FROM runtime_secret_provisioning_attempts WHERE project_id = ?`, []any{id}},
		{`DELETE FROM project_secrets WHERE project_id = ?`, []any{id}},
		{`DELETE FROM project_secret_provider_metadata WHERE project_id = ?`, []any{id}},
		{`DELETE FROM scripts WHERE project_id = ?`, []any{id}},
		{`DELETE FROM folders WHERE project_id = ?`, []any{id}},
		{`DELETE FROM user_role_bindings WHERE scope_type = 'project' AND scope_id = ?`, []any{id}},
		{`DELETE FROM projects WHERE id = ?`, []any{id}},
	}
	for _, deletion := range deletes {
		if _, err := tx.Exec(deletion.query, deletion.args...); err != nil {
			return err
		}
	}
	for _, scenarioID := range ownedScenarioIDs {
		if _, err := tx.Exec(`DELETE FROM scenarios WHERE id = ?`, scenarioID); err != nil {
			return err
		}
	}
	return nil
}

func deleteProjectServiceAccountGrants(tx *storeTx, projectID string) error {
	rows, err := tx.Query(`SELECT id, COALESCE(project_roles_json, '') FROM service_accounts ORDER BY id`)
	if err != nil {
		return err
	}
	type update struct {
		id    string
		roles string
	}
	updates := []update{}
	for rows.Next() {
		var id, encoded string
		if err := rows.Scan(&id, &encoded); err != nil {
			rows.Close()
			return err
		}
		roles := map[string][]string{}
		if strings.TrimSpace(encoded) != "" && strings.TrimSpace(encoded) != "null" {
			if err := json.Unmarshal([]byte(encoded), &roles); err != nil {
				rows.Close()
				return fmt.Errorf("decode service account %s project grants: %w", id, err)
			}
		}
		if _, exists := roles[projectID]; !exists {
			continue
		}
		delete(roles, projectID)
		data, err := json.Marshal(roles)
		if err != nil {
			rows.Close()
			return err
		}
		updates = append(updates, update{id: id, roles: string(data)})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, update := range updates {
		if _, err := tx.Exec(`UPDATE service_accounts SET project_roles_json = ?, updated_at = ? WHERE id = ?`, update.roles, time.Now().UTC(), update.id); err != nil {
			return err
		}
	}
	return nil
}

func deleteDomainServiceAccountGrants(tx *storeTx, domainID, repositoryID string) error {
	rows, err := tx.Query(`SELECT id, COALESCE(domain_roles_json, ''), COALESCE(repository_roles_json, '') FROM service_accounts ORDER BY id`)
	if err != nil {
		return err
	}
	type update struct {
		id              string
		domainRoles     string
		repositoryRoles string
	}
	updates := []update{}
	for rows.Next() {
		var id, encodedDomains, encodedRepositories string
		if err := rows.Scan(&id, &encodedDomains, &encodedRepositories); err != nil {
			rows.Close()
			return err
		}
		domainRoles := map[string][]string{}
		if strings.TrimSpace(encodedDomains) != "" && strings.TrimSpace(encodedDomains) != "null" {
			if err := json.Unmarshal([]byte(encodedDomains), &domainRoles); err != nil {
				rows.Close()
				return fmt.Errorf("decode service account %s domain grants: %w", id, err)
			}
		}
		repositoryRoles := map[string][]string{}
		if strings.TrimSpace(encodedRepositories) != "" && strings.TrimSpace(encodedRepositories) != "null" {
			if err := json.Unmarshal([]byte(encodedRepositories), &repositoryRoles); err != nil {
				rows.Close()
				return fmt.Errorf("decode service account %s repository grants: %w", id, err)
			}
		}
		_, domainGrant := domainRoles[domainID]
		_, repositoryGrant := repositoryRoles[repositoryID]
		if !domainGrant && (!repositoryGrant || strings.TrimSpace(repositoryID) == "") {
			continue
		}
		delete(domainRoles, domainID)
		if strings.TrimSpace(repositoryID) != "" {
			delete(repositoryRoles, repositoryID)
		}
		domainData, err := json.Marshal(domainRoles)
		if err != nil {
			rows.Close()
			return err
		}
		repositoryData, err := json.Marshal(repositoryRoles)
		if err != nil {
			rows.Close()
			return err
		}
		updates = append(updates, update{id: id, domainRoles: string(domainData), repositoryRoles: string(repositoryData)})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, update := range updates {
		if _, err := tx.Exec(`UPDATE service_accounts SET domain_roles_json = ?, repository_roles_json = ?, updated_at = ? WHERE id = ?`,
			update.domainRoles, update.repositoryRoles, time.Now().UTC(), update.id); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) SaveFolder(folder *Folder) error {
	if folder == nil {
		return errors.New("folder is required")
	}
	if folder.ID == "" {
		return errors.New("folder id is required")
	}
	if strings.TrimSpace(folder.Name) == "" {
		return errors.New("folder name is required")
	}
	if err := validateFolderCategory(folder.Category); err != nil {
		return err
	}
	folder.ProjectID = normalizeProjectID(strings.TrimSpace(folder.ProjectID))
	if folder.ParentID != nil {
		parentID := strings.TrimSpace(*folder.ParentID)
		folder.ParentID = &parentID
	}
	if folder.CreatedAt.IsZero() {
		folder.CreatedAt = time.Now()
	}
	folder.UpdatedAt = time.Now()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockRequiredPlacementParent(tx,
		`UPDATE projects SET updated_at = updated_at WHERE id = ?`, folder.ProjectID, "folder", "project"); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE folders SET updated_at = updated_at WHERE id = ?`, folder.ID); err != nil {
		return err
	}

	var existingProjectID string
	err = tx.QueryRow(`SELECT project_id FROM folders WHERE id = ?`, folder.ID).Scan(&existingProjectID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && normalizeProjectID(existingProjectID) != folder.ProjectID {
		return fmt.Errorf("folder %q belongs to another project", folder.ID)
	}
	if err := validateFolderParent(tx, folder.ID, folder.ProjectID, folder.ParentID); err != nil {
		return err
	}

	result, err := tx.Exec(`INSERT INTO folders (id, project_id, parent_id, name, category, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET parent_id=excluded.parent_id, name=excluded.name, category=excluded.category, updated_at=excluded.updated_at
		WHERE folders.project_id = excluded.project_id`,
		folder.ID, folder.ProjectID, folder.ParentID, folder.Name, folder.Category, folder.CreatedAt, folder.UpdatedAt)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return err
	} else if affected == 0 {
		return fmt.Errorf("folder %q changed concurrently", folder.ID)
	}
	return tx.Commit()
}

// UpdateFolderIfUnchanged validates and applies a complete folder edit in one
// transaction while rejecting stale snapshots and preserving project ownership.
func (s *SQLiteStore) UpdateFolderIfUnchanged(folder *Folder, expectedUpdatedAt time.Time) (bool, error) {
	if folder == nil || strings.TrimSpace(folder.ID) == "" {
		return false, errors.New("folder id is required")
	}
	if strings.TrimSpace(folder.Name) == "" {
		return false, errors.New("folder name is required")
	}
	if err := validateFolderCategory(folder.Category); err != nil {
		return false, err
	}
	if expectedUpdatedAt.IsZero() {
		return false, errors.New("expected folder updated_at is required")
	}
	if folder.ParentID != nil {
		parentID := strings.TrimSpace(*folder.ParentID)
		folder.ParentID = &parentID
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var placementProjectID string
	if err := tx.QueryRow(`SELECT project_id FROM folders WHERE id = ?`, folder.ID).Scan(&placementProjectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	placementProjectID = normalizeProjectID(strings.TrimSpace(placementProjectID))
	if err := lockRequiredPlacementParent(tx,
		`UPDATE projects SET updated_at = updated_at WHERE id = ?`, placementProjectID, "folder", "project"); err != nil {
		return false, err
	}
	if err := lockRequiredPlacementParent(tx,
		`UPDATE folders SET updated_at = updated_at WHERE id = ?`, folder.ID, "folder", "folder"); err != nil {
		return false, err
	}
	var currentProjectID string
	var currentUpdatedAt time.Time
	var currentUpdatedRaw string
	if err := tx.QueryRow(`SELECT project_id, updated_at, CAST(updated_at AS TEXT) FROM folders WHERE id = ?`, folder.ID).Scan(&currentProjectID, &currentUpdatedAt, &currentUpdatedRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if normalizeProjectID(strings.TrimSpace(currentProjectID)) != normalizeProjectID(strings.TrimSpace(folder.ProjectID)) || !currentUpdatedAt.Equal(expectedUpdatedAt) {
		return false, nil
	}
	folder.ProjectID = normalizeProjectID(currentProjectID)
	if err := validateFolderParent(tx, folder.ID, folder.ProjectID, folder.ParentID); err != nil {
		return false, err
	}
	folder.UpdatedAt = time.Now()
	result, err := tx.Exec(`UPDATE folders SET parent_id = ?, name = ?, category = ?, updated_at = ?
		WHERE id = ? AND project_id = ? AND CAST(updated_at AS TEXT) = ?`, folder.ParentID, folder.Name, folder.Category, folder.UpdatedAt, folder.ID, folder.ProjectID, currentUpdatedRaw)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return false, err
	}
	return true, tx.Commit()
}

func (s *SQLiteStore) GetFolder(id string) (*Folder, error) {
	var folder Folder
	err := s.db.QueryRow(`SELECT id, project_id, parent_id, name, category, created_at, updated_at FROM folders WHERE id = ?`, id).Scan(
		&folder.ID, &folder.ProjectID, &folder.ParentID, &folder.Name, &folder.Category, &folder.CreatedAt, &folder.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &folder, nil
}

func (s *SQLiteStore) ListFolders(projectID string, category string) ([]*Folder, error) {
	projectID = normalizeProjectID(projectID)
	args := []any{projectID}
	query := `SELECT id, project_id, parent_id, name, category, created_at, updated_at FROM folders WHERE project_id = ?`
	if category != "" && category != "general" {
		if err := validateFolderCategory(category); err != nil {
			return nil, err
		}
		query += ` AND category = ?`
		args = append(args, category)
	}
	query += ` ORDER BY name ASC`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var folders []*Folder
	for rows.Next() {
		var folder Folder
		if err := rows.Scan(&folder.ID, &folder.ProjectID, &folder.ParentID, &folder.Name, &folder.Category, &folder.CreatedAt, &folder.UpdatedAt); err != nil {
			return nil, err
		}
		folders = append(folders, &folder)
	}
	return folders, rows.Err()
}

func (s *SQLiteStore) MoveFolder(id string, newParentID *string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("folder id is required")
	}
	if newParentID != nil {
		parentID := strings.TrimSpace(*newParentID)
		newParentID = &parentID
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var projectID string
	if err := tx.QueryRow(`SELECT project_id FROM folders WHERE id = ?`, id).Scan(&projectID); err != nil {
		return err
	}
	projectID = normalizeProjectID(strings.TrimSpace(projectID))
	if err := lockRequiredPlacementParent(tx,
		`UPDATE projects SET updated_at = updated_at WHERE id = ?`, projectID, "folder move", "project"); err != nil {
		return err
	}
	if err := lockRequiredPlacementParent(tx,
		`UPDATE folders SET updated_at = updated_at WHERE id = ?`, id, "folder move", "folder"); err != nil {
		return err
	}
	if err := validateFolderParent(tx, id, projectID, newParentID); err != nil {
		return err
	}
	result, err := tx.Exec(`UPDATE folders SET parent_id = ?, updated_at = ? WHERE id = ? AND project_id = ?`, newParentID, time.Now(), id, projectID)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return err
	} else if affected == 0 {
		return fmt.Errorf("folder %q changed concurrently", id)
	}
	return tx.Commit()
}

type folderQueryer interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
}

func validateFolderParent(queryer folderQueryer, folderID string, projectID string, parentID *string) error {
	if parentID == nil {
		return nil
	}
	parent := strings.TrimSpace(*parentID)
	if parent == "" {
		return errors.New("parent folder id is required when parent_id is set")
	}
	if parent == folderID {
		return errors.New("folder cannot be moved into itself")
	}
	if err := lockRequiredPlacementParent(queryer,
		`UPDATE folders SET updated_at = updated_at WHERE id = ?`, parent, "folder", "parent folder"); err != nil {
		return err
	}
	var parentProjectID string
	if err := queryer.QueryRow(`SELECT project_id FROM folders WHERE id = ?`, parent).Scan(&parentProjectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("parent folder %q not found", parent)
		}
		return err
	}
	if normalizeProjectID(parentProjectID) != normalizeProjectID(projectID) {
		return errors.New("parent folder belongs to another project")
	}
	isDescendant, err := folderHasAncestor(queryer, parent, folderID)
	if err != nil {
		return err
	}
	if isDescendant {
		return errors.New("folder cannot be moved into its own descendant")
	}
	return nil
}

func folderHasAncestor(queryer folderQueryer, folderID string, ancestorID string) (bool, error) {
	current := folderID
	visited := make(map[string]struct{})
	for current != "" {
		if _, seen := visited[current]; seen {
			return false, errors.New("folder hierarchy contains a cycle")
		}
		visited[current] = struct{}{}
		var parent sql.NullString
		if err := queryer.QueryRow(`SELECT parent_id FROM folders WHERE id = ?`, current).Scan(&parent); err != nil {
			return false, err
		}
		if !parent.Valid {
			return false, nil
		}
		if parent.String == ancestorID {
			return true, nil
		}
		current = parent.String
	}
	return false, nil
}

func (s *SQLiteStore) DeleteFolder(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("folder id is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var projectID string
	if err := tx.QueryRow(`SELECT project_id FROM folders WHERE id = ?`, id).Scan(&projectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	projectID = normalizeProjectID(strings.TrimSpace(projectID))
	if err := lockRequiredPlacementParent(tx,
		`UPDATE projects SET updated_at = updated_at WHERE id = ?`, projectID, "delete folder", "project"); err != nil {
		return err
	}
	if err := lockRequiredPlacementParent(tx,
		`UPDATE folders SET updated_at = updated_at WHERE id = ?`, id, "delete folder", "folder"); err != nil {
		return err
	}
	var childCount, scriptCount, testCount int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM folders WHERE parent_id = ?`, id).Scan(&childCount); err != nil {
		return err
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM scripts WHERE folder_id = ?`, id).Scan(&scriptCount); err != nil {
		return err
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM tests WHERE folder_id = ?`, id).Scan(&testCount); err != nil {
		return err
	}
	if childCount+scriptCount+testCount > 0 {
		return errors.New("folder is not empty")
	}
	if _, err := tx.Exec(`DELETE FROM folders WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}
