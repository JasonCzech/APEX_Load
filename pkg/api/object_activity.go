package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

type ObjectActivitySummary = storage.ObjectActivitySummary
type ObjectArchiveRequest struct {
	UpdatedAt time.Time `json:"updated_at"`
}
type ObjectActivityListResponse struct {
	Events   []*storage.ObjectActivityEvent `json:"events"`
	Count    int                            `json:"count"`
	Limit    int                            `json:"limit"`
	Offset   int                            `json:"offset"`
	HasMore  bool                           `json:"has_more"`
	Activity *storage.ObjectActivitySummary `json:"activity,omitempty"`
}
type objectActivityContextKey struct{}
type activityRequest struct {
	startedAt time.Time
	writer    http.ResponseWriter
	before    map[string]activitySnapshot
	recorded  map[string]bool
}
type activitySnapshot struct {
	kind, id, name, project string
	incarnation             time.Time
	fields                  map[string]string
}

func (s *Server) activitySnapshot(kind, id string) (activitySnapshot, bool) {
	v := activitySnapshot{kind: kind, id: id}
	var object any
	switch kind {
	case "script":
		obj, ok := s.runner.GetScript(id)
		if !ok {
			return v, false
		}
		v.id = obj.ID
		v.name = disclosureSafeScriptName(obj.Name, obj.Script)
		v.project = obj.ProjectID
		v.incarnation = obj.CreatedAt
		object = obj.Script
	case "scenario":
		obj, ok := s.runner.GetScenario(id)
		if !ok {
			return v, false
		}
		v.id = obj.ID
		v.name = obj.Name
		v.project = obj.ProjectID
		v.incarnation = obj.CreatedAt
		object = obj
	case "test":
		obj, ok := s.runner.GetTest(id)
		if !ok {
			return v, false
		}
		v.id = obj.ID
		v.name = obj.Name
		v.project = obj.ProjectID
		v.incarnation = obj.CreatedAt
		object = obj
	case "external_result_import":
		if s.store == nil {
			return v, false
		}
		imports, ok := s.store.(immutableExternalResultImportPersistence)
		if !ok {
			return v, false
		}
		obj, err := imports.GetExternalResultImport(id)
		if err != nil {
			return v, false
		}
		v.name = obj.Name
		v.project = obj.ProjectID
		v.incarnation = obj.CreatedAt
		object = obj
	default:
		return v, false
	}
	raw, _ := json.Marshal(object)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	v.fields = map[string]string{}
	for key, value := range fields {
		if kind == "test" {
			switch key {
			case "name", "project_id", "folder_id", "application_id", "release_id", "scenario_id", "status", "groups", "duration", "runtime_settings", "goal_config", "data_pools", "scenario_mode", "termination_mode", "shakeout_enabled", "resource_baseline_enabled", "start_delay", "scheduled_start_at", "baseline_pinned", "baseline_pinned_at", "elk_capture", "dynatrace_capture":
			default:
				continue
			}
		}

		switch key {
		case "updated_at", "created_at", "activity", "history", "live_metrics", "sparkline", "archive", "telemetry_health", "authoring_health", "schedules", "scenario_count", "associated_scenarios", "associated_releases", "scenario_run_releases", "last_run_at":
			continue
		}
		sum := sha256.Sum256(value)
		v.fields[key] = string(sum[:])
	}
	return v, true
}
func activityChangedFields(before, after activitySnapshot) []string {
	fields := []string{}
	for key, value := range after.fields {
		if before.fields[key] != value {
			fields = append(fields, key)
		}
	}
	for key := range before.fields {
		if _, ok := after.fields[key]; !ok {
			fields = append(fields, key)
		}
	}
	sort.Strings(fields)
	return fields
}
func lifecycleActivityAction(action string) string {
	parts := strings.Split(action, ".")
	last := parts[len(parts)-1]
	switch last {
	case "promote":
		return "start"
	case "create", "delete", "archive", "restore", "move", "start", "stop", "abort", "pause", "resume", "prune":
		return last
	case "modify", "update", "replace", "sync", "apply", "apply_to_groups", "bind_script", "save", "adjust", "scale", "pin", "unpin", "approve", "rollback", "commit":
		return "modify"
	}
	return ""
}
func (s *Server) recordObjectActivity(r *http.Request, action, status, kind, id, project string, details map[string]any) {
	store, ok := s.store.(storage.ObjectActivityStore)
	if !ok {
		return
	}
	// These operations change protected import drafts, rather than stored scripts.
	if action == "script.import.parameterize.apply" || action == "script.import.settings" {
		return
	}
	parentSection := ""
	if kind == "scenario_schedule" || kind == "scenario_schedule_occurrence" || kind == "scenario_schedule_approval" {
		parentID, _ := details["scenario_id"].(string)
		if parentID == "" {
			parentID = r.PathValue("id")
		}
		if parentID == "" {
			return
		}
		kind, id, parentSection = "scenario", parentID, "schedules"
	}
	if strings.HasPrefix(action, "run.annotation.") {
		parentSection = "annotations"
	}
	if action == "monitor.bindings.save" {
		parentSection = "monitor_bindings"
	}
	if action == "object_repository.bind_script" {
		parentSection = "object_repository"
	}
	if action == "scenario.runtime.apply_to_groups" {
		parentSection = "runtime_settings"
	}
	normalized := lifecycleActivityAction(action)
	// Subresource transitions such as preparation cancellation and renewal
	// modify their parent even when their specialized verb is not a lifecycle action.
	if parentSection != "" || strings.HasPrefix(action, "test.notes.") || strings.HasPrefix(action, "test.preparation.") || strings.HasPrefix(action, "test.baseline_comparison.") {
		normalized = "modify"
	}
	if action == "archive.retention.prune" {
		normalized = "prune"
	}
	if normalized == "" {
		return
	}
	if kind != "script" && kind != "scenario" && kind != "test" && kind != "external_result_import" {
		return
	}
	if strings.Contains(action, ".inspect") || strings.Contains(action, ".suggest") || strings.Contains(action, ".preview") {
		return
	}
	ctx, _ := r.Context().Value(objectActivityContextKey{}).(*activityRequest)
	key := kind + ":" + id + ":" + normalized
	if ctx != nil && ctx.recorded[key] {
		return
	}
	snapshot, found := s.activitySnapshot(kind, id)
	var prior activitySnapshot
	if ctx != nil {
		prior = ctx.before[kind+":"+id]
	}
	if !found {
		snapshot = prior
		if prior.id == "" && normalized != "delete" {
			return
		}
	}
	if snapshot.id == "" {
		snapshot = activitySnapshot{kind: kind, id: id, project: project}
		if name, ok := details["name"].(string); ok {
			snapshot.name = name
		}
	}
	// Name-based legacy/import audit identifiers are not stable object identities.
	if found && snapshot.id != id {
		id = snapshot.id
		key = kind + ":" + id + ":" + normalized
	}
	actorType, actorSubject, actorName := s.resolveAuditActor(r)
	changed := []string{}
	if normalized == "modify" || normalized == "move" {
		if found && prior.id != "" {
			changed = activityChangedFields(prior, snapshot)
		} else {
			changed = []string{"content_or_settings"}
		}
	}
	if normalized == "modify" && (len(changed) == 0 || len(changed) == 1 && changed[0] == "content_or_settings") {
		changed = nil
		for _, section := range []string{"notes", "library_assignment", "preparation", "baseline_comparison", "comparison_baseline", "annotations", "comparison_policy"} {
			if strings.Contains(action, "."+section+".") {
				changed = append(changed, section)
			}
		}
		if len(changed) == 0 {
			changed = []string{"content_or_settings"}
		}
	}
	if normalized == "prune" {
		changed = []string{"result_artifacts"}
	}
	if parentSection != "" {
		if len(changed) == 1 && changed[0] == "content_or_settings" {
			changed = nil
		}
		foundSection := false
		for _, section := range changed {
			if section == parentSection {
				foundSection = true
			}
		}
		if !foundSection {
			changed = append(changed, parentSection)
		}
		sort.Strings(changed)
	}
	e := &storage.ObjectActivityEvent{ID: uuid.NewString(), Action: normalized, ActorType: actorType, ActorSubject: actorSubject, ActorDisplayName: actorName, ProjectID: normalizeProjectID(project), ResourceType: kind, ResourceID: id, ResourceName: snapshot.name, Status: status, ChangedFields: changed, CreatedAt: time.Now().UTC(), Deleted: normalized == "delete" && status == "success"}
	if claims, ok := ciClaimsFromContext(r.Context()); ok && claims.Actor != "" {
		e.InitiatedBy = "github:" + claims.Actor
	}
	if normalized == "start" && (status == "success" || status == "accepted" || status == "pending") {
		e.ID = testStartActivityID(id, snapshot.incarnation)
		if status == "pending" {
			e.ID = strings.TrimSuffix(e.ID, ":success") + ":pending"
		}
	}
	if normalized == "create" && !snapshot.incarnation.IsZero() {
		e.ID = "create:" + kind + ":" + id + ":" + snapshot.incarnation.UTC().Format(time.RFC3339Nano)
		e.CreatedAt = snapshot.incarnation
	}
	if err := store.SaveObjectActivity(e); err != nil {
		s.activityWarning(r, err)
	} else if ctx != nil {
		ctx.recorded[key] = true
	}
}
func (s *Server) activityWarning(r *http.Request, err error) {
	slog.Warn("object activity persistence failed", "path", r.URL.Path, "error", err)
	if ctx, ok := r.Context().Value(objectActivityContextKey{}).(*activityRequest); ok {
		ctx.writer.Header().Set("X-APEXLOAD-Audit-Status", "unavailable")
	}
}

// Only JSON object routes are buffered; streams, metrics, downloads and WebSocket
// upgrades keep their existing response writer and streaming behavior.
func (s *Server) objectActivityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/v1/")
		parts := strings.Split(path, "/")
		kind := ""
		switch parts[0] {
		case "scripts":
			kind = "script"
		case "scenarios":
			kind = "scenario"
		case "tests":
			kind = "test"
		}
		// Replay responses require connection deadlines and do not mutate stored objects.
		for _, part := range parts {
			if part == "replay" || part == "debug" || part == "browser-replay" {
				next.ServeHTTP(w, r)
				return
			}
		}
		// The authored document and placement form a fixed strong-ETag body.
		// Activity has its own lifecycle, so this representation must pass
		// through without display-cache reads or JSON activity enrichment.
		if r.Method == http.MethodGet && (kind == "script" || kind == "scenario") && len(parts) == 2 {
			views := r.URL.Query()["view"]
			if len(views) == 1 && views[0] == "authoring" {
				next.ServeHTTP(w, r)
				return
			}
		}
		mutation := isMutatingMethod(r.Method)
		readObject := r.Method == http.MethodGet && kind != "" && len(parts) <= 2
		if !mutation && !readObject {
			next.ServeHTTP(w, r)
			return
		}
		if mutation && kind == "" && !strings.HasPrefix(path, "library/") && !strings.HasPrefix(path, "ci/") && !strings.HasPrefix(path, "github/") && !strings.HasPrefix(path, "recorder/") && !strings.HasPrefix(path, "results/") {
			// Preserve streaming behavior while still exposing audit warnings for
			// ordinary mutations such as archive retention and report metadata.
			ctx := &activityRequest{startedAt: time.Now().UTC(), writer: w, before: map[string]activitySnapshot{}, recorded: map[string]bool{}}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), objectActivityContextKey{}, ctx)))
			return
		}
		recorder := httptest.NewRecorder()
		ctx := &activityRequest{startedAt: time.Now().UTC(), writer: recorder, before: map[string]activitySnapshot{}, recorded: map[string]bool{}}
		if kind != "" && len(parts) > 1 {
			if before, ok := s.activitySnapshot(kind, parts[1]); ok {
				ctx.before[kind+":"+before.id] = before
			}
		}
		r = r.WithContext(context.WithValue(r.Context(), objectActivityContextKey{}, ctx))
		if mutation && kind != "" && len(parts) > 1 && parts[len(parts)-1] != "archive" && parts[len(parts)-1] != "restore" && !(r.Method == http.MethodDelete && len(parts) == 2) {
			if err := s.rejectArchivedMutation(kind, parts[1]); errors.Is(err, storage.ErrObjectArchived) {
				writeError(w, http.StatusConflict, err.Error())
				return
			}
		}
		next.ServeHTTP(recorder, r)
		// Approval/recording/batch-import responses identify committed objects even
		// when their specialized audit event targets a session or batch instead.
		if mutation && recorder.Code < 400 && parts[len(parts)-1] != "archive" && parts[len(parts)-1] != "restore" {
			var body any
			if json.Unmarshal(recorder.Body.Bytes(), &body) == nil {
				s.recordReturnedActivity(r, body, kind)
			}
		}
		viewMutation := mutation && kind != "" && (len(parts) <= 2 || kind != "test" && (parts[len(parts)-1] == "archive" || parts[len(parts)-1] == "restore"))
		if (readObject || viewMutation) && recorder.Code >= 200 && recorder.Code < 300 {
			s.enrichActivityResponse(recorder, kind, r)
		}
		for key, values := range recorder.Header() {
			w.Header()[key] = values
		}
		w.WriteHeader(recorder.Code)
		_, _ = w.Write(recorder.Body.Bytes())
	})
}
func (s *Server) recordReturnedActivity(r *http.Request, value any, hint string) {
	switch v := value.(type) {
	case []any:
		for _, item := range v {
			s.recordReturnedActivity(r, item, hint)
		}
	case map[string]any:
		id, _ := v["id"].(string)
		kind := hint
		libraryAction := ""
		if localID, ok := v["local_item_id"].(string); ok {
			if itemKind, ok := v["kind"].(string); ok {
				kind = itemKind
				if kind == "result" {
					kind = "external_result_import"
				}
				id = localID
				outcome, _ := v["action"].(string)
				switch {
				case strings.HasPrefix(outcome, "created"):
					libraryAction = "create"
				case strings.HasPrefix(outcome, "updated"):
					libraryAction = "modify"
				default:
					return // unchanged/skipped imports did not mutate this object
				}
			}
		}
		if _, ok := v["script"]; ok {
			kind = "script"
		}
		if _, ok := v["actions"]; ok {
			kind = "script"
		}
		if _, ok := v["script_count"]; ok {
			kind = "scenario"
		}
		if id != "" && (kind == "script" || kind == "scenario" || kind == "external_result_import") {
			if snapshot, ok := s.activitySnapshot(kind, id); ok {
				ctx, _ := r.Context().Value(objectActivityContextKey{}).(*activityRequest)
				action := "create"
				if libraryAction != "" {
					action = libraryAction
				}
				if ctx != nil && libraryAction == "" && snapshot.incarnation.Before(ctx.startedAt) && ctx.before[kind+":"+snapshot.id].id == "" {
					return
				}
				if ctx != nil && ctx.before[kind+":"+snapshot.id].id != "" {
					action = "modify"
					if len(activityChangedFields(ctx.before[kind+":"+snapshot.id], snapshot)) == 0 {
						return
					}
				}
				if ctx != nil {
					for key := range ctx.recorded {
						if strings.HasPrefix(key, kind+":"+snapshot.id+":") {
							return
						}
					}
				}
				s.recordObjectActivity(r, kind+"."+action, "success", kind, snapshot.id, snapshot.project, nil)
				return
			}
		}
		for key, item := range v {
			next := hint
			switch key {
			case "scripts", "script":
				next = "script"
			case "scenarios", "scenario":
				next = "scenario"
			}
			if key != "actions" && key != "init" && key != "teardown" && key != "config" && key != "parameters" && key != "data_sources" {
				s.recordReturnedActivity(r, item, next)
			}
		}
	}
}
func (s *Server) rejectArchivedMutation(kind, id string) error {
	if s.store == nil {
		return nil
	}
	if kind == "script" {
		obj, err := s.store.GetScript(id)
		if err == nil && obj.ArchivedAt != nil {
			return storage.ErrObjectArchived
		}
	}
	if kind == "scenario" {
		obj, _, err := s.store.GetScenario(id)
		if err == nil && obj.ArchivedAt != nil {
			return storage.ErrObjectArchived
		}
	}
	return nil
}
func (s *Server) enrichActivityResponse(recorder *httptest.ResponseRecorder, kind string, r *http.Request) {
	store, ok := s.store.(storage.ObjectActivityStore)
	if !ok {
		return
	}
	var body any
	if json.Unmarshal(recorder.Body.Bytes(), &body) != nil {
		return
	}
	objects := []map[string]any{}
	switch v := body.(type) {
	case map[string]any:
		if _, ok := v["id"]; ok {
			objects = append(objects, v)
		} else {
			if list, ok := v[kind+"s"].([]any); ok {
				for _, item := range list {
					if obj, ok := item.(map[string]any); ok {
						objects = append(objects, obj)
					}
				}
			}
		}
	}
	grouped := map[string][]string{}
	for _, obj := range objects {
		project, _ := obj["project_id"].(string)
		id, _ := obj["id"].(string)
		grouped[project] = append(grouped[project], id)
	}
	summaries := map[string]storage.ObjectActivitySummary{}
	for project, ids := range grouped {
		values, err := store.ObjectActivitySummaries(project, kind, ids)
		if err != nil {
			continue
		}
		for id, summary := range values {
			summaries[id] = summary
		}
	}
	for _, obj := range objects {
		id, _ := obj["id"].(string)
		if v, ok := summaries[id]; ok {
			obj["activity"] = v
		}
	}
	encoded, err := json.Marshal(body)
	if err == nil {
		recorder.Body.Reset()
		recorder.Body.Write(encoded)
		recorder.Header().Del("Content-Length")
	}
}
func (s *Server) handleProjectActivity(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(storage.ObjectActivityStore)
	if !ok {
		writeError(w, 503, "activity storage is unavailable")
		return
	}
	q := r.URL.Query()
	limit := 50
	offset := 0
	var err error
	if q.Has("limit") {
		limit, err = strconv.Atoi(q.Get("limit"))
		if err != nil || limit < 1 || limit > 250 {
			writeError(w, 400, "limit must be between 1 and 250")
			return
		}
	}
	if q.Has("offset") {
		offset, err = strconv.Atoi(q.Get("offset"))
		if err != nil || offset < 0 {
			writeError(w, 400, "offset must be non-negative")
			return
		}
	}
	allowed := []string{"script", "scenario", "test", "external_result_import"}
	if account, ok := AuthenticatedServiceAccountFromContext(r.Context()); ok {
		allowed = []string{}
		if activityScopeAllows(account, "read") {
			allowed = append(allowed, "script")
		}
		if activityScopeAllows(account, "results:read") {
			allowed = append(allowed, "scenario", "test", "external_result_import")
		}
	}
	kind := q.Get("resource_type")
	if kind != "" {
		valid := false
		for _, value := range allowed {
			if kind == value {
				valid = true
			}
		}
		if !valid {
			writeError(w, 403, "resource history is not accessible")
			return
		}
	}
	f := storage.ObjectActivityFilter{ProjectID: r.PathValue("id"), ResourceType: kind, ResourceTypes: allowed, ResourceID: q.Get("resource_id"), ActorSubject: q.Get("actor_subject"), Action: q.Get("action"), Search: q.Get("q"), Limit: limit + 1, Offset: offset}
	for _, v := range []struct {
		key string
		dst **time.Time
	}{{"created_after", &f.After}, {"created_before", &f.Before}} {
		if raw := q.Get(v.key); raw != "" {
			at, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				writeError(w, 400, "invalid "+v.key)
				return
			}
			*v.dst = &at
		}
	}
	if f.After != nil && f.Before != nil && f.After.After(*f.Before) {
		writeError(w, 400, "created_after must precede created_before")
		return
	}
	events, err := store.ListObjectActivity(f)
	if err != nil {
		writeError(w, 503, "activity history is unavailable")
		return
	}
	more := len(events) > limit
	if more {
		events = events[:limit]
	}
	response := ObjectActivityListResponse{Events: events, Count: len(events), Limit: limit, Offset: offset, HasMore: more}
	if kind != "" && f.ResourceID != "" {
		if summaries, err := store.ObjectActivitySummaries(f.ProjectID, kind, []string{f.ResourceID}); err == nil {
			if summary, ok := summaries[f.ResourceID]; ok {
				response.Activity = &summary
			}
		}
	}
	writeJSON(w, 200, response)
}
func (s *Server) handleArchiveObject(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(storage.ObjectActivityStore)
	if !ok {
		writeError(w, 503, "archive storage is unavailable")
		return
	}
	kind := "script"
	if strings.Contains(r.URL.Path, "/scenarios/") {
		kind = "scenario"
	}
	snapshot, ok := s.activitySnapshot(kind, r.PathValue("id"))
	if !ok {
		writeError(w, 404, kind+" not found")
		return
	}
	var req ObjectArchiveRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	archived := strings.HasSuffix(r.URL.Path, "/archive")
	changed, err := store.SetObjectArchived(kind, snapshot.id, req.UpdatedAt, archived)
	if err != nil {
		writeError(w, 409, err.Error())
		return
	}
	action := "restore"
	if archived {
		action = "archive"
	}
	if changed {
		s.recordAuditEvent(r, kind+"."+action, "success", kind, snapshot.id, snapshot.project, map[string]any{"name": snapshot.name})
	}
	// Refresh the changed object directly. A full-library refresh may be fenced
	// by an unrelated concurrent read and leave this object's version stale.
	var script *StoredScript
	var scenario *StoredScenario
	if kind == "script" {
		var found bool
		script, found, err = s.runner.GetScriptDurable(snapshot.id)
		if err != nil || !found {
			writeError(w, http.StatusServiceUnavailable, "archive state saved; script refresh is unavailable")
			return
		}
	} else {
		scenario, err = s.runner.refreshArchivedScenario(snapshot.id)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "archive state saved; scenario refresh is unavailable")
			return
		}
	}

	if kind == "script" {
		writeJSON(w, http.StatusOK, maskStoredScript(script))
		return
	}
	writeJSON(w, http.StatusOK, maskStoredScenario(scenario))
}

func activityScopeAllows(account *AuthenticatedServiceAccount, scope string) bool {
	for _, value := range normalizeAuthenticatedServiceAccountScopes(account.Scopes) {
		if value == scope || value == "read" || value == "*" || value == "automation:*" {
			return true
		}
	}
	return false
}

func archiveStateFilter(w http.ResponseWriter, r *http.Request) (string, bool) {
	state := r.URL.Query().Get("archive_state")
	if state == "" {
		state = "active"
	}
	switch state {
	case "active", "archived", "all":
		return state, true
	}
	writeError(w, 400, "archive_state must be active, archived or all")
	return "", false
}
func filterArchivedScripts(scripts []*StoredScript, state string) []*StoredScript {
	result := make([]*StoredScript, 0, len(scripts))
	for _, obj := range scripts {
		if obj == nil {
			continue
		}
		if obj.Script != nil {
			obj.ArchivedAt = obj.Script.ArchivedAt
		}
		if state == "all" || state == "archived" && obj.ArchivedAt != nil || state == "active" && obj.ArchivedAt == nil {
			result = append(result, obj)
		}
	}
	return result
}
func filterArchivedScenarios(scenarios []*StoredScenario, state string) []*StoredScenario {
	result := make([]*StoredScenario, 0, len(scenarios))
	for _, obj := range scenarios {
		if obj != nil && (state == "all" || state == "archived" && obj.ArchivedAt != nil || state == "active" && obj.ArchivedAt == nil) {
			result = append(result, obj)
		}
	}
	return result
}

// Refresh a single scenario while holding the same local write fence used by
// object mutations, so an unrelated catalog read cannot discard this snapshot.
func (tr *TestRunner) refreshArchivedScenario(id string) (*StoredScenario, error) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	full, groups, err := tr.scenarioStore.GetScenario(id)
	if err != nil {
		return nil, err
	}
	bindings, err := tr.scenarioStore.ListScenarioDataPoolBindings(id)
	if err != nil {
		return nil, err
	}
	schedules, err := tr.scenarioStore.ListScenarioSchedules(id)
	if err != nil {
		return nil, err
	}
	copiedSchedules := make([]*storage.ScenarioSchedule, 0, len(schedules))
	for _, schedule := range schedules {
		copiedSchedules = append(copiedSchedules, cloneScenarioSchedule(schedule))
	}
	scenario := tr.buildStoredScenarioFromPartsLocked(full, groups, bindings, copiedSchedules)
	if previous := tr.scenarios[id]; previous != nil {
		tr.removeRefKeyLocked(previous.RefKey)
	}
	tr.scenarios[id] = scenario
	tr.contentRevision++
	tr.observeAndIndexRefKeyLocked(scenario.RefKey, scenario.ID)
	now := time.Now().UTC()
	tr.readinessScheduler = schedulerReadinessProjectionForScenarios(tr.scenarios, now)
	tr.publishRuntimeReadinessLocked(now)
	return tr.enrichScenarioSummaryLocked(scenario), nil
}
