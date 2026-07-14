package storage

// SQLite store — projects. Split from sqlite.go (same package, behavior-preserving).

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
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
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM projects WHERE domain_id = ?`, id).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return errors.New("domain has projects")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
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
	_, err := s.db.Exec(`INSERT INTO application_repositories (id, domain_id, name, description, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET domain_id=excluded.domain_id, name=excluded.name, description=excluded.description, updated_at=excluded.updated_at`,
		repository.ID, repository.DomainID, strings.TrimSpace(repository.Name), strings.TrimSpace(repository.Description), repository.CreatedAt, repository.UpdatedAt)
	return err
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
	_, err := s.db.Exec(`INSERT INTO repository_categories (id, domain_id, repository_id, name, description, sort_order, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET domain_id=excluded.domain_id, repository_id=excluded.repository_id, name=excluded.name, description=excluded.description, sort_order=excluded.sort_order, updated_at=excluded.updated_at`,
		category.ID, category.DomainID, category.RepositoryID, strings.TrimSpace(category.Name), strings.TrimSpace(category.Description), category.SortOrder, category.CreatedAt, category.UpdatedAt)
	return err
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
	if application.ID == "" {
		return errors.New("application id is required")
	}
	application.ProjectID = normalizeProjectID(application.ProjectID)
	if strings.TrimSpace(application.Name) == "" {
		return errors.New("application name is required")
	}
	if application.CreatedAt.IsZero() {
		application.CreatedAt = time.Now()
	}
	application.UpdatedAt = time.Now()
	_, err := s.db.Exec(`INSERT INTO applications (id, project_id, name, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET project_id=excluded.project_id, name=excluded.name, updated_at=excluded.updated_at`,
		application.ID, application.ProjectID, application.Name, application.CreatedAt, application.UpdatedAt)
	return err
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
	if id == LegacyApplicationID {
		return errors.New("legacy application cannot be deleted")
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM tests WHERE application_id = ?`, id).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return errors.New("application has test results")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM releases WHERE application_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM applications WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) SaveRelease(release *Release) error {
	if release.ID == "" {
		return errors.New("release id is required")
	}
	release.ApplicationID = normalizeApplicationID(release.ApplicationID)
	if strings.TrimSpace(release.ProjectID) == "" {
		if application, err := s.GetApplication(release.ApplicationID); err == nil && application != nil {
			release.ProjectID = application.ProjectID
		}
	}
	release.ProjectID = normalizeProjectID(release.ProjectID)
	if strings.TrimSpace(release.Name) == "" {
		return errors.New("release name is required")
	}
	if release.CreatedAt.IsZero() {
		release.CreatedAt = time.Now()
	}
	release.UpdatedAt = time.Now()
	_, err := s.db.Exec(`INSERT INTO releases (id, application_id, project_id, name, created_at, updated_at, archived_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET application_id=excluded.application_id, project_id=excluded.project_id, name=excluded.name, updated_at=excluded.updated_at, archived_at=excluded.archived_at`,
		release.ID, release.ApplicationID, release.ProjectID, release.Name, release.CreatedAt, release.UpdatedAt, release.ArchivedAt)
	return err
}

func (s *SQLiteStore) GetRelease(id string) (*Release, error) {
	var release Release
	err := s.db.QueryRow(`SELECT id, application_id, COALESCE(project_id, ''), name, created_at, updated_at, archived_at FROM releases WHERE id = ?`, id).Scan(
		&release.ID, &release.ApplicationID, &release.ProjectID, &release.Name, &release.CreatedAt, &release.UpdatedAt, &release.ArchivedAt)
	if err != nil {
		return nil, err
	}
	release.ProjectID = normalizeProjectID(release.ProjectID)
	return &release, nil
}

func (s *SQLiteStore) ListReleases(applicationID string, includeArchived bool) ([]*Release, error) {
	args := []any{}
	query := `SELECT id, application_id, COALESCE(project_id, ''), name, created_at, updated_at, archived_at FROM releases`
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
		if err := rows.Scan(&release.ID, &release.ApplicationID, &release.ProjectID, &release.Name, &release.CreatedAt, &release.UpdatedAt, &release.ArchivedAt); err != nil {
			return nil, err
		}
		release.ProjectID = normalizeProjectID(release.ProjectID)
		releases = append(releases, &release)
	}
	return releases, rows.Err()
}

func (s *SQLiteStore) ListReleasesByProject(projectID string, includeArchived bool) ([]*Release, error) {
	args := []any{normalizeProjectID(projectID)}
	query := `SELECT id, application_id, COALESCE(project_id, ''), name, created_at, updated_at, archived_at FROM releases WHERE project_id = ?`
	if !includeArchived {
		query += ` AND archived_at IS NULL`
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
		if err := rows.Scan(&release.ID, &release.ApplicationID, &release.ProjectID, &release.Name, &release.CreatedAt, &release.UpdatedAt, &release.ArchivedAt); err != nil {
			return nil, err
		}
		release.ProjectID = normalizeProjectID(release.ProjectID)
		releases = append(releases, &release)
	}
	return releases, rows.Err()
}

func (s *SQLiteStore) ArchiveRelease(id string) error {
	if id == LegacyReleaseID {
		return errors.New("legacy release cannot be archived")
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM tests WHERE release_id = ?`, id).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return errors.New("release has associated results; archive or delete those results first")
	}
	now := time.Now()
	_, err := s.db.Exec(`UPDATE releases SET archived_at = ?, updated_at = ? WHERE id = ?`, now, now, id)
	return err
}

func (s *SQLiteStore) DeleteRelease(id string) error {
	if id == LegacyReleaseID {
		return errors.New("legacy release cannot be deleted")
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM tests WHERE release_id = ?`, id).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return errors.New("release has associated results; archive or delete those results first")
	}
	_, err := s.db.Exec(`DELETE FROM releases WHERE id = ?`, id)
	return err
}

func (s *SQLiteStore) SaveProject(project *Project) error {
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
	_, err = s.db.Exec(`INSERT INTO projects (id, domain_id, category_id, name, settings_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET domain_id=excluded.domain_id, category_id=excluded.category_id, name=excluded.name, settings_json=excluded.settings_json, updated_at=excluded.updated_at`,
		project.ID, project.DomainID, project.CategoryID, project.Name, string(settingsJSON), project.CreatedAt, project.UpdatedAt)
	return err
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

func (s *SQLiteStore) DeleteProject(id string) error {
	if id == DefaultProjectID {
		return errors.New("default project cannot be deleted")
	}
	id = normalizeProjectID(id)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM projects WHERE id = ?`, id).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return sql.ErrNoRows
	}
	// Serialize every archive/finalization/note transition for the project in a
	// deterministic order before snapshotting cleanup records. PostgreSQL's FK
	// rewriting means the explicit row locks are the publication tombstone.
	lockQuery := `SELECT id FROM tests WHERE project_id = ? ORDER BY id`
	if s.db.dialect == storeDialectPostgres {
		lockQuery += ` FOR UPDATE`
	}
	lockedRows, err := tx.Query(lockQuery, id)
	if err != nil {
		return err
	}
	for lockedRows.Next() {
		var testID string
		if err := lockedRows.Scan(&testID); err != nil {
			lockedRows.Close()
			return err
		}
	}
	if err := lockedRows.Err(); err != nil {
		lockedRows.Close()
		return err
	}
	lockedRows.Close()
	archiveRecords, err := listProjectRunArchivesTx(tx, id)
	if err != nil {
		return err
	}
	cataloged := make(map[string]struct{}, len(archiveRecords))
	for _, record := range archiveRecords {
		if err := enqueueArchiveCleanupTx(tx, id, record); err != nil {
			return err
		}
		cataloged[record.TestID] = struct{}{}
	}
	archiveFinalizationJobs, err := listProjectArchiveFinalizationJobsTx(tx, id)
	if err != nil {
		return err
	}
	for _, job := range archiveFinalizationJobs {
		if job == nil {
			continue
		}
		if _, ok := cataloged[job.TestID]; ok {
			continue
		}
		if err := enqueueArchiveCleanupTx(tx, id, &job.ArchiveRecord); err != nil {
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
		{`DELETE FROM github_check_links WHERE test_id IN (SELECT id FROM tests WHERE project_id = ?) OR script_id IN (SELECT id FROM scripts WHERE project_id = ?)`, []any{id, id}},
		{`DELETE FROM test_history WHERE test_id IN (SELECT id FROM tests WHERE project_id = ?)`, []any{id}},
		{`DELETE FROM test_notes WHERE test_id IN (SELECT id FROM tests WHERE project_id = ?)`, []any{id}},
		{`DELETE FROM test_interval_summaries WHERE test_id IN (SELECT id FROM tests WHERE project_id = ?)`, []any{id}},
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
		{`DELETE FROM usage_accounting_snapshots WHERE project_id = ?`, []any{id}},
		// Audit history is intentionally retained after project deletion. It is an
		// immutable governance trail, not project-owned mutable content.

		// Application/release hierarchy and its governance records.
		{`DELETE FROM release_sla_waivers WHERE project_id = ? OR release_id IN (SELECT id FROM releases WHERE project_id = ? OR application_id IN (SELECT id FROM applications WHERE project_id = ?))`, []any{id, id, id}},
		{`DELETE FROM releases WHERE project_id = ? OR application_id IN (SELECT id FROM applications WHERE project_id = ?)`, []any{id, id}},
		{`DELETE FROM applications WHERE project_id = ?`, []any{id}},

		// Script-derived sensitive records and project repository contents.
		{`DELETE FROM github_script_links WHERE script_id IN (SELECT id FROM scripts WHERE project_id = ?)`, []any{id}},
		{`DELETE FROM script_recordings WHERE script_id IN (SELECT id FROM scripts WHERE project_id = ?)`, []any{id}},
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
	return tx.Commit()
}

func deleteProjectServiceAccountGrants(tx *storeTx, projectID string) error {
	rows, err := tx.Query(`SELECT id, COALESCE(project_roles_json, '') FROM service_accounts`)
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

func (s *SQLiteStore) SaveFolder(folder *Folder) error {
	if folder.ID == "" {
		return errors.New("folder id is required")
	}
	if strings.TrimSpace(folder.Name) == "" {
		return errors.New("folder name is required")
	}
	if err := validateFolderCategory(folder.Category); err != nil {
		return err
	}
	folder.ProjectID = normalizeProjectID(folder.ProjectID)
	if folder.CreatedAt.IsZero() {
		folder.CreatedAt = time.Now()
	}
	folder.UpdatedAt = time.Now()
	_, err := s.db.Exec(`INSERT INTO folders (id, project_id, parent_id, name, category, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET project_id=excluded.project_id, parent_id=excluded.parent_id, name=excluded.name, category=excluded.category, updated_at=excluded.updated_at`,
		folder.ID, folder.ProjectID, folder.ParentID, folder.Name, folder.Category, folder.CreatedAt, folder.UpdatedAt)
	return err
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
	if newParentID != nil {
		if *newParentID == id {
			return errors.New("folder cannot be moved into itself")
		}
		isDescendant, err := s.folderHasAncestor(*newParentID, id)
		if err != nil {
			return err
		}
		if isDescendant {
			return errors.New("folder cannot be moved into its own descendant")
		}
	}
	_, err := s.db.Exec(`UPDATE folders SET parent_id = ?, updated_at = ? WHERE id = ?`, newParentID, time.Now(), id)
	return err
}

func (s *SQLiteStore) folderHasAncestor(folderID string, ancestorID string) (bool, error) {
	current := folderID
	for current != "" {
		var parent sql.NullString
		if err := s.db.QueryRow(`SELECT parent_id FROM folders WHERE id = ?`, current).Scan(&parent); err != nil {
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
	var childCount, scriptCount, testCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM folders WHERE parent_id = ?`, id).Scan(&childCount); err != nil {
		return err
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM scripts WHERE folder_id = ?`, id).Scan(&scriptCount); err != nil {
		return err
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM tests WHERE folder_id = ?`, id).Scan(&testCount); err != nil {
		return err
	}
	if childCount+scriptCount+testCount > 0 {
		return errors.New("folder is not empty")
	}
	_, err := s.db.Exec(`DELETE FROM folders WHERE id = ?`, id)
	return err
}
