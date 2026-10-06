package storage

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

// BusinessKeyStore holds each project's wrapped HMAC key for business-key
// digests. The key is created on first use and deleted with its project;
// archives digested under a deleted key can no longer be joined.
type BusinessKeyStore interface {
	// BusinessKeyProjectKey returns the project's wrapped key, storing
	// create's result when the project has none. Concurrent first callers
	// all receive the one stored key.
	BusinessKeyProjectKey(project string, create func() (string, error)) (string, error)
}

func (s *SQLiteStore) BusinessKeyProjectKey(project string, create func() (string, error)) (string, error) {
	project = strings.TrimSpace(project)
	if project == "" || create == nil {
		return "", errors.New("invalid business key request")
	}
	read := func() (string, error) {
		var wrapped string
		err := s.db.QueryRow(`SELECT wrapped_key FROM business_key_project_keys WHERE project_id = ?`, project).Scan(&wrapped)
		return wrapped, err
	}
	wrapped, err := read()
	if !errors.Is(err, sql.ErrNoRows) {
		return wrapped, err
	}
	candidate, err := create()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(candidate) == "" || len(candidate) > 16<<10 {
		return "", errors.New("invalid wrapped business key")
	}
	if _, err := s.db.Exec(`INSERT INTO business_key_project_keys(project_id, wrapped_key, created_at) VALUES(?,?,?) ON CONFLICT(project_id) DO NOTHING`, project, candidate, time.Now().UTC()); err != nil {
		return "", err
	}
	// Another controller may have stored its key first; everyone uses that one.
	return read()
}
