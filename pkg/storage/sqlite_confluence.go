package storage

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

const confluenceConnectionColumns = `id,project_id,name,enabled,site_url,email,space_id,parent_page_id,token_secret_name,auto_publish,last_checked_at,last_check_status,last_check_message,created_at,updated_at`
const confluencePublicationColumns = `test_id,project_id,connection_id,page_id,page_url,status,error,attempts,automatic,requested_by,created_at,updated_at`

func (s *SQLiteStore) CreateConfluenceConnection(c *ConfluenceConnection) error {
	if err := c.Validate(); err != nil {
		return err
	}
	now := time.Now().UTC()
	c.CreatedAt, c.UpdatedAt = now, now
	_, err := s.db.Exec(`INSERT INTO confluence_connections (`+confluenceConnectionColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, c.ID, c.ProjectID, c.Name, c.Enabled, c.SiteURL, c.Email, c.SpaceID, c.ParentPageID, c.TokenSecretName, c.AutoPublish, c.LastCheckedAt, c.LastCheckStatus, c.LastCheckMessage, c.CreatedAt, c.UpdatedAt)
	return err
}

func scanConfluenceConnection(row interface{ Scan(...any) error }) (*ConfluenceConnection, error) {
	c := new(ConfluenceConnection)
	err := row.Scan(&c.ID, &c.ProjectID, &c.Name, &c.Enabled, &c.SiteURL, &c.Email, &c.SpaceID, &c.ParentPageID, &c.TokenSecretName, &c.AutoPublish, &c.LastCheckedAt, &c.LastCheckStatus, &c.LastCheckMessage, &c.CreatedAt, &c.UpdatedAt)
	return c, err
}

func (s *SQLiteStore) GetConfluenceConnection(id string) (*ConfluenceConnection, error) {
	return scanConfluenceConnection(s.db.QueryRow(`SELECT `+confluenceConnectionColumns+` FROM confluence_connections WHERE id=?`, strings.TrimSpace(id)))
}

func (s *SQLiteStore) ListConfluenceConnections(projectID string) ([]*ConfluenceConnection, error) {
	rows, err := s.db.Query(`SELECT `+confluenceConnectionColumns+` FROM confluence_connections WHERE project_id=? ORDER BY name,id`, strings.TrimSpace(projectID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*ConfluenceConnection, 0)
	for rows.Next() {
		c, err := scanConfluenceConnection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) UpdateConfluenceConnectionIfUnchanged(c *ConfluenceConnection, expected time.Time) (bool, error) {
	if err := c.Validate(); err != nil {
		return false, err
	}
	now := time.Now().UTC()
	result, err := s.db.Exec(`UPDATE confluence_connections SET name=?,enabled=?,site_url=?,email=?,space_id=?,parent_page_id=?,token_secret_name=?,auto_publish=?,last_checked_at=?,last_check_status=?,last_check_message=?,updated_at=? WHERE id=? AND project_id=? AND updated_at=?`, c.Name, c.Enabled, c.SiteURL, c.Email, c.SpaceID, c.ParentPageID, c.TokenSecretName, c.AutoPublish, c.LastCheckedAt, c.LastCheckStatus, c.LastCheckMessage, now, c.ID, c.ProjectID, expected)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if n == 1 {
		c.UpdatedAt = now
	}
	return n == 1, err
}

func (s *SQLiteStore) UpdateConfluenceConnectionHealth(id, projectID, status, message string, checkedAt, expectedUpdatedAt time.Time) error {
	r, err := s.db.Exec(`UPDATE confluence_connections SET last_checked_at=?,last_check_status=?,last_check_message=? WHERE id=? AND project_id=? AND updated_at=?`, checkedAt, status, message, id, projectID, expectedUpdatedAt)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return err
}

func (s *SQLiteStore) DeleteConfluenceConnection(id, projectID string) (bool, error) {
	r, err := s.db.Exec(`DELETE FROM confluence_connections WHERE id=? AND project_id=?`, id, projectID)
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n == 1, err
}

func scanConfluencePublication(row interface{ Scan(...any) error }) (*ConfluencePublication, error) {
	p := new(ConfluencePublication)
	err := row.Scan(&p.TestID, &p.ProjectID, &p.ConnectionID, &p.PageID, &p.PageURL, &p.Status, &p.Error, &p.Attempts, &p.Automatic, &p.RequestedBy, &p.CreatedAt, &p.UpdatedAt)
	return p, err
}

func (s *SQLiteStore) GetConfluencePublication(testID, connectionID string) (*ConfluencePublication, error) {
	return scanConfluencePublication(s.db.QueryRow(`SELECT `+confluencePublicationColumns+` FROM confluence_publications WHERE test_id=? AND connection_id=?`, testID, connectionID))
}

func (s *SQLiteStore) ListConfluencePublications(testID string) ([]*ConfluencePublication, error) {
	rows, err := s.db.Query(`SELECT `+confluencePublicationColumns+` FROM confluence_publications WHERE test_id=? ORDER BY created_at`, testID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*ConfluencePublication, 0)
	for rows.Next() {
		p, err := scanConfluencePublication(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ClaimConfluencePublication is an atomic claim across controllers. A stale
// claim becomes eligible after five minutes. Automatic claims never republish
// a completed page and stop after three failures.
func (s *SQLiteStore) ClaimConfluencePublication(testID, projectID, connectionID, actor string, automatic bool) (*ConfluencePublication, bool, error) {
	now := time.Now().UTC()
	_, err := s.db.Exec(`INSERT INTO confluence_publications (`+confluencePublicationColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(test_id,connection_id) DO NOTHING`, testID, projectID, connectionID, "", "", "queued", "", 0, automatic, actor, now, now)
	if err != nil {
		return nil, false, err
	}
	condition := `(status!='publishing' OR updated_at<?)`
	args := []any{automatic, actor, now, testID, connectionID, now.Add(-5 * time.Minute)}
	if automatic {
		condition += ` AND status!='complete' AND attempts<3 AND (status!='failed' OR updated_at<?)`
		args = append(args, now.Add(-time.Minute))
	}
	r, err := s.db.Exec(`UPDATE confluence_publications SET status='publishing',error='',attempts=attempts+1,automatic=CASE WHEN automatic=TRUE THEN TRUE ELSE ? END,requested_by=?,updated_at=? WHERE test_id=? AND connection_id=? AND `+condition, args...)
	if err != nil {
		return nil, false, err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return nil, false, err
	}
	p, err := s.GetConfluencePublication(testID, connectionID)
	return p, n == 1, err
}

func (s *SQLiteStore) FinishConfluencePublication(testID, connectionID, status, pageID, pageURL, message string) error {
	if status != "complete" && status != "failed" {
		return errors.New("invalid Confluence publication status")
	}
	r, err := s.db.Exec(`UPDATE confluence_publications SET status=?,page_id=CASE WHEN ?='' THEN page_id ELSE ? END,page_url=CASE WHEN ?='' THEN page_url ELSE ? END,error=?,updated_at=? WHERE test_id=? AND connection_id=?`, status, pageID, pageID, pageURL, pageURL, message, time.Now().UTC(), testID, connectionID)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return err
}

func (s *SQLiteStore) AbandonConfluencePublication(testID, connectionID, message string) error {
	_, err := s.db.Exec(`UPDATE confluence_publications SET status='failed',error=?,attempts=3,updated_at=? WHERE test_id=? AND connection_id=? AND status!='complete'`, message, time.Now().UTC(), testID, connectionID)
	return err
}

func (s *SQLiteStore) ListPendingConfluencePublications(limit int) ([]*ConfluencePublication, error) {
	if limit < 1 || limit > 100 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT `+confluencePublicationColumns+` FROM confluence_publications WHERE automatic=TRUE AND attempts<3 AND (status='queued' OR (status='failed' AND updated_at<?) OR (status='publishing' AND updated_at<?)) ORDER BY updated_at LIMIT ?`, time.Now().UTC().Add(-time.Minute), time.Now().UTC().Add(-5*time.Minute), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*ConfluencePublication, 0)
	for rows.Next() {
		p, err := scanConfluencePublication(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func enqueueConfluencePublicationTx(tx *storeTx, testID, projectID string) error {
	now := time.Now().UTC()
	_, err := tx.Exec(`INSERT INTO confluence_publications (`+confluencePublicationColumns+`) SELECT ?,project_id,id,'','','queued','',0,TRUE,'automatic',?,? FROM confluence_connections WHERE project_id=? AND enabled=TRUE AND auto_publish=TRUE ON CONFLICT(test_id,connection_id) DO NOTHING`, testID, now, now, projectID)
	return err
}
