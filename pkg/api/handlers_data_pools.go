package api

import (
	"database/sql"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/fileasset"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	defaultDataPoolRowListLimit = 100
	maxDataPoolRowListLimit     = 100

	dataPoolExportPageSize          = 1000
	dataPoolImportPreviewSampleRows = 10
	// Mirrors of the engine's data-source load caps (pkg/engine/data.go). The
	// import endpoint pre-enforces them so an oversized pool fails at upload
	// time instead of at run start.
	// A materialized pool always gains a generated CSV header. The engine's
	// 1,000,000-record cap includes that header, so at most 999,999 data rows
	// are executable.
	maxDataPoolImportRecords = 1_000_000 - 1
	maxDataPoolImportColumns = 4_096
	maxDataPoolImportCells   = 2_000_000
	// Above this input size the flattened pool CSV starts crowding the 64 MiB
	// generator script budget that also has to carry the script itself.
	dataPoolImportSizeWarningBytes = 32 << 20
	dataPoolRowCursorVersion       = 1
	maxDataPoolRowCursorBytes      = 4096
	dataPoolExportMetadataMarker   = "APEXLOAD_DATA_POOL_EXPORT_V1"
	dataPoolExportStatusColumnBase = "__apexload_row_status"
	// A marked APEX backup carries one formula-guard byte per guarded cell,
	// one status field per row, and a small metadata preamble in addition to
	// the engine-capped user-data CSV. Keep that transport-only overhead
	// bounded without raising the limit for ordinary CSV uploads.
	dataPoolExportEnvelopeOverheadBytes int64 = 16 << 20
	dataPoolMaxMarkedCSVBytes                 = fileasset.MaxBytes + dataPoolExportEnvelopeOverheadBytes
)

var (
	errInvalidDataPoolRowCursor     = errors.New("invalid data pool row cursor")
	errStaleDataPoolRowCursor       = errors.New("data pool rows changed during pagination")
	errUnsupportedDataPoolRowCursor = errors.New("cursor data pool row pagination is unsupported")
	errDataPoolCSVByteLimit         = errors.New("data pool CSV byte limit exceeded")
)

type dataPoolRowsPager interface {
	ListDataPoolRowsPageAfter(poolID, afterKey, afterID string, limit int) ([]*storage.SharedDataPoolRow, string, error)
}

type dataPoolRowsPageStateReader interface {
	DataPoolRowsPageState(poolID string) (string, int, error)
}

type dataPoolRowPageCursor struct {
	Version  int    `json:"v"`
	PoolID   string `json:"p"`
	AfterKey string `json:"k"`
	AfterID  string `json:"i"`
	Revision string `json:"r"`
	Seen     int    `json:"s"`
}

type dataPoolRowCursorPage struct {
	Rows       []*storage.SharedDataPoolRow
	Total      int
	Offset     int
	NextCursor string
	HasMore    bool
	Revision   string
}

type dataPoolDetailResponse struct {
	DataPool   *storage.SharedDataPool      `json:"data_pool"`
	Rows       []*storage.SharedDataPoolRow `json:"rows"`
	Count      int                          `json:"count"`
	Total      int                          `json:"total"`
	Limit      int                          `json:"limit"`
	Offset     int                          `json:"offset"`
	Revision   string                       `json:"revision"`
	HasMore    bool                         `json:"has_more"`
	NextCursor string                       `json:"next_cursor"`
}

type dataPoolRowsResponse struct {
	Rows       []*storage.SharedDataPoolRow `json:"rows"`
	Count      int                          `json:"count"`
	Total      int                          `json:"total"`
	Limit      int                          `json:"limit"`
	Offset     int                          `json:"offset"`
	Revision   string                       `json:"revision"`
	HasMore    bool                         `json:"has_more"`
	NextCursor string                       `json:"next_cursor"`
}

func (s *Server) handleListDataPools(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusOK, map[string]any{"data_pools": []*storage.SharedDataPool{}, "count": 0})
		return
	}
	includeArchived := r.URL.Query().Get("include_archived") == "true"
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	var pools []*storage.SharedDataPool
	var err error
	if projectID != "" {
		if scoped, ok := s.store.(interface {
			ListDataPoolsForProject(bool, string) ([]*storage.SharedDataPool, error)
		}); ok {
			pools, err = scoped.ListDataPoolsForProject(includeArchived, projectID)
		} else {
			writeError(w, http.StatusNotImplemented, "project-scoped data pool listing is not supported by this storage backend")
			return
		}
	} else {
		pools, err = s.store.ListDataPools(includeArchived)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if projectID != "" {
		normalizedProjectID := normalizeProjectID(projectID)
		filtered := pools[:0]
		for _, pool := range pools {
			if normalizeProjectID(pool.ProjectID) == normalizedProjectID {
				filtered = append(filtered, pool)
			}
		}
		pools = filtered
	}
	if pools == nil {
		pools = []*storage.SharedDataPool{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data_pools": pools, "count": len(pools)})
}

func (s *Server) handleCreateDataPool(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	var req storage.SharedDataPool
	if !decodeJSONBody(w, r, &req) {
		return
	}
	req.ID = uuid.New().String()
	req.Name = strings.TrimSpace(req.Name)
	req.ProjectID = normalizeProjectID(req.ProjectID)
	// Archiving is a governed transition on PUT. A create request must never
	// smuggle in an archived_at value that hides the new pool from normal
	// listings while producing only the generic create audit event.
	req.ArchivedAt = nil
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "data pool name is required")
		return
	}
	now := time.Now()
	req.CreatedAt = now
	req.UpdatedAt = now
	if err := s.store.SaveDataPool(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "data_pool.create", "success", "data_pool", req.ID, req.ProjectID, map[string]any{"name": req.Name})
	writeJSON(w, http.StatusCreated, req)
}

func (s *Server) handleGetDataPool(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	pool, err := s.store.GetDataPool(r.PathValue("id"))
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		writeError(w, status, "data pool not found")
		return
	}
	limit, offset := parseLimitOffsetQuery(r, defaultDataPoolRowListLimit)
	if limit > maxDataPoolRowListLimit {
		limit = maxDataPoolRowListLimit
	}
	if r.URL.Query().Has("cursor") {
		if strings.TrimSpace(r.URL.Query().Get("offset")) != "" {
			writeError(w, http.StatusBadRequest, "cursor and offset cannot be combined")
			return
		}
		page, err := s.listDataPoolRowsCursorPage(pool.ID, r.URL.Query().Get("cursor"), limit)
		if err != nil {
			writeDataPoolRowCursorError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, dataPoolDetailResponse{
			DataPool: pool, Rows: page.Rows, Count: len(page.Rows), Total: page.Total,
			Limit: limit, Offset: page.Offset, Revision: page.Revision,
			HasMore: page.HasMore, NextCursor: page.NextCursor,
		})
		return
	}
	rows, err := s.listDataPoolRowsPage(pool.ID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if rows == nil {
		rows = []*storage.SharedDataPoolRow{}
	}
	total, err := s.dataPoolRowCount(pool.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count data pool rows")
		return
	}
	revision := s.dataPoolRowsPageRevision(pool.ID)
	writeJSON(w, http.StatusOK, dataPoolDetailResponse{
		DataPool: pool, Rows: rows, Count: len(rows), Total: total, Limit: limit, Offset: offset,
		Revision: revision, HasMore: offset+len(rows) < total, NextCursor: "",
	})
}

func (s *Server) handleUpdateDataPool(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	id := r.PathValue("id")
	existing, err := s.store.GetDataPool(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "data pool not found")
		return
	}
	var req struct {
		// Name is optional for metadata-only updates but must be non-empty when
		// explicitly provided.
		Name        *string         `json:"name"`
		Description json.RawMessage `json:"description"`
		Schema      json.RawMessage `json:"schema"`
		UpdatedAt   time.Time       `json:"updated_at"`
		// Raw so absent (keep current) is distinguishable from null (restore)
		// and a timestamp (archive): archiving is a governance transition and
		// must never flip as a side effect of a rename that omits the field.
		ArchivedAt json.RawMessage `json:"archived_at"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	wasArchived := existing.ArchivedAt != nil
	if req.Name != nil {
		existing.Name = strings.TrimSpace(*req.Name)
	}
	if req.Name == nil {
		// No rename requested.
	} else if existing.Name == "" {
		writeError(w, http.StatusBadRequest, "data pool name is required")
		return
	}

	if len(req.Description) > 0 {
		if strings.TrimSpace(string(req.Description)) == "null" {
			existing.Description = ""
		} else if err := json.Unmarshal(req.Description, &existing.Description); err != nil {
			writeError(w, http.StatusBadRequest, "description must be a string or null")
			return
		}
	}
	if len(req.Schema) > 0 {
		if strings.TrimSpace(string(req.Schema)) == "null" {
			existing.Schema = nil
		} else if err := json.Unmarshal(req.Schema, &existing.Schema); err != nil {
			writeError(w, http.StatusBadRequest, "schema must be an object or null")
			return
		}
	}
	if len(req.ArchivedAt) > 0 {
		if strings.TrimSpace(string(req.ArchivedAt)) == "null" {
			existing.ArchivedAt = nil
		} else {
			var archivedAt time.Time
			if err := json.Unmarshal(req.ArchivedAt, &archivedAt); err != nil {
				writeError(w, http.StatusBadRequest, "archived_at must be a timestamp or null")
				return
			}
			existing.ArchivedAt = &archivedAt
		}
	}
	updater, ok := s.store.(interface {
		UpdateDataPoolIfUnchanged(pool *storage.SharedDataPool, expectedUpdatedAt time.Time) (bool, error)
	})
	if !ok {
		writeError(w, http.StatusNotImplemented, "optimistic data pool updates are not supported by this storage backend")
		return
	}
	// updated_at is the editor's concurrency token. Substituting the freshly
	// loaded value here would only guard the in-handler window and let a stale
	// editor silently overwrite changes made since its GET.
	if req.UpdatedAt.IsZero() {
		writeError(w, http.StatusBadRequest, "updated_at is required")
		return
	}
	updated, err := updater.UpdateDataPoolIfUnchanged(existing, req.UpdatedAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !updated {
		writeError(w, http.StatusConflict, "data pool changed while the update was in flight; reload and retry")
		return
	}
	details := map[string]any{"name": existing.Name}
	if isArchived := existing.ArchivedAt != nil; isArchived != wasArchived {
		details["archived"] = isArchived
	}
	s.recordAuditEvent(r, "data_pool.update", "success", "data_pool", existing.ID, existing.ProjectID, details)
	writeJSON(w, http.StatusOK, existing)
}

func (s *Server) handleDeleteDataPool(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	id := r.PathValue("id")
	projectID := ""
	name := ""
	if pool, err := s.store.GetDataPool(id); err == nil && pool != nil {
		projectID = pool.ProjectID
		name = pool.Name
	}
	if err := s.store.DeleteDataPool(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "data_pool.delete", "success", "data_pool", id, projectID, map[string]any{"name": name})
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

func (s *Server) handleListDataPoolRows(w http.ResponseWriter, r *http.Request) {
	limit, offset := parseLimitOffsetQuery(r, defaultDataPoolRowListLimit)
	if limit > maxDataPoolRowListLimit {
		limit = maxDataPoolRowListLimit
	}
	if s.store == nil {
		writeJSON(w, http.StatusOK, dataPoolRowsResponse{
			Rows: []*storage.SharedDataPoolRow{}, Limit: limit, Offset: offset,
		})
		return
	}
	if r.URL.Query().Has("cursor") {
		if strings.TrimSpace(r.URL.Query().Get("offset")) != "" {
			writeError(w, http.StatusBadRequest, "cursor and offset cannot be combined")
			return
		}
		page, err := s.listDataPoolRowsCursorPage(r.PathValue("id"), r.URL.Query().Get("cursor"), limit)
		if err != nil {
			writeDataPoolRowCursorError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, dataPoolRowsResponse{
			Rows: page.Rows, Count: len(page.Rows), Total: page.Total,
			Limit: limit, Offset: page.Offset, Revision: page.Revision,
			HasMore: page.HasMore, NextCursor: page.NextCursor,
		})
		return
	}
	rows, err := s.listDataPoolRowsPage(r.PathValue("id"), limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if rows == nil {
		rows = []*storage.SharedDataPoolRow{}
	}
	total, err := s.dataPoolRowCount(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count data pool rows")
		return
	}
	revision := s.dataPoolRowsPageRevision(r.PathValue("id"))
	writeJSON(w, http.StatusOK, dataPoolRowsResponse{
		Rows: rows, Count: len(rows), Total: total, Limit: limit, Offset: offset,
		Revision: revision, HasMore: offset+len(rows) < total, NextCursor: "",
	})
}

func (s *Server) dataPoolRowsPageRevision(poolID string) string {
	stateReader, ok := s.store.(dataPoolRowsPageStateReader)
	if !ok {
		return ""
	}
	revision, _, err := stateReader.DataPoolRowsPageState(poolID)
	if err != nil {
		return ""
	}
	return revision
}

func (s *Server) listDataPoolRowsPage(poolID string, limit, offset int) ([]*storage.SharedDataPoolRow, error) {
	if paged, ok := s.store.(interface {
		ListDataPoolRowsPage(string, int, int) ([]*storage.SharedDataPoolRow, error)
	}); ok {
		return paged.ListDataPoolRowsPage(poolID, limit, offset)
	}
	rows, err := s.store.ListDataPoolRows(poolID)
	if err != nil {
		return nil, err
	}
	if offset >= len(rows) {
		return []*storage.SharedDataPoolRow{}, nil
	}
	end := len(rows)
	if limit < len(rows)-offset {
		end = offset + limit
	}
	return rows[offset:end], nil
}

func encodeDataPoolRowPageCursor(cursor dataPoolRowPageCursor) (string, error) {
	encoded, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeDataPoolRowPageCursor(raw, poolID string) (dataPoolRowPageCursor, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return dataPoolRowPageCursor{
			Version: dataPoolRowCursorVersion,
			PoolID:  poolID,
		}, nil
	}
	if len(raw) > maxDataPoolRowCursorBytes {
		return dataPoolRowPageCursor{}, fmt.Errorf("%w: cursor is too large", errInvalidDataPoolRowCursor)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return dataPoolRowPageCursor{}, fmt.Errorf("%w: malformed encoding", errInvalidDataPoolRowCursor)
	}
	var cursor dataPoolRowPageCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil {
		return dataPoolRowPageCursor{}, fmt.Errorf("%w: malformed payload", errInvalidDataPoolRowCursor)
	}
	if cursor.Version != dataPoolRowCursorVersion ||
		cursor.PoolID != poolID ||
		cursor.AfterKey == "" ||
		cursor.AfterID == "" ||
		cursor.Revision == "" ||
		cursor.Seen <= 0 {
		return dataPoolRowPageCursor{}, fmt.Errorf("%w: cursor fields are inconsistent", errInvalidDataPoolRowCursor)
	}
	return cursor, nil
}

// listDataPoolRowsCursorPage binds a keyset boundary to a whole-pool revision.
// Keyset traversal prevents deletes before the boundary from shifting later
// rows past an OFFSET, while the revision fence makes inserts, updates, and
// deletes explicit drift instead of silently mixing two pool snapshots.
func (s *Server) listDataPoolRowsCursorPage(poolID, rawCursor string, limit int) (dataPoolRowCursorPage, error) {
	pager, ok := s.store.(dataPoolRowsPager)
	if !ok {
		return dataPoolRowCursorPage{}, errUnsupportedDataPoolRowCursor
	}
	stateReader, ok := s.store.(dataPoolRowsPageStateReader)
	if !ok {
		return dataPoolRowCursorPage{}, errUnsupportedDataPoolRowCursor
	}
	cursor, err := decodeDataPoolRowPageCursor(rawCursor, poolID)
	if err != nil {
		return dataPoolRowCursorPage{}, err
	}
	revision, total, err := stateReader.DataPoolRowsPageState(poolID)
	if err != nil {
		return dataPoolRowCursorPage{}, fmt.Errorf("load data pool row revision: %w", err)
	}
	if cursor.Revision != "" && cursor.Revision != revision {
		return dataPoolRowCursorPage{}, errStaleDataPoolRowCursor
	}
	if cursor.Seen > total {
		return dataPoolRowCursorPage{}, errStaleDataPoolRowCursor
	}
	if cursor.Revision == "" {
		cursor.Revision = revision
	}

	rows, nextKey, err := pager.ListDataPoolRowsPageAfter(poolID, cursor.AfterKey, cursor.AfterID, limit)
	if err != nil {
		return dataPoolRowCursorPage{}, fmt.Errorf("list data pool row page: %w", err)
	}
	if rows == nil {
		rows = []*storage.SharedDataPoolRow{}
	}
	verifiedRevision, verifiedTotal, err := stateReader.DataPoolRowsPageState(poolID)
	if err != nil {
		return dataPoolRowCursorPage{}, fmt.Errorf("verify data pool row revision: %w", err)
	}
	if verifiedRevision != cursor.Revision || verifiedTotal != total {
		return dataPoolRowCursorPage{}, errStaleDataPoolRowCursor
	}
	nextSeen := cursor.Seen + len(rows)
	if nextSeen > total || (nextSeen < total && len(rows) == 0) {
		return dataPoolRowCursorPage{}, errStaleDataPoolRowCursor
	}
	hasMore := nextSeen < total
	nextCursor := ""
	if hasMore {
		last := rows[len(rows)-1]
		if last == nil || strings.TrimSpace(last.ID) == "" || strings.TrimSpace(nextKey) == "" ||
			(nextKey == cursor.AfterKey && last.ID == cursor.AfterID) {
			return dataPoolRowCursorPage{}, errors.New("data pool row pager did not advance")
		}
		nextCursor, err = encodeDataPoolRowPageCursor(dataPoolRowPageCursor{
			Version:  dataPoolRowCursorVersion,
			PoolID:   poolID,
			AfterKey: nextKey,
			AfterID:  last.ID,
			Revision: cursor.Revision,
			Seen:     nextSeen,
		})
		if err != nil {
			return dataPoolRowCursorPage{}, fmt.Errorf("encode data pool row cursor: %w", err)
		}
	}
	return dataPoolRowCursorPage{
		Rows:       rows,
		Total:      total,
		Offset:     cursor.Seen,
		NextCursor: nextCursor,
		HasMore:    hasMore,
		Revision:   cursor.Revision,
	}, nil
}

func writeDataPoolRowCursorError(w http.ResponseWriter, _ *http.Request, err error) {
	switch {
	case errors.Is(err, errInvalidDataPoolRowCursor):
		writeError(w, http.StatusBadRequest, "invalid data pool row cursor")
	case errors.Is(err, errStaleDataPoolRowCursor):
		writeError(w, http.StatusConflict, "data pool rows changed while pages were being loaded; reload from the first page")
	case errors.Is(err, errUnsupportedDataPoolRowCursor):
		writeError(w, http.StatusNotImplemented, "cursor data pool row pagination is not supported by this storage backend")
	default:
		writeError(w, http.StatusInternalServerError, "failed to load data pool rows")
	}
}

func (s *Server) handleCreateDataPoolRow(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	var req storage.SharedDataPoolRow
	if !decodeJSONBody(w, r, &req) {
		return
	}
	req.ID = uuid.New().String()
	req.PoolID = r.PathValue("id")
	now := time.Now()
	req.CreatedAt = now
	req.UpdatedAt = now
	saver, supported := s.store.(interface {
		SaveDataPoolRowIfValid(*storage.SharedDataPoolRow, storage.DataPoolRowsMutationValidator) error
	})
	if !supported {
		writeError(w, http.StatusNotImplemented, "transactional data pool row validation is not supported by this storage backend")
		return
	}
	if err := saver.SaveDataPoolRowIfValid(&req, validateExecutableDataPoolRows); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	projectID := ""
	if pool, err := s.store.GetDataPool(req.PoolID); err == nil && pool != nil {
		projectID = pool.ProjectID
	}
	s.recordAuditEvent(r, "data_pool.row.create", "success", "data_pool_row", req.ID, projectID, map[string]any{"pool_id": req.PoolID})
	writeJSON(w, http.StatusCreated, req)
}

func (s *Server) handleUpdateDataPoolRow(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	poolID := r.PathValue("id")
	rowID := r.PathValue("row_id")
	existing, ok := s.requireDataPoolRow(w, poolID, rowID)
	if !ok {
		return
	}
	var req struct {
		Data    map[string]string `json:"data"`
		Status  string            `json:"status"`
		Version int               `json:"version"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.Data == nil {
		writeError(w, http.StatusBadRequest, "data is required")
		return
	}
	row := &storage.SharedDataPoolRow{
		ID:        rowID,
		PoolID:    poolID,
		Data:      req.Data,
		Status:    req.Status,
		Version:   req.Version,
		CreatedAt: existing.CreatedAt,
	}
	if row.Status == "" {
		// The row editor updates data without sending status. Preserve explicit
		// inactive/consumed state instead of silently reactivating the row.
		row.Status = existing.Status
	}
	updater, supported := s.store.(interface {
		UpdateDataPoolRowIfVersionAndValid(poolID string, row *storage.SharedDataPoolRow, expectedVersion int, validate storage.DataPoolRowsMutationValidator) (bool, error)
	})
	if !supported {
		writeError(w, http.StatusNotImplemented, "optimistic data pool row updates are not supported by this storage backend")
		return
	}
	// version is the editor's concurrency token. Substituting the freshly
	// loaded version would let a tokenless stale client overwrite changes made
	// since its GET.
	if req.Version <= 0 {
		writeError(w, http.StatusBadRequest, "version must be greater than zero")
		return
	}
	updated, err := updater.UpdateDataPoolRowIfVersionAndValid(poolID, row, req.Version, validateExecutableDataPoolRows)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !updated {
		writeError(w, http.StatusConflict, "data pool row changed while the update was in flight; reload and retry")
		return
	}
	projectID := ""
	if pool, err := s.store.GetDataPool(row.PoolID); err == nil && pool != nil {
		projectID = pool.ProjectID
	}
	s.recordAuditEvent(r, "data_pool.row.update", "success", "data_pool_row", row.ID, projectID, map[string]any{"pool_id": row.PoolID})
	writeJSON(w, http.StatusOK, row)
}

func (s *Server) handleDeleteDataPoolRow(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	id := r.PathValue("row_id")
	projectID := ""
	poolID := r.PathValue("id")
	if _, ok := s.requireDataPoolRow(w, poolID, id); !ok {
		return
	}
	if pool, err := s.store.GetDataPool(poolID); err == nil && pool != nil {
		projectID = pool.ProjectID
	}
	if scoped, ok := s.store.(interface {
		DeleteDataPoolRowFromPool(poolID string, id string) (bool, error)
	}); ok {
		deleted, err := scoped.DeleteDataPoolRowFromPool(poolID, id)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if !deleted {
			writeError(w, http.StatusNotFound, "data pool row not found")
			return
		}
	} else if err := s.store.DeleteDataPoolRow(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "data_pool.row.delete", "success", "data_pool_row", id, projectID, map[string]any{"pool_id": poolID})
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

func (s *Server) requireDataPoolRow(w http.ResponseWriter, poolID string, rowID string) (*storage.SharedDataPoolRow, bool) {
	if scoped, ok := s.store.(interface {
		GetDataPoolRowFromPool(poolID string, id string) (*storage.SharedDataPoolRow, error)
	}); ok {
		row, err := scoped.GetDataPoolRowFromPool(poolID, rowID)
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "data pool row not found")
			return nil, false
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load data pool row")
			return nil, false
		}
		if row == nil || row.ID != rowID || row.PoolID != poolID {
			writeError(w, http.StatusNotFound, "data pool row not found")
			return nil, false
		}
		return row, true
	}
	rows, err := s.store.ListDataPoolRows(poolID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load data pool rows")
		return nil, false
	}
	for _, row := range rows {
		if row != nil && row.ID == rowID && row.PoolID == poolID {
			return row, true
		}
	}
	// Use the same response for a missing row and a row owned by another pool so
	// callers cannot use this nested route to enumerate cross-project row IDs.
	writeError(w, http.StatusNotFound, "data pool row not found")
	return nil, false
}

func (s *Server) handleListScenarioDataPools(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusOK, map[string]any{"data_pools": []storage.ScenarioDataPoolBinding{}, "count": 0})
		return
	}
	bindings, err := s.store.ListScenarioDataPoolBindings(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if bindings == nil {
		bindings = []storage.ScenarioDataPoolBinding{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data_pools": bindings, "count": len(bindings)})
}

func (s *Server) handleUpdateScenarioDataPools(w http.ResponseWriter, r *http.Request) {
	if refuseUnconditionedScenarioWriter(w, r) {
		return
	}
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	scenarioID := r.PathValue("id")
	scenario, ok := s.runner.GetScenario(scenarioID)
	if !ok {
		writeError(w, http.StatusNotFound, "scenario not found")
		return
	}
	var req struct {
		DataPools []storage.ScenarioDataPoolBinding `json:"data_pools"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	bindings, err := normalizeScenarioDataPoolBindings(scenarioID, req.DataPools)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.validateScenarioDataPoolProjects(scenario.ProjectID, bindings); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	updater, ok := s.store.(interface {
		UpdateScenarioDataPoolBindingsIfUnchanged(string, []storage.ScenarioDataPoolBinding, time.Time) (time.Time, bool, error)
	})
	if !ok {
		writeError(w, http.StatusNotImplemented, "optimistic scenario data-pool updates are not supported by this storage backend")
		return
	}
	updatedAt, updated, err := updater.UpdateScenarioDataPoolBindingsIfUnchanged(scenarioID, bindings, scenario.UpdatedAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !updated {
		s.runner.refreshScenariosForHARead(true)
		writeError(w, http.StatusConflict, "scenario changed while data-pool bindings were being updated; reload and retry")
		return
	}
	if !s.runner.replaceScenarioDataPools(scenarioID, bindings, scenario.UpdatedAt, updatedAt) {
		s.runner.refreshScenariosForHARead(true)
	}
	s.recordAuditEvent(r, "scenario.data_pools.update", "success", "scenario", scenarioID, scenario.ProjectID, map[string]any{"binding_count": len(bindings)})
	writeJSON(w, http.StatusOK, map[string]any{"data_pools": bindings, "count": len(bindings)})
}

func (s *Server) validateScenarioDataPoolProjects(projectID string, bindings []storage.ScenarioDataPoolBinding) error {
	if s.store == nil || len(bindings) == 0 {
		return nil
	}
	expectedProjectID := normalizeProjectID(projectID)
	for _, binding := range bindings {
		pool, err := s.store.GetDataPool(binding.PoolID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("data pool %q not found", binding.PoolID)
			}
			return fmt.Errorf("load data pool %q: %w", binding.PoolID, err)
		}
		if normalizeProjectID(pool.ProjectID) != expectedProjectID {
			return fmt.Errorf("data pool %q does not belong to scenario project", binding.PoolID)
		}
		if err := errDataPoolArchived(pool); err != nil {
			return err
		}
	}
	return nil
}

// handleImportDataPoolRows bulk-loads rows from an uploaded CSV/DAT file.
// mode=append|replace, delimiter=auto|comma|tab, header=true|false (+columns=
// when false), dry_run=true parses and previews without writing.
// OpenAPI-Responses: 404
func (s *Server) handleImportDataPoolRows(w http.ResponseWriter, r *http.Request) {
	// Dry-run previews can contain the same sensitive values as the pool.
	w.Header().Set("Cache-Control", "no-store")
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	pool, err := s.store.GetDataPool(r.PathValue("id"))
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		writeError(w, status, "data pool not found")
		return
	}
	if err := errDataPoolArchived(pool); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, dataPoolCSVImportMaxBodyBytes)
	data, _, ok := readImportMultipartFile(w, r, dataPoolCSVImportMaxBodyBytes)
	if !ok {
		return
	}
	rawContent := string(data)
	_, envelopeStatusColumn, err := extractDataPoolExportMetadata(rawContent)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	isAPEXExport := envelopeStatusColumn != ""
	if err := validateDataPoolImportFileSize(int64(len(data)), isAPEXExport); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	mode := strings.ToLower(strings.TrimSpace(r.FormValue("mode")))
	if mode == "" {
		mode = "append"
	}
	if mode != "append" && mode != "replace" {
		writeError(w, http.StatusBadRequest, `mode must be "append" or "replace"`)
		return
	}
	dryRun, err := parseOptionalDataPoolBool(r.FormValue("dry_run"), false, "dry_run")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Formula guarding and the hidden row-status column are decoded only when
	// the file itself carries the self-identifying APEX preamble.
	// Filenames are user-controlled and exports are routinely renamed.
	columns, records, recordStatuses, warnings, err := parseDataPoolCSVImport(
		rawContent,
		r.FormValue("delimiter"),
		r.FormValue("header"),
		r.FormValue("columns"),
	)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	emptyReplaceOnlyWarning := ""
	if len(records) == 0 && mode != "replace" {
		emptyReplaceOnlyWarning = "an empty APEX data pool export can only be imported with mode=replace"
		if !dryRun {
			writeError(w, http.StatusBadRequest, emptyReplaceOnlyWarning)
			return
		}
	}
	if len(data) > dataPoolImportSizeWarningBytes {
		warnings = append(warnings, fmt.Sprintf("file is %d MiB; the flattened pool must fit the 64 MiB generator script budget together with the script itself", len(data)>>20))
	}
	// Engine caps apply to the flattened executable pool, not merely this
	// upload. For append, load the existing rows so columns introduced by older
	// active rows participate in the union/cell/byte checks; explicitly inactive
	// rows are omitted just as they are during run materialization.
	var existingPoolRows []*storage.SharedDataPoolRow
	var existingRuntimeRows []*storage.SharedDataPoolRow
	existingRows := 0
	existingRevision := ""
	if mode == "append" {
		existingPoolRows, err = s.store.ListDataPoolRows(pool.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to inspect existing data pool rows")
			return
		}
		existingRows = len(existingPoolRows)
		existingRuntimeRows = activeDataPoolRows(existingPoolRows)
	} else {
		if revisioner, ok := s.store.(interface {
			DataPoolRowsRevision(poolID string) (string, int, error)
		}); ok {
			existingRevision, existingRows, err = revisioner.DataPoolRowsRevision(pool.ID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to fingerprint existing data pool rows")
				return
			}
		} else {
			existingRows, err = s.dataPoolRowCount(pool.ID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to count existing data pool rows")
				return
			}
		}
	}
	incomingRuntimeRows := make([]map[string]string, 0, len(records))
	for index, record := range records {
		if index >= len(recordStatuses) || isActiveDataPoolRowStatus(recordStatuses[index]) {
			incomingRuntimeRows = append(incomingRuntimeRows, record)
		}
	}
	appendWarning := emptyReplaceOnlyWarning
	shapeErr := validateDataPoolRuntimeShape(existingRuntimeRows, incomingRuntimeRows, fileasset.MaxBytes)
	if shapeErr != nil {
		if mode == "append" {
			if appendWarning != "" {
				appendWarning += "; "
			}
			appendWarning += fmt.Sprintf("pool already exposes %d active row(s); appending %d would %v", len(existingRuntimeRows), len(records), shapeErr)
		} else {
			writeError(w, http.StatusBadRequest, shapeErr.Error())
			return
		}
	}
	if dryRun {
		sample := records
		if len(sample) > dataPoolImportPreviewSampleRows {
			sample = sample[:dataPoolImportPreviewSampleRows]
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"dry_run": true, "mode": mode, "columns": columns, "row_count": len(records), "sample_rows": sample, "warnings": warnings, "append_warning": appendWarning,
		})
		return
	}
	if appendWarning != "" {
		writeError(w, http.StatusBadRequest, appendWarning)
		return
	}
	bulk, supported := s.store.(interface {
		BulkReplaceDataPoolRows(poolID string, rows []*storage.SharedDataPoolRow, replace bool) error
	})
	if !supported {
		writeError(w, http.StatusNotImplemented, "bulk data pool imports are not supported by this storage backend")
		return
	}
	rows := make([]*storage.SharedDataPoolRow, 0, len(records))
	for index, record := range records {
		status := ""
		if index < len(recordStatuses) {
			status = recordStatuses[index]
		}
		rows = append(rows, &storage.SharedDataPoolRow{ID: uuid.New().String(), PoolID: pool.ID, Data: record, Status: status})
	}
	if guarded, ok := s.store.(interface {
		BulkAppendDataPoolRowsIfUnchanged(poolID string, rows, expectedExistingRows []*storage.SharedDataPoolRow) (bool, error)
	}); ok && mode == "append" {
		applied, err := guarded.BulkAppendDataPoolRowsIfUnchanged(pool.ID, rows, existingPoolRows)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if !applied {
			writeError(w, http.StatusConflict, "data pool rows changed while the import was being prepared; retry the import")
			return
		}
	} else if guarded, ok := s.store.(interface {
		BulkReplaceDataPoolRowsIfRevision(poolID string, rows []*storage.SharedDataPoolRow, expectedRevision string) (bool, error)
	}); ok && mode == "replace" && existingRevision != "" {
		applied, err := guarded.BulkReplaceDataPoolRowsIfRevision(pool.ID, rows, existingRevision)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if !applied {
			writeError(w, http.StatusConflict, "data pool rows changed while the import was being prepared; retry the import")
			return
		}
	} else if guarded, ok := s.store.(interface {
		BulkReplaceDataPoolRowsIfCount(poolID string, rows []*storage.SharedDataPoolRow, replace bool, expectedExistingRows int) (bool, error)
	}); ok {
		applied, err := guarded.BulkReplaceDataPoolRowsIfCount(pool.ID, rows, mode == "replace", existingRows)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if !applied {
			writeError(w, http.StatusConflict, "data pool rows changed while the import was being prepared; retry the import")
			return
		}
	} else if err := bulk.BulkReplaceDataPoolRows(pool.ID, rows, mode == "replace"); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	deleted := 0
	if mode == "replace" {
		deleted = existingRows
	}
	totalFallback := len(rows)
	if mode == "append" {
		totalFallback += existingRows
	}
	total := s.countDataPoolRows(pool.ID, totalFallback)
	s.recordAuditEvent(r, "data_pool.rows.import", "success", "data_pool", pool.ID, pool.ProjectID, map[string]any{"name": pool.Name, "mode": mode, "imported": len(rows), "deleted": deleted})
	writeJSON(w, http.StatusOK, map[string]any{"imported": len(rows), "deleted": deleted, "mode": mode, "columns": columns, "total": total, "warnings": warnings})
}

// parseDataPoolCSVImport recognizes the APEX export preamble and separates its
// collision-free row-status column from user data. Ordinary CSV files retain
// the historical parser behavior and default imported rows to active.
func parseDataPoolCSVImport(content, delimiterParam, headerParam, columnsParam string) ([]string, []map[string]string, []string, []string, error) {
	return parseDataPoolCSVImportWithOptions(content, delimiterParam, headerParam, columnsParam, dataPoolCSVParseOptions{})
}

func parseDataPoolCSVImportWithOptions(content, delimiterParam, headerParam, columnsParam string, options dataPoolCSVParseOptions) ([]string, []map[string]string, []string, []string, error) {
	content, statusColumn, err := extractDataPoolExportMetadata(content)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	isAPEXExport := statusColumn != ""
	options.decodeSpreadsheetGuards = isAPEXExport
	options.allowEmpty = isAPEXExport
	options.metadataColumns = boolToInt(isAPEXExport)
	columns, rows, warnings, err := parseDataPoolCSVWithOptions(
		content,
		delimiterParam,
		headerParam,
		columnsParam,
		options,
	)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	statuses := make([]string, len(rows))
	if statusColumn == "" {
		return columns, rows, statuses, warnings, nil
	}
	foundStatusColumn := false
	userColumns := make([]string, 0, len(columns)-1)
	for _, column := range columns {
		if column == statusColumn {
			foundStatusColumn = true
			continue
		}
		userColumns = append(userColumns, column)
	}
	if !foundStatusColumn {
		return nil, nil, nil, nil, errors.New("APEX data pool export is missing its declared row-status column")
	}
	for index, row := range rows {
		statuses[index] = row[statusColumn]
		delete(row, statusColumn)
	}
	return userColumns, rows, statuses, warnings, nil
}

func extractDataPoolExportMetadata(content string) (string, string, error) {
	withoutBOM := strings.TrimPrefix(content, "\ufeff")
	reader := csv.NewReader(strings.NewReader(withoutBOM))
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	record, err := reader.Read()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return content, "", nil
		}
		return "", "", fmt.Errorf("parse APEX data pool export metadata: %w", err)
	}
	if len(record) == 0 || record[0] != dataPoolExportMetadataMarker {
		return content, "", nil
	}
	if len(record) != 2 || strings.TrimSpace(record[1]) == "" {
		return "", "", errors.New("APEX data pool export metadata is invalid")
	}
	offset := reader.InputOffset()
	if offset < 0 || offset > int64(len(withoutBOM)) {
		return "", "", errors.New("APEX data pool export metadata offset is invalid")
	}
	return withoutBOM[offset:], record[1], nil
}

type dataPoolCSVParseOptions struct {
	decodeSpreadsheetGuards bool
	allowEmpty              bool
	metadataColumns         int
	maxRecords              int
	maxColumns              int
	maxCells                int
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// parseDataPoolCSV converts an uploaded delimited file into per-row column maps.
// The bool options are retained for the parser's focused unit tests and legacy
// callers: [0] decodes formula guards and [1] permits a header-only document.
func parseDataPoolCSV(content, delimiterParam, headerParam, columnsParam string, options ...bool) ([]string, []map[string]string, []string, error) {
	parseOptions := dataPoolCSVParseOptions{}
	if len(options) > 0 {
		parseOptions.decodeSpreadsheetGuards = options[0]
	}
	if len(options) > 1 {
		parseOptions.allowEmpty = options[1]
	}
	return parseDataPoolCSVWithOptions(content, delimiterParam, headerParam, columnsParam, parseOptions)
}

func parseDataPoolCSVWithOptions(content, delimiterParam, headerParam, columnsParam string, options dataPoolCSVParseOptions) ([]string, []map[string]string, []string, error) {
	if options.maxRecords <= 0 {
		options.maxRecords = maxDataPoolImportRecords
	}
	if options.maxColumns <= 0 {
		options.maxColumns = maxDataPoolImportColumns
	}
	if options.maxCells <= 0 {
		options.maxCells = maxDataPoolImportCells
	}
	if !utf8.ValidString(content) {
		return nil, nil, nil, errors.New("uploaded file must be valid UTF-8")
	}
	content = strings.TrimPrefix(content, "\ufeff")
	if strings.TrimSpace(content) == "" {
		return nil, nil, nil, errors.New("uploaded file is empty")
	}
	var comma rune
	switch strings.ToLower(strings.TrimSpace(delimiterParam)) {
	case "", "auto":
		comma = dsl.DetectDataSourceDelimiter(content)
		if comma == 0 {
			comma = ','
		}
	case "comma":
		comma = ','
	case "tab", `\t`:
		comma = '\t'
	default:
		return nil, nil, nil, errors.New(`delimiter must be "auto", "comma", or "tab"`)
	}
	reader := csv.NewReader(strings.NewReader(content))
	reader.Comma = comma
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	// CSV whitespace is data. Header names are normalized explicitly below,
	// but values such as fixed-width identifiers and padded credentials must
	// retain leading spaces unless the file quotes/transforms them itself.
	reader.TrimLeadingSpace = false
	// Incremental parse so the caps bound what gets allocated, not just what
	// gets stored: a 50 MiB file of one-character cells must fail fast instead
	// of materializing hundreds of MB before the check.
	records := make([][]string, 0, 128)
	var parsedFields int64
	maxParsedFields := int64(options.maxCells)
	if options.metadataColumns > 0 {
		maxParsedFields += int64(options.maxRecords+1) * int64(options.metadataColumns)
	}
	for {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, nil, fmt.Errorf("parse uploaded file: %w", err)
		}
		if len(records) > options.maxRecords {
			return nil, nil, nil, fmt.Errorf("file has more than %d rows; the engine caps a data source at %d", options.maxRecords, options.maxRecords)
		}
		parsedFields += int64(len(record))
		if parsedFields > maxParsedFields {
			return nil, nil, nil, fmt.Errorf("file has more than %d user-data cells including the generated header; the engine caps a data source at %d", options.maxCells, options.maxCells)
		}
		records = append(records, record)
	}
	warnings := make([]string, 0, 2)
	decodeGuardedCell := func(value string) string {
		if options.decodeSpreadsheetGuards {
			return unescapeSpreadsheetGuardedCell(value)
		}
		return value
	}
	var columns []string
	header, err := parseOptionalDataPoolBool(headerParam, true, "header")
	if err != nil {
		return nil, nil, nil, err
	}
	if !header {
		if strings.TrimSpace(columnsParam) == "" {
			return nil, nil, nil, errors.New("columns is required when header=false")
		}
		for index, part := range strings.Split(columnsParam, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				return nil, nil, nil, fmt.Errorf("columns contains a blank name at position %d", index+1)
			}
			columns = append(columns, part)
		}
	} else {
		if len(records) == 0 {
			return nil, nil, nil, errors.New("uploaded file has no header row")
		}
		columns = make([]string, 0, len(records[0]))
		for i, name := range records[0] {
			name = strings.TrimSpace(decodeGuardedCell(name))
			if name == "" {
				name = fmt.Sprintf("col%d", i+1)
				warnings = append(warnings, fmt.Sprintf("blank header in column %d imported as %q", i+1, name))
			}
			columns = append(columns, name)
		}
		records = records[1:]
	}
	seen := make(map[string]struct{}, len(columns))
	for _, column := range columns {
		// Data-pool row keys and engine lookups are case-sensitive. Preserve
		// that contract here so an exported pool containing both "User" and
		// "user" can be restored without rejecting its own CSV header.
		if _, dup := seen[column]; dup {
			return nil, nil, nil, fmt.Errorf("duplicate column name %q", column)
		}
		seen[column] = struct{}{}
	}
	if len(records) == 0 && !options.allowEmpty {
		return nil, nil, nil, errors.New("uploaded file has no data rows")
	}
	if len(records) > options.maxRecords {
		return nil, nil, nil, fmt.Errorf("file has %d rows; the engine caps a data source at %d", len(records), options.maxRecords)
	}
	userColumnCount := len(columns) - options.metadataColumns
	if userColumnCount < 0 {
		userColumnCount = 0
	}
	if userColumnCount > options.maxColumns {
		return nil, nil, nil, fmt.Errorf("file has %d user-data columns; the engine caps a data source at %d", userColumnCount, options.maxColumns)
	}
	userCellCount := int64(len(records)+1) * int64(userColumnCount)
	if userCellCount > int64(options.maxCells) {
		return nil, nil, nil, fmt.Errorf("file has %d user-data cells including the generated header; the engine caps a data source at %d", userCellCount, options.maxCells)
	}
	ragged := 0
	rows := make([]map[string]string, 0, len(records))
	for _, record := range records {
		if len(record) > len(columns) {
			ragged++
		}
		row := make(map[string]string, len(columns))
		for i, column := range columns {
			if i < len(record) {
				row[column] = decodeGuardedCell(record[i])
			} else {
				row[column] = ""
			}
		}
		rows = append(rows, row)
	}
	if ragged > 0 {
		warnings = append(warnings, fmt.Sprintf("%d row(s) carry more fields than the header; extra fields were dropped", ragged))
	}
	return columns, rows, warnings, nil
}

func parseOptionalDataPoolBool(raw string, defaultValue bool, field string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return defaultValue, nil
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be true or false", field)
	}
}

func validateDataPoolImportFileSize(size int64, markedAPEXExport ...bool) error {
	marked := len(markedAPEXExport) > 0 && markedAPEXExport[0]
	return validateDataPoolImportFileSizeAgainst(
		size,
		fileasset.MaxBytes,
		dataPoolExportEnvelopeOverheadBytes,
		marked,
	)
}

func validateDataPoolImportFileSizeAgainst(size, userDataMax, envelopeOverhead int64, markedAPEXExport bool) error {
	maxBytes := userDataMax
	limitName := "data-source"
	if markedAPEXExport {
		maxBytes += envelopeOverhead
		limitName = "APEX-export envelope"
	}
	if size > maxBytes {
		return fmt.Errorf("uploaded file exceeds the %d-byte %s limit", maxBytes, limitName)
	}
	return nil
}

// validateDataPoolRuntimeShape checks the exact flattened representation used
// at run materialization. The engine counts the generated header as one record
// and one cell per column, and reads inline CSV through fileasset.MaxBytes.
func validateDataPoolRuntimeShape(existing []*storage.SharedDataPoolRow, incoming []map[string]string, maxBytes int64) error {
	columnSet := make(map[string]struct{})
	addColumns := func(data map[string]string) {
		for key := range data {
			key = strings.TrimSpace(key)
			if key != "" {
				columnSet[key] = struct{}{}
			}
		}
	}
	for _, row := range existing {
		if row != nil {
			addColumns(row.Data)
		}
	}
	for _, row := range incoming {
		addColumns(row)
	}
	columns := make([]string, 0, len(columnSet))
	for column := range columnSet {
		columns = append(columns, column)
	}
	sort.Strings(columns)

	rowCount := len(existing) + len(incoming)
	if rowCount > 0 && len(columns) == 0 {
		return errors.New("data pool rows must include at least one column")
	}
	if err := validateDataPoolRuntimeDimensions(rowCount, len(columns)); err != nil {
		return err
	}
	counter := &dataPoolCSVLimitWriter{max: maxBytes}
	writer := csv.NewWriter(counter)
	if err := writer.Write(columns); err != nil {
		return fmt.Errorf("produce an executable CSV larger than %d bytes", maxBytes)
	}
	values := make([]string, len(columns))
	writeValues := func(data map[string]string) error {
		for index, column := range columns {
			values[index] = canonicalDataPoolRowValue(data, column)
		}
		return writer.Write(values)
	}
	for _, row := range existing {
		if row != nil {
			if err := writeValues(row.Data); err != nil {
				return fmt.Errorf("produce an executable CSV larger than %d bytes", maxBytes)
			}
		}
	}
	for _, row := range incoming {
		if err := writeValues(row); err != nil {
			return fmt.Errorf("produce an executable CSV larger than %d bytes", maxBytes)
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return fmt.Errorf("produce an executable CSV larger than %d bytes", maxBytes)
	}
	return nil
}

func validateExecutableDataPoolRows(rows []*storage.SharedDataPoolRow) error {
	active := activeDataPoolRows(rows)
	if len(active) == 0 {
		return nil
	}
	return validateDataPoolRuntimeShape(active, nil, fileasset.MaxBytes)
}

func validateDataPoolRuntimeDimensions(rowCount, columnCount int) error {
	if rowCount > maxDataPoolImportRecords {
		return fmt.Errorf("exceed the %d-data-row engine cap", maxDataPoolImportRecords)
	}
	if columnCount > maxDataPoolImportColumns {
		return fmt.Errorf("exceed the %d-column engine cap", maxDataPoolImportColumns)
	}
	records := int64(rowCount) + 1 // generated header
	cells := records * int64(columnCount)
	if cells > int64(maxDataPoolImportCells) {
		return fmt.Errorf("produce %d cells including the generated header; the engine cap is %d cells", cells, maxDataPoolImportCells)
	}
	return nil
}

type dataPoolCSVLimitWriter struct {
	written int64
	max     int64
}

func (w *dataPoolCSVLimitWriter) Write(data []byte) (int, error) {
	if w.max <= 0 || int64(len(data)) > w.max-w.written {
		return 0, errDataPoolCSVByteLimit
	}
	w.written += int64(len(data))
	return len(data), nil
}

// unescapeSpreadsheetGuardedCell reverses spreadsheetSafeCSVCell for files
// explicitly identified as APEX exports. It must never run on an arbitrary CSV:
// a literal leading apostrophe before a formula-like value is valid user data.
func unescapeSpreadsheetGuardedCell(value string) string {
	leadingApostrophes := 0
	for leadingApostrophes < len(value) && value[leadingApostrophes] == '\'' {
		leadingApostrophes++
	}
	if leadingApostrophes == 0 {
		return value
	}
	// Data-pool exports add one marker before any existing leading-apostrophe
	// run whose remainder is formula-like. Remove exactly that marker so every
	// number of real leading apostrophes round-trips without ambiguity.
	if spreadsheetFormulaLike(value[leadingApostrophes:]) {
		return value[1:]
	}
	return value
}

func spreadsheetFormulaLike(value string) bool {
	trimmed := strings.TrimLeftFunc(value, func(char rune) bool {
		return unicode.IsSpace(char) || char == '\ufeff'
	})
	if trimmed == "" {
		return false
	}
	switch trimmed[0] {
	case '=', '+', '-', '@':
		return true
	default:
		return false
	}
}

func spreadsheetSafeDataPoolCSVCell(value string) string {
	leadingApostrophes := 0
	for leadingApostrophes < len(value) && value[leadingApostrophes] == '\'' {
		leadingApostrophes++
	}
	if leadingApostrophes > 0 && spreadsheetFormulaLike(value[leadingApostrophes:]) {
		return "'" + value
	}
	return spreadsheetSafeCSVCell(value)
}

// canonicalDataPoolRowValue mirrors the pool materializer's trimmed column
// union while preserving values from legacy rows whose raw JSON keys carry
// surrounding whitespace. Exact canonical keys win; otherwise the
// lexicographically first matching raw key makes ambiguous legacy data stable.
func canonicalDataPoolRowValue(data map[string]string, column string) string {
	if value, ok := data[column]; ok {
		return value
	}
	var matchedKey string
	for key := range data {
		if strings.TrimSpace(key) != column {
			continue
		}
		if matchedKey == "" || key < matchedKey {
			matchedKey = key
		}
	}
	return data[matchedKey]
}

// handleExportDataPoolRows streams the pool as CSV. The header is the sorted
// union of row keys, which requires a column pass before the row pass; rows are
// paged so the whole pool is never buffered. ?status=active limits the export
// to rows eligible for execution.
// OpenAPI-Responses: 200, 404, 413
func (s *Server) handleExportDataPoolRows(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-APEXLoad-Sensitivity", "raw")
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	pool, err := s.store.GetDataPool(r.PathValue("id"))
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		writeError(w, status, "data pool not found")
		return
	}
	statusFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))
	if statusFilter == "" {
		statusFilter = "all"
	}
	if statusFilter != "all" && statusFilter != "active" {
		writeError(w, http.StatusBadRequest, `status must be "active" or "all"`)
		return
	}
	auditDetails := map[string]any{"name": pool.Name, "status": statusFilter}
	auditOperationID, err := s.beginRequiredAuditOperation(
		r,
		"data_pool.rows.export",
		"data_pool",
		pool.ID,
		pool.ProjectID,
		auditDetails,
	)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "data pool export audit is unavailable; no rows were exported")
		return
	}
	completeExportAudit := func(status, outcome, failure string, poolRows int) error {
		details := map[string]any{
			"name":      pool.Name,
			"status":    statusFilter,
			"pool_rows": poolRows,
		}
		if failure != "" {
			details["error"] = failure
		}
		return s.completeRequiredAuditOperation(
			r,
			auditOperationID,
			"data_pool.rows.export",
			status,
			outcome,
			"data_pool",
			pool.ID,
			pool.ProjectID,
			details,
		)
	}
	stateReader, ok := s.store.(dataPoolRowsPageStateReader)
	if !ok {
		if auditErr := completeExportAudit("failed", "unsupported", "consistent export is unavailable", 0); auditErr != nil {
			writeError(w, http.StatusServiceUnavailable, "data pool export failed and its audit outcome could not be persisted")
			return
		}
		writeError(w, http.StatusServiceUnavailable, "storage backend does not support consistent data pool export")
		return
	}
	expectedRevision, poolRowCount, err := stateReader.DataPoolRowsPageState(pool.ID)
	if err != nil {
		if auditErr := completeExportAudit("failed", "snapshot_failed", "export revision scan failed", 0); auditErr != nil {
			writeError(w, http.StatusServiceUnavailable, "data pool export failed and its audit outcome could not be persisted")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to establish a data pool export snapshot")
		return
	}
	errRowsChangedDuringExport := errors.New("data pool rows changed during export")
	verifyRevision := func() error {
		currentRevision, currentCount, revisionErr := stateReader.DataPoolRowsPageState(pool.ID)
		if revisionErr != nil {
			return fmt.Errorf("verify data pool export snapshot: %w", revisionErr)
		}
		if currentRevision != expectedRevision || currentCount != poolRowCount {
			return errRowsChangedDuringExport
		}
		return nil
	}
	pager, hasKeyset := s.store.(dataPoolRowsPager)
	forEachRow := func(fn func(*storage.SharedDataPoolRow) error) error {
		afterKey := ""
		afterID := ""
		offset := 0
		for {
			// Stop doing DB work for a socket the client already closed.
			if err := r.Context().Err(); err != nil {
				return err
			}
			var page []*storage.SharedDataPoolRow
			var err error
			if hasKeyset {
				page, afterKey, err = pager.ListDataPoolRowsPageAfter(pool.ID, afterKey, afterID, dataPoolExportPageSize)
			} else {
				page, err = s.listDataPoolRowsPage(pool.ID, dataPoolExportPageSize, offset)
			}
			if err != nil {
				return err
			}
			for _, row := range page {
				if row == nil {
					continue
				}
				if statusFilter == "active" && !isActiveDataPoolRowStatus(row.Status) {
					continue
				}
				if err := fn(row); err != nil {
					return err
				}
			}
			if len(page) < dataPoolExportPageSize {
				return nil
			}
			if hasKeyset {
				last := page[len(page)-1]
				if last == nil {
					return errors.New("data pool row pager returned a nil page boundary")
				}
				nextID := last.ID
				if nextID == afterID {
					return errors.New("data pool row pager did not advance")
				}
				afterID = nextID
			} else {
				offset += dataPoolExportPageSize
			}
		}
	}
	columnSet := map[string]struct{}{}
	exportedRowCount := 0
	if err := forEachRow(func(row *storage.SharedDataPoolRow) error {
		exportedRowCount++
		for key := range row.Data {
			key = strings.TrimSpace(key)
			if key != "" {
				columnSet[key] = struct{}{}
			}
		}
		return nil
	}); err != nil {
		if r.Context().Err() != nil {
			_ = completeExportAudit("failed", "client_canceled", "client canceled export", poolRowCount)
			return
		}
		if auditErr := completeExportAudit("failed", "scan_failed", "export scan failed", poolRowCount); auditErr != nil {
			writeError(w, http.StatusServiceUnavailable, "data pool export failed and its audit outcome could not be persisted")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// The CSV header is derived by a separate pass. Fence that pass before
	// committing response headers so a concurrent new column fails with 409
	// instead of being silently omitted from an apparently valid backup.
	if err := verifyRevision(); err != nil {
		if auditErr := completeExportAudit("failed", "snapshot_changed", "data pool changed during export", poolRowCount); auditErr != nil {
			writeError(w, http.StatusServiceUnavailable, "data pool export failed and its audit outcome could not be persisted")
			return
		}
		if errors.Is(err, errRowsChangedDuringExport) {
			writeError(w, http.StatusConflict, "data pool rows changed while the export was being prepared; retry the export")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to verify the data pool export snapshot")
		}
		return
	}
	columns := make([]string, 0, len(columnSet))
	for key := range columnSet {
		columns = append(columns, key)
	}
	sort.Strings(columns)
	statusColumn := dataPoolExportStatusColumn(columnSet)

	// A successful export must fit the exact dimensions and byte envelope that
	// the marked import path accepts. This pass also accounts for free-text row
	// statuses and formula guards, neither of which participates in the engine
	// shape. Keep it behind the revision fence and before committing a 200.
	if dimensionErr := validateDataPoolRuntimeDimensions(exportedRowCount, len(columns)); dimensionErr != nil {
		if auditErr := completeExportAudit("failed", "format_limit", dimensionErr.Error(), poolRowCount); auditErr != nil {
			writeError(w, http.StatusServiceUnavailable, "data pool export failed and its audit outcome could not be persisted")
			return
		}
		writeError(w, http.StatusRequestEntityTooLarge, "data pool cannot be exported as a restorable APEX backup: "+dimensionErr.Error())
		return
	}
	counter := &dataPoolCSVLimitWriter{max: dataPoolMaxMarkedCSVBytes}
	preflightErr := writeDataPoolExportCSV(counter, columns, statusColumn, forEachRow)
	if preflightErr == nil {
		preflightErr = verifyRevision()
	}
	if preflightErr != nil {
		if r.Context().Err() != nil {
			_ = completeExportAudit("failed", "client_canceled", "client canceled export", poolRowCount)
			return
		}
		if errors.Is(preflightErr, errDataPoolCSVByteLimit) {
			if auditErr := completeExportAudit("failed", "format_limit", "APEX backup exceeds the marked import byte limit", poolRowCount); auditErr != nil {
				writeError(w, http.StatusServiceUnavailable, "data pool export failed and its audit outcome could not be persisted")
				return
			}
			writeError(w, http.StatusRequestEntityTooLarge, "data pool cannot be exported as a restorable APEX backup: marked export byte limit exceeded")
			return
		}
		if auditErr := completeExportAudit("failed", "snapshot_changed", "data pool changed during export preflight", poolRowCount); auditErr != nil {
			writeError(w, http.StatusServiceUnavailable, "data pool export failed and its audit outcome could not be persisted")
			return
		}
		if errors.Is(preflightErr, errRowsChangedDuringExport) {
			writeError(w, http.StatusConflict, "data pool rows changed while the export was being prepared; retry the export")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to preflight the data pool export")
		}
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+dataPoolExportFilename(pool)+"\"")
	rowErr := writeDataPoolExportCSV(w, columns, statusColumn, forEachRow)
	if rowErr == nil {
		rowErr = verifyRevision()
	}
	if rowErr != nil {
		// The 200 and header are already on the wire; ending the chunked body
		// cleanly would hand the client a silently truncated or cross-revision
		// file. Abort the connection so the download fails loudly.
		outcome := "stream_failed"
		failure := "export stream truncated"
		if errors.Is(rowErr, errRowsChangedDuringExport) {
			outcome = "snapshot_changed"
			failure = "data pool changed during export"
		} else if r.Context().Err() != nil {
			outcome = "client_canceled"
			failure = "client canceled export"
		}
		_ = completeExportAudit("failed", outcome, failure, poolRowCount)
		if r.Context().Err() != nil {
			return
		}
		panic(http.ErrAbortHandler)
	}
	if auditErr := completeExportAudit("success", "exported", "", poolRowCount); auditErr != nil {
		// Bytes may already have reached the socket. Abort rather than handing
		// the client a valid-looking governed export with no terminal audit.
		panic(http.ErrAbortHandler)
	}
}

func writeDataPoolExportCSV(destination io.Writer, columns []string, statusColumn string, forEachRow func(func(*storage.SharedDataPoolRow) error) error) error {
	exportColumns := append(append([]string(nil), columns...), statusColumn)
	writer := csv.NewWriter(destination)
	if err := writer.Write([]string{dataPoolExportMetadataMarker, statusColumn}); err != nil {
		return err
	}
	header := make([]string, len(exportColumns))
	for index, column := range exportColumns {
		header[index] = spreadsheetSafeDataPoolCSVCell(column)
	}
	if err := writer.Write(header); err != nil {
		return err
	}
	values := make([]string, len(exportColumns))
	if err := forEachRow(func(row *storage.SharedDataPoolRow) error {
		for index, column := range columns {
			values[index] = spreadsheetSafeDataPoolCSVCell(canonicalDataPoolRowValue(row.Data, column))
		}
		values[len(values)-1] = spreadsheetSafeDataPoolCSVCell(row.Status)
		return writer.Write(values)
	}); err != nil {
		return err
	}
	writer.Flush()
	return writer.Error()
}

func dataPoolExportStatusColumn(userColumns map[string]struct{}) string {
	candidate := dataPoolExportStatusColumnBase
	for suffix := 2; ; suffix++ {
		if _, exists := userColumns[candidate]; !exists {
			return candidate
		}
		candidate = fmt.Sprintf("%s_%d", dataPoolExportStatusColumnBase, suffix)
	}
}

func dataPoolExportFilename(pool *storage.SharedDataPool) string {
	name := strings.TrimSpace(pool.Name)
	if name == "" {
		name = pool.ID
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		}
	}
	if b.Len() == 0 {
		return "data-pool.apexload.csv"
	}
	return b.String() + ".apexload.csv"
}

// countDataPoolRows returns the pool's total row count, falling back to the
// unbounded listing for backends without the count fast path.
func (s *Server) countDataPoolRows(poolID string, fallback int) int {
	count, err := s.dataPoolRowCount(poolID)
	if err != nil {
		return fallback
	}
	return count
}

func (s *Server) dataPoolRowCount(poolID string) (int, error) {
	if counter, ok := s.store.(interface{ CountDataPoolRows(string) (int, error) }); ok {
		return counter.CountDataPoolRows(poolID)
	}
	rows, err := s.store.ListDataPoolRows(poolID)
	if err != nil {
		return 0, err
	}
	return len(rows), nil
}

// errDataPoolArchived enforces the governance contract that archiving prevents
// new use: archived pools cannot be bound to scenarios or materialized for runs.
func errDataPoolArchived(pool *storage.SharedDataPool) error {
	if pool == nil || pool.ArchivedAt == nil {
		return nil
	}
	name := strings.TrimSpace(pool.Name)
	if name == "" {
		name = pool.ID
	}
	return fmt.Errorf("data pool %q is archived; restore it before binding or running it", name)
}

// isActiveDataPoolRowStatus reports whether a row participates in execution.
// Status is legacy free text, so exclusion is an explicit blocklist: a
// deployment that used the field as a tag (e.g. "ready") must not silently
// lose rows from runs, while rows explicitly marked spent stay excluded.
func isActiveDataPoolRowStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "consumed", "used", "disabled", "inactive", "exhausted":
		return false
	default:
		return true
	}
}

// activeDataPoolRows returns the rows eligible for execution snapshots.
func activeDataPoolRows(rows []*storage.SharedDataPoolRow) []*storage.SharedDataPoolRow {
	active := make([]*storage.SharedDataPoolRow, 0, len(rows))
	for _, row := range rows {
		if row != nil && isActiveDataPoolRowStatus(row.Status) {
			active = append(active, row)
		}
	}
	return active
}
