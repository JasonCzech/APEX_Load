package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/livetable"
	"github.com/jasonczech/apexload/pkg/storage"
)

type liveTableView struct {
	ID         string `json:"id"`
	Alias      string `json:"alias"`
	Profile    string `json:"profile"`
	Durability string `json:"durability"`
	State      string `json:"state"`
	*livetable.Stats
	Finalization *storage.LiveTableFinalizationResult `json:"finalization,omitempty"`
}

// Terminal test hydration retains these immutable bindings. An omitted end
// mode on an explicit live binding has always meant discard. Missing bindings
// are not permission to invent a new output policy.
func liveTableConfiguredEndMode(test *ManagedTest, id string) (string, bool) {
	for _, binding := range test.DataPools {
		if binding.AccessMode != "live" || liveTableID(test.ID, binding.Alias) != id {
			continue
		}
		if binding.LiveOptions == nil || binding.LiveOptions.EndMode == "" {
			return "discard", true
		}
		switch binding.LiveOptions.EndMode {
		case "discard", "save_as_new", "write_back":
			return binding.LiveOptions.EndMode, true
		}
		return "", false
	}
	return "", false
}

func (s *Server) liveTableRunRequest(w http.ResponseWriter, r *http.Request) (*ManagedTest, bool) {
	test, ok := s.runner.GetTestSummary(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "run not found")
		return nil, false
	}
	if !requireAuthorizedProjectMatch(w, r, test.ProjectID) {
		return nil, false
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	return test, true
}
func (s *Server) handleListLiveTables(w http.ResponseWriter, r *http.Request) {
	test, ok := s.liveTableRunRequest(w, r)
	if !ok {
		return
	}
	views := []liveTableView{}
	if run := s.runner.liveRun(test.ID); run != nil && run.projectID == test.ProjectID {
		run.mu.RLock()
		for _, rt := range run.tables {
			stats := rt.summary
			if rt.table != nil {
				stats = rt.table.Stats()
			}
			state := "active"
			if stats.Unavailable || (!run.closed && !s.runner.executionLeaseTermLocallyCurrent(run.term)) {
				state = "unavailable"
			} else if run.closed {
				state = "closed"
			} else if !run.ready {
				state = "recovering"
			}
			var finalization *storage.LiveTableFinalizationResult
			if rt.final != nil {
				copy := *rt.final
				finalization = &copy
			}
			views = append(views, liveTableView{ID: rt.record.ID, Alias: rt.binding.Alias, Profile: rt.binding.LiveOptions.Profile, Durability: rt.binding.LiveOptions.Durability, State: state, Stats: &stats, Finalization: finalization})
		}
		run.mu.RUnlock()
	} else if hasLiveDataPools(test.DataPools) {
		state, _, err := s.runner.liveStore()
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "live table metadata is unavailable")
			return
		}
		records, err := state.ListLiveTablesForRun(r.Context(), test.ID, test.ProjectID)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "live table metadata is unavailable")
			return
		}
		_, seedStore, _ := s.runner.liveStore()
		for _, record := range records {
			view := liveTableView{ID: record.ID, Alias: record.Alias, Profile: record.Profile, Durability: record.Durability, State: "unavailable"}
			if record.State == "closed" {
				view.State = "closed"
			}
			if seedStore != nil {
				final, err := seedStore.GetLiveTableFinalization(r.Context(), record.ID, test.ProjectID)
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					writeError(w, http.StatusServiceUnavailable, "live table finalization status is unavailable")
					return
				}
				view.Finalization = final
				if final == nil && record.State == "closed" {
					// A crash or an evicted runtime may leave a closed checkpoint
					// without a committed pool outcome. Keep explicit recovery visible.
					mode, known := liveTableConfiguredEndMode(test, record.ID)
					view.Finalization = &storage.LiveTableFinalizationResult{TableID: record.ID, Mode: mode, State: "pending", Reason: "reconciliation_required"}
					if known && mode == "discard" {
						// Close already applied the discard policy even if a crash
						// prevented recording the separate finalization outcome.
						view.Finalization.State = "complete"
						view.Finalization.Reason = "discarded"
					}
				}
			}
			views = append(views, view)
		}
	}
	sort.Slice(views, func(i, j int) bool { return views[i].Alias < views[j].Alias })
	writeJSON(w, http.StatusOK, map[string]any{"tables": views, "updated_at": time.Now().UTC()})
}

// Inspect never dispatches a runtime operation and never registers an operation
// identity. A view of an active table is served only by its current owner.
func (s *Server) handleLiveTableRows(w http.ResponseWriter, r *http.Request) {
	test, ok := s.liveTableRunRequest(w, r)
	if !ok {
		return
	}
	// Keep only one raw response buffer in flight through the network write.
	// This reservation never blocks runtime operations or table finalization.
	if !s.runner.liveTablesResponseMu.TryLock() {
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusTooManyRequests, "live table preview or export is busy; retry")
		return
	}
	defer s.runner.liveTablesResponseMu.Unlock()
	offset, err := strconv.Atoi(defaultQuery(r, "offset", "0"))
	if err != nil || offset < 0 || offset > 100000 {
		writeError(w, 400, "offset must be between 0 and 100000")
		return
	}
	limit, err := strconv.Atoi(defaultQuery(r, "limit", "100"))
	if err != nil || limit < 1 || limit > 100 {
		writeError(w, 400, "limit must be between 1 and 100")
		return
	}
	revision, err := strconv.ParseUint(defaultQuery(r, "revision", "0"), 10, 64)
	if err != nil {
		writeError(w, 400, "invalid live table revision")
		return
	}
	readCurrent := s.runner.liveTableReadGuard(test.ID)
	table, release, err := s.runner.inspectLiveTable(r.Context(), test, r.PathValue("tableID"))
	if err != nil {
		writeError(w, 409, "live table data is unavailable on this controller")
		return
	}
	defer release()
	page, err := liveTableInspectPage(table, offset, limit, revision)
	current := readCurrent()
	release()
	if err != nil {
		writeError(w, 409, "live table changed or requested page exceeds the result budget; refresh and retry")
		return
	}
	if !current {
		writeError(w, 409, "live table owner changed; refresh and retry")
		return
	}
	w.Header().Set("X-APEXLoad-Data-Sensitivity", "raw")
	writeJSON(w, 200, page)
}
func defaultQuery(r *http.Request, key, fallback string) string {
	if value := r.URL.Query().Get(key); value != "" {
		return value
	}
	return fallback
}

func (s *Server) handleExportLiveTable(w http.ResponseWriter, r *http.Request) {
	test, ok := s.liveTableRunRequest(w, r)
	if !ok {
		return
	}
	// Keep only one raw response buffer in flight through the network write.
	// This reservation never blocks runtime operations or table finalization.
	if !s.runner.liveTablesResponseMu.TryLock() {
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusTooManyRequests, "live table preview or export is busy; retry")
		return
	}
	defer s.runner.liveTablesResponseMu.Unlock()
	id := r.PathValue("tableID")
	readCurrent := s.runner.liveTableReadGuard(test.ID)
	table, release, err := s.runner.inspectLiveTable(r.Context(), test, id)
	if err != nil {
		writeError(w, 409, "live table export is unavailable on this controller")
		return
	}
	defer release()
	audit, err := s.beginRequiredAuditOperation(r, "live_table.rows.export", "live_table", id, test.ProjectID, map[string]any{"run_id": test.ID})
	if err != nil {
		release()
		writeError(w, 503, "live table export audit is unavailable")
		return
	}
	complete := func(outcome string, count int) error {
		return s.completeRequiredAuditOperation(r, audit, "live_table.rows.export", outcome, outcome, "live_table", id, test.ProjectID, map[string]any{"run_id": test.ID, "row_count": count})
	}
	revision := table.Stats().Revision
	if raw := r.URL.Query().Get("revision"); raw != "" {
		expected, parseErr := strconv.ParseUint(raw, 10, 64)
		if parseErr != nil || expected != revision {
			release()
			_ = complete("failed", 0)
			writeError(w, 409, "live table revision changed; refresh before exporting")
			return
		}
	}
	rows, columns, err := liveTableExportRows(table, revision)
	current := readCurrent()
	release()
	if err != nil {
		_ = complete("failed", 0)
		writeError(w, 409, "live table changed or exceeds the export budget; retry after the run stops")
		return
	}
	body, err := json.Marshal(map[string]any{"format": "apexload-live-table-v1", "run_id": test.ID, "table_id": id, "revision": revision, "columns": columns, "rows": rows})
	if err != nil || len(body) > 16<<20 {
		_ = complete("failed", 0)
		writeError(w, 409, "live table export exceeds its byte budget")
		return
	}
	if !current {
		_ = complete("failed", 0)
		writeError(w, 409, "live table owner changed; refresh before exporting")
		return
	}
	if err := complete("succeeded", len(rows)); err != nil {
		writeError(w, 503, "live table export audit is unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="live-table.json"`)
	w.Header().Set("X-APEXLoad-Data-Sensitivity", "raw")
	w.WriteHeader(200)
	_, _ = w.Write(body)
}
func liveTableExportRows(table *livetable.Table, revision uint64) ([]livetable.Row, []string, error) {
	rows := []livetable.Row{}
	var columns []string
	bytes := 0
	for offset := 0; ; {
		page, err := liveTableInspectPage(table, offset, 100, revision)
		if err != nil {
			return nil, nil, err
		}
		columns = page.Columns
		for _, row := range page.Rows {
			for k, v := range row.Values {
				bytes += len(k) + len(v)
			}
			if bytes > 8<<20 || len(rows) >= 100000 {
				return nil, nil, errors.New("live table export budget exceeded")
			}
			rows = append(rows, row)
		}
		if len(page.Rows) == 0 {
			break
		}
		offset += len(page.Rows)
	}
	if table.Stats().Revision != revision {
		return nil, nil, errors.New("live table changed")
	}
	return rows, columns, nil
}

func (tr *TestRunner) liveTableReadGuard(testID string) func() bool {
	run := tr.liveRun(testID)
	if run == nil {
		return func() bool { return true }
	}
	run.mu.RLock()
	closed, term := run.closed, run.term
	run.mu.RUnlock()
	return func() bool { return closed || tr.executionLeaseTermLocallyCurrent(term) }
}

func (tr *TestRunner) inspectLiveTable(ctx context.Context, test *ManagedTest, id string) (*livetable.Table, func(), error) {
	// One bounded raw-data copy/decryption at a time. Reject overload instead
	// of queueing unbounded exports or holding a run lock across slow clients.
	if !tr.liveTablesInspectMu.TryLock() {
		return nil, nil, errors.New("live table inspection is busy; retry")
	}
	release := sync.OnceFunc(tr.liveTablesInspectMu.Unlock)
	if run := tr.liveRun(test.ID); run != nil && run.projectID == test.ProjectID {
		run.mu.RLock()
		rt := run.tables[id]
		if rt != nil && rt.table != nil && (run.closed || tr.executionLeaseTermLocallyCurrent(run.term)) {
			table := rt.table
			run.mu.RUnlock()
			return table, release, nil
		}
		run.mu.RUnlock()
	}
	if !isTerminalTestStatus(test.Status) {
		release()
		return nil, nil, errors.New("active live table owner is unavailable")
	}
	rt, err := tr.loadTerminalLiveTable(ctx, test, id)
	if err != nil {
		release()
		return nil, nil, err
	}
	return rt.table, release, nil
}
func (tr *TestRunner) loadTerminalLiveTable(ctx context.Context, test *ManagedTest, id string) (*runLiveTable, error) {
	state, _, err := tr.liveStore()
	if err != nil {
		return nil, err
	}
	recovery, err := state.ReadLiveTableRecovery(ctx, id, test.ProjectID)
	if err != nil {
		return nil, err
	}
	if recovery.Table.TestID != test.ID || recovery.Table.State != "closed" {
		return nil, errors.New("live table is not closed")
	}
	var metadata liveTablePrivateMetadata
	if json.Unmarshal([]byte(recovery.Table.SeedMetadata), &metadata) != nil || metadata.Version != 1 {
		return nil, errors.New("invalid live table metadata")
	}
	cipher, err := restoreLiveTableCipher(ctx, tr.continuityKeyWrapper, id, metadata.Key)
	if err != nil {
		return nil, err
	}
	var manifest liveTableSeedManifest
	if err := cipher.open(metadata.Seed, liveTableAAD(id, "seed", 0), &manifest); err != nil {
		return nil, err
	}
	var snapshot livetable.Snapshot
	if err := cipher.open(recovery.Checkpoint, liveTableAAD(id, "checkpoint", recovery.Table.CheckpointRevision), &snapshot); err != nil {
		return nil, err
	}
	table, err := livetable.Restore(snapshot, nil)
	if err != nil {
		return nil, err
	}
	if snapshot.Revision != recovery.Table.Revision || !table.Stats().Closed {
		return nil, errors.New("terminal live table snapshot is incomplete")
	}
	return &runLiveTable{table: table, binding: manifest.Binding, seed: manifest.Identity, cipher: cipher, record: recovery.Table}, nil
}
func (s *Server) handleFinalizeLiveTable(w http.ResponseWriter, r *http.Request) {
	test, ok := s.liveTableRunRequest(w, r)
	if !ok {
		return
	}
	if !isTerminalTestStatus(test.Status) {
		writeError(w, 409, "live table output can be saved only after the run stops")
		return
	}
	if !s.requireDomainFeatureForProject(w, test.ProjectID, "data.live_tables") {
		return
	}
	// The copied output remains live across storage and audit I/O. Reserve the
	// same bounded raw-data slot through the entire reconciliation request.
	if !s.runner.liveTablesResponseMu.TryLock() {
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusTooManyRequests, "live table data request is busy; retry")
		return
	}
	defer s.runner.liveTablesResponseMu.Unlock()
	var req struct {
		OutputPoolName string `json:"output_pool_name"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if len(req.OutputPoolName) == 0 || len(req.OutputPoolName) > 256 {
		writeError(w, 400, "output_pool_name must contain 1–256 bytes")
		return
	}
	id := r.PathValue("tableID")
	mode, known := liveTableConfiguredEndMode(test, id)
	if !known {
		writeError(w, http.StatusConflict, "live table output policy is unavailable")
		return
	}
	if mode == "discard" {
		writeError(w, http.StatusConflict, "live table output is already finalized under its discard policy")
		return
	}
	// Explicit operator recovery has a fresh authenticated permission check from
	// route RBAC. The source run's former principal does not authorize this call.
	table, release, err := s.runner.inspectLiveTable(r.Context(), test, id)
	if err != nil {
		writeError(w, 409, "live table output snapshot is unavailable")
		return
	}
	defer func() {
		if release != nil {
			release()
		}
	}()
	rows, columns, err := liveTableExportRows(table, table.Stats().Revision)
	release()
	if err != nil {
		writeError(w, 409, "live table output snapshot could not be read")
		return
	}
	state, seedStore, err := s.runner.liveStore()
	if err != nil {
		writeError(w, 503, "live table persistence is unavailable")
		return
	}
	term, err := s.runner.captureExecutionLeaseTerm()
	if err != nil {
		writeError(w, 503, "execution leader is unavailable")
		return
	}
	if term.valid() {
		if err := state.ClaimLiveTable(r.Context(), id, liveStorageTerm(term)); err != nil {
			writeError(w, 409, "live table owner changed")
			return
		}
	}
	input := storage.LiveTableFinalizationRequest{TableID: id, ProjectID: test.ProjectID, Revision: table.Stats().Revision, Mode: "save_as_new", OutputPoolName: req.OutputPoolName, Schema: map[string]string{}}
	for _, column := range columns {
		input.Schema[column] = "string"
	}
	for _, row := range rows {
		status := "available"
		if row.State == "quarantined" {
			status = "disabled"
		}
		input.Rows = append(input.Rows, &storage.SharedDataPoolRow{ID: row.ID, Data: row.Values, Status: status, Version: int(row.Version)})
	}
	if err := validateExecutableDataPoolRows(input.Rows); err != nil {
		writeError(w, http.StatusConflict, "live table output exceeds executable data pool limits")
		return
	}
	audit, err := s.beginRequiredAuditOperation(r, "live_table.finalize", "live_table", id, test.ProjectID, map[string]any{"run_id": test.ID, "mode": "save_as_new"})
	if err != nil {
		writeError(w, 503, "live table finalization audit is unavailable")
		return
	}
	result, err := seedStore.GetLiveTableFinalization(r.Context(), id, test.ProjectID)
	if err == nil && result != nil && result.State == "complete" && (result.Mode != "save_as_new" || result.OutputPoolID == "") {
		_ = s.completeRequiredAuditOperation(r, audit, "live_table.finalize", "failed", "already_finalized", "live_table", id, test.ProjectID, nil)
		writeError(w, 409, "live table output is already finalized under its original policy")
		return
	}
	if err != nil || result == nil || result.State != "complete" {
		result, err = seedStore.FinalizeLiveTable(r.Context(), input, liveStorageTerm(term))
	}
	if err != nil {
		_ = s.completeRequiredAuditOperation(r, audit, "live_table.finalize", "failed", "conflict", "live_table", id, test.ProjectID, nil)
		writeError(w, 409, "live table output was already finalized with different options")
		return
	}
	release()
	release = nil
	if run := s.runner.liveRun(test.ID); run != nil && run.projectID == test.ProjectID {
		run.mu.Lock()
		if rt := run.tables[id]; rt != nil {
			copy := *result
			rt.final = &copy
		}
		run.mu.Unlock()
	}
	if err := s.completeRequiredAuditOperation(r, audit, "live_table.finalize", "succeeded", "succeeded", "live_table", id, test.ProjectID, map[string]any{"output_pool_id": result.OutputPoolID}); err != nil {
		writeError(w, 503, "output is saved but its audit outcome requires retry")
		return
	}
	writeJSON(w, 200, result)
}

func liveTableInspectPage(table *livetable.Table, offset, limit int, revision uint64) (livetable.Result, error) {
	limit = min(limit, table.Limits().MaxRows)
	for {
		result, err := table.Inspect(offset, limit, revision)
		if err == nil || livetable.CodeOf(err) != livetable.CodeCapacity || limit <= 1 {
			return result, err
		}
		limit = max(1, limit/2)
	}
}
