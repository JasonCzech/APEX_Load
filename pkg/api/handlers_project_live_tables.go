package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/livetable"
	"github.com/jasonczech/apexload/pkg/storage"
)

type ProjectLiveTablePermissions struct {
	CanView   bool `json:"can_view"`
	CanMutate bool `json:"can_mutate"`
	CanAdmin  bool `json:"can_admin"`
}
type ProjectLiveTableStats struct {
	Revision  string                         `json:"revision"`
	DataBytes int64                          `json:"data_bytes"`
	Counts    *livetable.Counts              `json:"counts,omitempty"`
	Capacity  storage.ProjectLiveTableConfig `json:"capacity"`
}
type ProjectLiveTableAttachmentView struct {
	ID        string `json:"id"`
	RunID     string `json:"run_id"`
	GroupName string `json:"group_name"`
	Alias     string `json:"alias"`
	State     string `json:"state"`
}
type ProjectLiveTableView struct {
	ID             string                           `json:"id"`
	ProjectID      string                           `json:"project_id"`
	Name           string                           `json:"name"`
	Profile        string                           `json:"profile"`
	Columns        []string                         `json:"columns"`
	State          string                           `json:"state"`
	ConfigRevision string                           `json:"config_revision"`
	InstanceID     string                           `json:"instance_id"`
	Stats          *ProjectLiveTableStats           `json:"stats,omitempty"`
	Attachments    []ProjectLiveTableAttachmentView `json:"attachments,omitempty"`
	Permissions    ProjectLiveTablePermissions      `json:"permissions"`
}
type ProjectLiveTableListResponse struct {
	Tables []ProjectLiveTableView `json:"tables"`
}
type ProjectLiveTableRowsResponse struct {
	Rows     []ProjectLiveTableRow `json:"rows"`
	Columns  []string              `json:"columns"`
	Revision string                `json:"revision"`
	Total    int64                 `json:"total"`
}
type ProjectLiveTableCreateRequest struct {
	Name       string                         `json:"name"`
	Profile    string                         `json:"profile"`
	Columns    []string                       `json:"columns"`
	Limits     storage.ProjectLiveTableConfig `json:"limits"`
	SeedPoolID string                         `json:"seed_pool_id,omitempty"`
}
type ProjectLiveTableLifecycleRequest struct {
	Action                 string `json:"action"`
	ExpectedConfigRevision string `json:"expected_config_revision"`
	ExpectedInstanceID     string `json:"expected_instance_id"`
	ExpectedRevision       string `json:"expected_revision"`
	Reason                 string `json:"reason"`
}

func (s *Server) projectTableRequest(w http.ResponseWriter, r *http.Request) (string, bool) {
	project := r.PathValue("id")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if s.store == nil || s.runner == nil {
		writeError(w, 503, "project live tables are unavailable")
		return "", false
	}
	if !requireAuthorizedProjectMatch(w, r, project) || !s.requireDomainFeatureForProject(w, project, "data.live_tables") {
		return "", false
	}
	if _, err := s.store.GetProject(project); err != nil {
		writeError(w, 404, "project not found")
		return "", false
	}
	return project, true
}

func (s *Server) projectTablePermissions(r *http.Request, project string) ProjectLiveTablePermissions {
	if isAuthBypassMode(s.authSettingsForRequest().Mode) {
		return ProjectLiveTablePermissions{true, true, true}
	}
	user, _ := AuthenticatedUserFromContext(r.Context())
	account, _ := AuthenticatedServiceAccountFromContext(r.Context())
	allowed := func(requirement accessRequirement, scope string) bool {
		if user == nil {
			return false
		}
		if !userHasGlobalGrantForRequirement(user, requirement) && !userHasProjectOrParentRole(s.store, user, project, requirement.ProjectRoles...) {
			return false
		}
		return account == nil || serviceAccountAllowsScopes(account, []string{scope})
	}
	return ProjectLiveTablePermissions{CanView: allowed(viewerProjectAccess(resolveProjectIDFromPath), "read"), CanMutate: allowed(editorProjectAccess(resolveProjectIDFromPath), "scripts:write"), CanAdmin: allowed(projectAdminAccess(resolveProjectIDFromPath), "scripts:write")}
}

func (s *Server) projectTableView(r *http.Request, definition *storage.ProjectLiveTableDefinition) ProjectLiveTableView {
	return ProjectLiveTableView{ID: definition.ID, ProjectID: definition.ProjectID, Name: definition.Name, Profile: definition.Config.Profile, Columns: append([]string(nil), definition.Config.Columns...), State: definition.State, ConfigRevision: strconv.FormatUint(definition.Revision, 10), InstanceID: definition.CurrentInstanceID, Permissions: s.projectTablePermissions(r, definition.ProjectID)}
}

func (s *Server) projectTableAudit(r *http.Request, project, table, action string, details map[string]any) *storage.AuditEvent {
	kind, subject, name := s.resolveAuditActor(r)
	event := &storage.AuditEvent{ID: uuid.NewString(), Action: "project_live_table." + action, ActorType: kind, ActorSubject: subject, ActorDisplayName: name, ProjectID: project, ResourceType: "project_live_table", ResourceID: table, Status: "success", Details: details, CreatedAt: time.Now().UTC()}
	if p, err := s.store.GetProject(project); err == nil && p != nil {
		event.DomainID = p.DomainID
	}
	return event
}

func decodeProjectTableBody(w http.ResponseWriter, r *http.Request, out any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		writeError(w, 400, "invalid project live table request fields or body size")
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		writeError(w, 400, "request must contain one JSON object")
		return false
	}
	return true
}

func writeProjectTableError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, 404, "project live table not found")
	case errors.Is(err, storage.ErrLiveTableConflict):
		writeError(w, 409, "project live table changed; refresh and review before retrying")
	case errors.Is(err, storage.ErrLiveTableClosed):
		writeError(w, 409, "project live table lifecycle does not allow this operation")
	case errors.Is(err, storage.ErrLiveTableLimit):
		writeError(w, 429, "project live table capacity exhausted")
	default:
		switch livetable.CodeOf(err) {
		case livetable.CodeConflict, livetable.CodeLease:
			writeError(w, 409, "table or row changed, or a worker holds its lease; refresh and review before retrying")
		case livetable.CodeExpired:
			writeError(w, 410, "operator request expired; inspect current state before creating a new request")
		case livetable.CodeInvalid:
			writeError(w, 400, "invalid operation for this table")
		case livetable.CodeCapacity:
			writeError(w, 429, "project live table capacity exhausted")
		default:
			writeError(w, 503, "project live table state is unavailable; retain the same request identity when retrying an uncertain mutation")
		}
	}
}

func (s *Server) handleListProjectLiveTables(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), projectLiveTableServiceTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	project, ok := s.projectTableRequest(w, r)
	if !ok {
		return
	}
	definitions, err := s.runner.projectLiveTableService().List(r.Context(), project)
	if err != nil {
		writeProjectTableError(w, err)
		return
	}
	out := ProjectLiveTableListResponse{Tables: []ProjectLiveTableView{}}
	for i := range definitions {
		out.Tables = append(out.Tables, s.projectTableView(r, &definitions[i]))
	}
	writeJSON(w, 200, out)
}

func (s *Server) handleGetProjectLiveTable(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), projectLiveTableServiceTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	project, ok := s.projectTableRequest(w, r)
	if !ok {
		return
	}
	svc := s.runner.projectLiveTableService()
	definition, err := svc.Get(r.Context(), project, r.PathValue("tableID"))
	if err != nil {
		writeProjectTableError(w, err)
		return
	}
	view := s.projectTableView(r, definition)
	stats, err := svc.Stats(r.Context(), project, definition.ID, definition.CurrentInstanceID)
	view.Stats = &ProjectLiveTableStats{Revision: strconv.FormatUint(stats.Revision, 10), DataBytes: stats.DataBytes, Counts: &stats.Counts, Capacity: definition.Config}
	if err != nil {
		writeProjectTableError(w, err)
		return
	}
	store, ok := s.store.(storage.ProjectLiveTableStore)
	if !ok {
		writeError(w, 503, "project live table storage is unavailable")
		return
	}
	attachments, err := store.ListProjectLiveTableAttachments(r.Context(), project, definition.ID)
	if err != nil {
		writeProjectTableError(w, err)
		return
	}
	for _, a := range attachments {
		view.Attachments = append(view.Attachments, ProjectLiveTableAttachmentView{ID: a.ID, RunID: a.TestID, GroupName: a.Group, Alias: a.Alias, State: a.State})
	}
	writeJSON(w, 200, view)
}

func (s *Server) handleCreateProjectLiveTable(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), projectLiveTableServiceTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	project, ok := s.projectTableRequest(w, r)
	if !ok {
		return
	}
	var input ProjectLiveTableCreateRequest
	if !decodeProjectTableBody(w, r, &input) {
		return
	}
	if input.Limits.Profile != "" || len(input.Limits.Columns) > 0 {
		writeError(w, 400, "profile and columns belong at the top level")
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || len(input.Name) > 256 {
		writeError(w, 400, "name must contain 1..256 bytes")
		return
	}
	input.Limits.Profile = input.Profile
	input.Limits.Columns = input.Columns
	limits, err := normalizeProjectTableConfig(input.Limits)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	input.Limits = limits
	if input.SeedPoolID != "" {
		pool, err := s.store.GetDataPool(input.SeedPoolID)
		if err != nil || pool == nil || normalizeProjectID(pool.ProjectID) != normalizeProjectID(project) {
			writeError(w, 404, "seed pool not found in this project")
			return
		}
	}
	audit := s.projectTableAudit(r, project, "", "create", nil)
	definition, err := s.runner.projectLiveTableService().Create(r.Context(), ProjectLiveTableCreateInput{ProjectID: project, Name: input.Name, Config: input.Limits, SeedPoolID: input.SeedPoolID, ActorSubject: audit.ActorSubject, Audit: audit})
	if err != nil {
		writeProjectTableError(w, err)
		return
	}
	writeJSON(w, 201, s.projectTableView(r, definition))
}

func (s *Server) handleMutateProjectLiveTable(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), projectLiveTableServiceTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	project, ok := s.projectTableRequest(w, r)
	if !ok {
		return
	}
	var input ProjectLiveTableMutation
	if !decodeProjectTableMutationBody(w, r, &input) {
		return
	}
	svc := s.runner.projectLiveTableService()
	definition, err := svc.Get(r.Context(), project, r.PathValue("tableID"))
	if err != nil {
		writeProjectTableError(w, err)
		return
	}
	if _, _, err = input.request(livetable.Profile(definition.Config.Profile), time.Now().UTC(), time.Duration(definition.Config.RetryWindowMS)*time.Millisecond); err != nil {
		if livetable.CodeOf(err) == livetable.CodeExpired {
			writeProjectTableError(w, err)
		} else {
			writeError(w, 400, err.Error())
		}
		return
	}
	audit := s.projectTableAudit(r, project, definition.ID, "mutate", map[string]any{"operation": input.Operation, "instance_id": input.ExpectedInstanceID, "expected_revision": input.ExpectedRevision, "record_id": input.RecordID, "position": input.Position})
	response, err := svc.ExecuteOperator(r.Context(), project, definition.ID, audit.ActorSubject, input, audit)
	if err != nil {
		writeProjectTableError(w, err)
		return
	}
	if !s.reauthorizeProjectTableAccess(w, r, project, withServiceAccountScope(editorProjectAccess(resolveProjectIDFromPath), "scripts:write")) {
		return
	}
	w.Header().Set("X-APEXLoad-Data-Sensitivity", "raw")
	writeJSON(w, 200, response)
}

func (s *Server) handleProjectLiveTableLifecycle(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), projectLiveTableServiceTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	project, ok := s.projectTableRequest(w, r)
	if !ok {
		return
	}
	var input ProjectLiveTableLifecycleRequest
	if !decodeProjectTableBody(w, r, &input) {
		return
	}
	revision, err := parseProjectTableRevision(input.ExpectedRevision)
	configRevision, configErr := parseProjectTableRevision(input.ExpectedConfigRevision)
	if err != nil || configErr != nil || input.ExpectedInstanceID == "" || strings.TrimSpace(input.Reason) == "" || len(input.Reason) > 1024 {
		writeError(w, 400, "lifecycle actions require current revisions, instance and a reason of 1..1024 bytes")
		return
	}
	switch input.Action {
	case "quiesce", "resume", "reset", "archive":
	default:
		writeError(w, 400, "unsupported lifecycle action")
		return
	}
	table := r.PathValue("tableID")
	audit := s.projectTableAudit(r, project, table, input.Action, map[string]any{"reason": input.Reason, "instance_id": input.ExpectedInstanceID})
	command := ProjectLiveTableLifecycleInput{ProjectID: project, TableID: table, Action: input.Action, Reason: input.Reason, ExpectedDefinitionRevision: configRevision, ExpectedInstanceID: input.ExpectedInstanceID, ExpectedRevision: revision, Audit: audit}
	var definition *storage.ProjectLiveTableDefinition
	if input.Action == "reset" {
		definition, err = s.runner.projectLiveTableService().Reset(r.Context(), ProjectLiveTableResetInput{ProjectLiveTableLifecycleInput: command})
	} else {
		definition, err = s.runner.projectLiveTableService().ChangeLifecycle(r.Context(), command)
	}
	if err != nil {
		writeProjectTableError(w, err)
		return
	}
	writeJSON(w, 200, s.projectTableView(r, definition))
}

func (s *Server) handleProjectLiveTableRows(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), projectLiveTableServiceTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	s.projectLiveTableRead(w, r, false)
}
func (s *Server) handleExportProjectLiveTable(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), projectLiveTableServiceTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	s.projectLiveTableRead(w, r, true)
}
func (s *Server) projectLiveTableRead(w http.ResponseWriter, r *http.Request, export bool) {
	project, ok := s.projectTableRequest(w, r)
	if !ok {
		return
	}
	revision, err := parseProjectTableRevision(r.URL.Query().Get("revision"))
	instance := r.URL.Query().Get("instance_id")
	if err != nil || instance == "" {
		writeError(w, 400, "inspection requires instance_id and a decimal revision")
		return
	}
	offset, err := strconv.Atoi(defaultQuery(r, "offset", "0"))
	limit, limitErr := strconv.Atoi(defaultQuery(r, "limit", "100"))
	if err != nil || limitErr != nil || offset < 0 || offset > 100000 || limit < 1 || limit > 100 {
		writeError(w, 400, "inspection requires offset 0..100000 and limit 1..100")
		return
	}
	if !s.runner.liveTablesResponseMu.TryLock() {
		w.Header().Set("Retry-After", "1")
		writeError(w, 429, "table preview or export is busy; retry")
		return
	}
	defer s.runner.liveTablesResponseMu.Unlock()
	action := "rows.preview"
	if export {
		action = "rows.export"
		offset = 0
		limit = 100
	}
	table := r.PathValue("tableID")
	svc := s.runner.projectLiveTableService()
	definition, err := svc.Get(r.Context(), project, table)
	if err != nil {
		writeProjectTableError(w, err)
		return
	}
	limit = min(limit, definition.Config.MaxRows)
	if limit < 1 {
		writeError(w, 503, "project live table inspection limits are unavailable")
		return
	}
	term, err := s.runner.captureProjectLiveTableTerm()
	if err != nil {
		writeProjectTableError(w, err)
		return
	}
	auditID, err := s.beginRequiredAuditOperation(r, "project_live_table."+action, "project_live_table", table, project, map[string]any{"instance_id": instance, "revision": strconv.FormatUint(revision, 10)})
	if err != nil {
		writeError(w, 503, "inspection audit is unavailable")
		return
	}
	out := ProjectLiveTableRowsResponse{Rows: []ProjectLiveTableRow{}, Revision: strconv.FormatUint(revision, 10)}
	encodedRowsBytes := 0
	err = svc.WithInspection(r.Context(), project, table, instance, func(ctx context.Context, core *livetable.Table) error {
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			page, readErr := core.Inspect(offset, limit, revision)
			if livetable.CodeOf(readErr) == livetable.CodeCapacity && limit > 1 {
				// Valid large cells may not fit the normal page size. Keep the
				// same revision and offset while shrinking the bounded request.
				limit = max(1, limit/2)
				continue
			}
			if readErr != nil {
				return readErr
			}
			if page.Revision != revision {
				return storage.ErrLiveTableConflict
			}
			out.Columns = page.Columns
			out.Total = page.Count
			for _, row := range page.Rows {
				value := projectTableRow(row)
				// Encode each row once for admission. Re-encoding the complete
				// accumulated export per page makes large exports quadratic.
				encoded, encodeErr := json.Marshal(value)
				if encodeErr != nil {
					return encodeErr
				}
				encodedRowsBytes += len(encoded) + 1
				if encodedRowsBytes > 16<<20 {
					return storage.ErrLiveTableLimit
				}
				out.Rows = append(out.Rows, value)
			}
			if !export || offset+len(page.Rows) >= int(page.Count) || len(page.Rows) == 0 {
				return nil
			}
			offset += len(page.Rows)
		}
	})
	if err == nil {
		body, encodeErr := json.Marshal(out)
		if encodeErr != nil {
			err = encodeErr
		} else if len(body) > 16<<20 {
			err = storage.ErrLiveTableLimit
		}
	}
	status := "success"
	if err != nil {
		status = "failed"
	}
	if auditErr := s.completeRequiredAuditOperation(r, auditID, "project_live_table."+action, status, status, "project_live_table", table, project, map[string]any{"row_count": len(out.Rows), "instance_id": instance}); auditErr != nil {
		writeError(w, 503, "inspection audit is unavailable")
		return
	}
	if err != nil {
		writeProjectTableError(w, err)
		return
	}
	// The audit can block while ownership, membership or credentials change.
	// Re-resolve authority before any row bytes leave this handler.
	if !s.reauthorizeProjectTableRead(w, r, project) {
		return
	}
	if !s.runner.projectLiveTableTermCurrent(term) {
		writeError(w, 409, "project live table owner changed; refresh before inspecting")
		return
	}
	w.Header().Set("X-APEXLoad-Data-Sensitivity", "raw")
	writeJSON(w, 200, out)
}

func (s *Server) reauthorizeProjectTableRead(w http.ResponseWriter, r *http.Request, project string) bool {
	return s.reauthorizeProjectTableAccess(w, r, project, withServiceAccountScope(viewerProjectAccess(resolveProjectIDFromPath), "read"))
}

func (s *Server) reauthorizeProjectTableAccess(w http.ResponseWriter, r *http.Request, project string, requirement accessRequirement) bool {
	if r.Context().Err() != nil {
		writeError(w, 503, "project table request is no longer active")
		return false
	}
	settings := s.authSettingsForRequest()
	if preview, _, ok := devPreviewResolutionForRequest(settings, r); ok {
		if !authorizeAccessRequirement(w, r, s.store, preview, requirement) {
			return false
		}
	} else if !isAuthBypassMode(settings.Mode) {
		resolution, err := resolveRequestAuth(r, settings, s.store, s.activeLocalAuthGeneration())
		if err != nil {
			writeError(w, 403, "project table permission is no longer available")
			return false
		}
		if !authorizeAccessRequirement(w, r, s.store, resolution, requirement) {
			return false
		}
	}
	return requireAuthorizedProjectMatch(w, r, project) && s.requireDomainFeatureForProject(w, project, "data.live_tables")
}
