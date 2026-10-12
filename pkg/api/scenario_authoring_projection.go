package api

import (
	"database/sql"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

var errScenarioAuthoringUnsupported = errors.New("durable scenario authoring store unsupported")

const scenarioAuthoringProjectionVersion = "apexload.scenario_authoring.v1"
const scenarioAuthoringTagPrefix = "scenario-authoring-v1:"

// ScenarioAuthoringGroup uses the CreateGroupEntry field names so an authored
// document can be edited and sent back as a scenario update. Array position is
// the group order; joined script names and protocols are display context.
type ScenarioAuthoringGroup struct {
	Name                         string                       `json:"name"`
	ScriptID                     string                       `json:"script_id"`
	Role                         string                       `json:"role,omitempty"`
	VUsers                       int                          `json:"vusers"`
	RampUp                       string                       `json:"ramp_up,omitempty"`
	RampDown                     string                       `json:"ramp_down,omitempty"`
	StartOffset                  string                       `json:"start_offset,omitempty"`
	Stages                       []storage.LoadStage          `json:"stages,omitempty"`
	Iterations                   *int                         `json:"iterations,omitempty"`
	Initialization               string                       `json:"initialization,omitempty"`
	RuntimeSettings              *RuntimeSettingsOverride     `json:"runtime_settings,omitempty"`
	Assignment                   *GeneratorAssignmentSettings `json:"assignment,omitempty"`
	TransactionResponseTimeSLAMs *float64                     `json:"transaction_response_time_sla_ms,omitempty"`
}

// ScenarioAuthoringDataPoolBinding keeps the binding's stable identity but not
// its clocks or the joined pool name.
type ScenarioAuthoringDataPoolBinding struct {
	ID                string                    `json:"id"`
	PoolID            string                    `json:"pool_id"`
	Alias             string                    `json:"alias"`
	AccessMode        string                    `json:"access_mode"`
	SelectionStrategy string                    `json:"selection_strategy"`
	LiveOptions       *storage.LiveTableOptions `json:"live_options,omitempty"`
	Scope             string                    `json:"scope,omitempty"`
	ProjectTableID    string                    `json:"project_table_id,omitempty"`
}

// ScenarioAuthoringDocument holds only persisted authored fields, with
// runtime credentials masked exactly as the ordinary scenario read masks them.
type ScenarioAuthoringDocument struct {
	ID               string                             `json:"id"`
	RefKey           string                             `json:"ref_key,omitempty"`
	Name             string                             `json:"name"`
	ProjectID        string                             `json:"project_id"`
	WorkloadShapeID  string                             `json:"workload_shape_id,omitempty"`
	Duration         string                             `json:"duration"`
	StartDelay       string                             `json:"start_delay,omitempty"`
	TerminationMode  string                             `json:"termination_mode,omitempty"`
	ScenarioMode     string                             `json:"scenario_mode,omitempty"`
	ScriptShakeout   bool                               `json:"script_shakeout,omitempty"`
	ResourceBaseline bool                               `json:"resource_baseline,omitempty"`
	Assignment       *GeneratorAssignmentSettings       `json:"assignment,omitempty"`
	GoalConfig       *GoalConfig                        `json:"goal_config,omitempty"`
	TrafficProfile   *TrafficProfile                    `json:"traffic_profile,omitempty"`
	RuntimeSettings  *RuntimeSettingsOverride           `json:"runtime_settings,omitempty"`
	Groups           []ScenarioAuthoringGroup           `json:"groups"`
	DataPools        []ScenarioAuthoringDataPoolBinding `json:"data_pools"`
}

type ScenarioAuthoringPlacement struct {
	ApplicationID *string  `json:"application_id"`
	ReleaseIDs    []string `json:"release_ids"`
}

// ScenarioAuthoringMonitorSelection is the bound monitor source set, ordered by
// source identity. Its separate CAS revision is not part of the document.
type ScenarioAuthoringMonitorSelection struct {
	Sources []storage.MonitorBinding `json:"sources"`
}

type ScenarioAuthoringProjection struct {
	ProjectionVersion string                            `json:"projection_version"`
	ID                string                            `json:"id"`
	ProjectID         string                            `json:"project_id"`
	ContentRevision   string                            `json:"content_revision"`
	Scenario          ScenarioAuthoringDocument         `json:"scenario"`
	LibraryPlacement  ScenarioAuthoringPlacement        `json:"library_placement"`
	MonitorSelection  ScenarioAuthoringMonitorSelection `json:"monitor_selection"`
}

func scenarioAuthoringETag(revision string) (string, error) {
	if !storage.ValidScenarioContentRevision(revision) {
		return "", storage.ErrScenarioAuthoringInvalidRevision
	}
	return `"` + scenarioAuthoringTagPrefix + revision + `"`, nil
}

// parseScenarioAuthoringIfMatch accepts exactly one header field holding one
// strong scenario authoring v1 tag. Header keys are matched case-insensitively
// because callers can construct header maps without canonicalization.
func parseScenarioAuthoringIfMatch(header http.Header) (string, bool, error) {
	var values []string
	fields := 0
	for key, entries := range header {
		if strings.EqualFold(key, "If-Match") {
			fields++
			values = append(values, entries...)
		}
	}
	if fields == 0 {
		return "", false, nil
	}
	if fields != 1 || len(values) != 1 {
		return "", true, storage.ErrScenarioAuthoringInvalidRevision
	}
	value := strings.Trim(values[0], " \t")
	if strings.ContainsAny(value, "\r\n") {
		return "", true, storage.ErrScenarioAuthoringInvalidRevision
	}
	prefix := `"` + scenarioAuthoringTagPrefix
	if !strings.HasPrefix(value, prefix) || !strings.HasSuffix(value, `"`) {
		return "", true, storage.ErrScenarioAuthoringInvalidRevision
	}
	revision := strings.TrimSuffix(strings.TrimPrefix(value, prefix), `"`)
	if !storage.ValidScenarioContentRevision(revision) {
		return "", true, storage.ErrScenarioAuthoringInvalidRevision
	}
	return revision, true, nil
}

// buildScenarioAuthoringProjection is a pure function of one durable snapshot,
// so the tag and the document always describe the same authored state.
func buildScenarioAuthoringProjection(snapshot *storage.ScenarioAuthoringSnapshot) (*ScenarioAuthoringProjection, error) {
	if snapshot == nil || snapshot.Scenario == nil || strings.TrimSpace(snapshot.ProjectID) == "" || !storage.ValidScenarioContentRevision(snapshot.ContentRevision) {
		return nil, storage.ErrScenarioAuthoringUnavailable
	}
	scenario := snapshot.Scenario
	groups := make([]storage.ScenarioScript, len(snapshot.Scripts))
	copy(groups, snapshot.Scripts)
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].Order < groups[j].Order })
	document := ScenarioAuthoringDocument{
		ID: scenario.ID, RefKey: scenario.RefKey, Name: scenario.Name, ProjectID: snapshot.ProjectID,
		WorkloadShapeID: scenario.WorkloadShapeID, Duration: scenario.Duration, StartDelay: scenario.StartDelay,
		TerminationMode: scenario.TerminationMode, ScenarioMode: normalizeScenarioMode(scenario.ScenarioMode),
		ScriptShakeout: scenario.ScriptShakeout, ResourceBaseline: scenario.ResourceBaseline,
		Assignment:      decodeStoredGeneratorAssignment(scenario.Assignment),
		GoalConfig:      decodeStoredGoalConfig(scenario.GoalConfig),
		TrafficProfile:  decodeStoredTrafficProfile(scenario.TrafficProfile),
		RuntimeSettings: maskRuntimeSettings(decodeStoredRuntimeSettings(scenario.RuntimeSettings)),
		Groups:          make([]ScenarioAuthoringGroup, 0, len(groups)),
		DataPools:       make([]ScenarioAuthoringDataPoolBinding, 0, len(snapshot.DataPools)),
	}
	for _, group := range groups {
		document.Groups = append(document.Groups, ScenarioAuthoringGroup{
			Name: group.Name, ScriptID: group.ScriptID, Role: group.Role, VUsers: group.VUsers,
			RampUp: group.RampUp, RampDown: group.RampDown, StartOffset: group.StartOffset,
			Stages: cloneLoadStages(group.Stages), Iterations: cloneIntPtr(group.Iterations),
			Initialization:               group.Initialization,
			RuntimeSettings:              maskRuntimeSettings(decodeStoredRuntimeSettings(group.RuntimeSettings)),
			Assignment:                   decodeStoredGeneratorAssignment(group.Assignment),
			TransactionResponseTimeSLAMs: cloneFloat64Ptr(group.TransactionResponseTimeSLAMs),
		})
	}
	for _, binding := range cloneScenarioDataPoolBindings(snapshot.DataPools) {
		document.DataPools = append(document.DataPools, ScenarioAuthoringDataPoolBinding{
			ID: binding.ID, PoolID: binding.PoolID, Alias: binding.Alias,
			AccessMode: binding.AccessMode, SelectionStrategy: binding.SelectionStrategy,
			LiveOptions: binding.LiveOptions, Scope: binding.Scope, ProjectTableID: binding.ProjectTableID,
		})
	}
	placement := ScenarioAuthoringPlacement{ReleaseIDs: []string{}}
	if assignment := snapshot.LibraryAssignment; assignment != nil {
		if assignment.Application != nil {
			id := assignment.Application.ID
			placement.ApplicationID = &id
		}
		for _, release := range assignment.Releases {
			placement.ReleaseIDs = append(placement.ReleaseIDs, release.ID)
		}
		sort.Strings(placement.ReleaseIDs)
	}
	monitors := ScenarioAuthoringMonitorSelection{Sources: []storage.MonitorBinding{}}
	if snapshot.MonitorBindings != nil {
		monitors.Sources = append(monitors.Sources, snapshot.MonitorBindings.Sources...)
		sort.Slice(monitors.Sources, func(i, j int) bool { return monitors.Sources[i].SourceID < monitors.Sources[j].SourceID })
	}
	return &ScenarioAuthoringProjection{
		ProjectionVersion: scenarioAuthoringProjectionVersion,
		ID:                scenario.ID, ProjectID: snapshot.ProjectID, ContentRevision: snapshot.ContentRevision,
		Scenario: document, LibraryPlacement: placement, MonitorSelection: monitors,
	}, nil
}

// GetScenarioAuthoringDurable reads the authored scenario and its nonce from
// one durable snapshot. Visibility follows the ordinary scenario read: a
// scenario the controller would not list (for example a pending import
// publication) is not found here either.
func (tr *TestRunner) GetScenarioAuthoringDurable(id string) (*ScenarioAuthoringProjection, bool, error) {
	tr.refreshScenariosForHARead()
	tr.mu.RLock()
	resolved := tr.resolveRefKeyLocked(strings.TrimSpace(id))
	cached := tr.scenarios[resolved]
	store, supported := tr.scenarioStore.(storage.ScenarioAuthoringStore)
	tr.mu.RUnlock()
	if cached == nil {
		return nil, false, nil
	}
	// A pending direct-import publication stays invisible, as on the
	// ordinary read; global readers bypass the RBAC lookup that hides it.
	if tr.importPublicationConfigured() && !tr.importAssetPublished("scenario", resolved) {
		return nil, false, nil
	}
	if !supported {
		return nil, false, errScenarioAuthoringUnsupported
	}
	snapshot, err := store.GetScenarioAuthoringSnapshot(resolved)
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, storage.ErrScenarioAuthoringNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	projection, err := buildScenarioAuthoringProjection(snapshot)
	if err != nil {
		return nil, false, err
	}
	return projection, true, nil
}

// writeScenarioAuthoringView serves view=authoring with its strong validator.
// Ownership is resolved before any tag is emitted, so a foreign scenario is
// indistinguishable from a missing one.
func (s *Server) writeScenarioAuthoringView(w http.ResponseWriter, r *http.Request, views []string) {
	if len(views) != 1 || views[0] != "authoring" {
		writeError(w, http.StatusBadRequest, "unsupported scenario view")
		return
	}
	projection, found, err := s.runner.GetScenarioAuthoringDurable(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "durable scenario content revisions unavailable")
		return
	}
	if !found || projection == nil {
		writeError(w, http.StatusNotFound, "scenario not found")
		return
	}
	if expected, scoped := authorizedProjectFromRequest(r); scoped && normalizeProjectID(projection.ProjectID) != expected {
		writeError(w, http.StatusNotFound, "scenario not found")
		return
	}
	tag, err := scenarioAuthoringETag(projection.ContentRevision)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "durable scenario content revision unavailable")
		return
	}
	w.Header().Set("ETag", tag)
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, projection)
}

// updateScenarioAuthoringAggregate performs the final conditioned write. It is
// called from the save builder's writer callback, which runs with tr.mu
// released, and returns the snapshot read inside the write transaction. With
// source admission the write also claims the exact script revisions the save
// was compiled from, their active state and the import publication state.
func updateScenarioAuthoringAggregate(store storage.ScenarioAuthoringStore, sources *scenarioDurableSources, scenario *storage.Scenario, scripts []storage.ScenarioScript, pools []storage.ScenarioDataPoolBinding, expected storage.ScenarioAuthoringPrecondition) (*storage.ScenarioAuthoringSnapshot, error) {
	var result *storage.ScenarioAuthoringMutationResult
	var err error
	if sources != nil {
		if !sources.bound {
			return nil, storage.ErrScenarioSourceAdmissionUnavailable
		}
		result, err = sources.store.UpdateScenarioAuthoringWithSourcesIfCurrent(scenario, scripts, pools, expected, sources.admission)
	} else {
		result, err = store.UpdateScenarioAuthoringIfCurrent(scenario, scripts, pools, expected)
	}
	if err != nil {
		return nil, err
	}
	if result == nil || !result.Applied || result.Snapshot == nil {
		return nil, storage.ErrScenarioAuthoringUnavailable
	}
	return result.Snapshot, nil
}

// UpdateScenarioAuthoringDurable runs the ordinary scenario save (secret
// protection, compilation, validation, runtime credential provisioning) and
// binds its final write to the caller's durable nonce. The save builder reads
// prior runtime credentials from the cached scenario, so the cache must still
// be the state that matched the durable snapshot at admission (cachedUpdatedAt);
// otherwise another save has committed and the admitted revision is stale. The returned snapshot is read
// inside the write transaction, so its revision is exactly what was committed.
func (tr *TestRunner) UpdateScenarioAuthoringDurable(id string, req CreateScenarioRequest, expected storage.ScenarioAuthoringPrecondition, cachedUpdatedAt time.Time) (*StoredScenario, *storage.ScenarioAuthoringSnapshot, bool, error) {
	if !storage.ValidScenarioContentRevision(expected.ContentRevision) {
		return nil, nil, false, storage.ErrScenarioAuthoringInvalidRevision
	}
	tr.scenarioMutationMu.Lock()
	defer tr.scenarioMutationMu.Unlock()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	id = tr.resolveRefKeyLocked(id)
	current, ok := tr.scenarios[id]
	if !ok {
		return nil, nil, false, nil
	}
	if !current.UpdatedAt.Equal(cachedUpdatedAt) {
		// The cache only moves after a committed save, so the revision the
		// caller was admitted against is no longer current.
		return nil, nil, true, storage.ErrScenarioAuthoringConflict
	}
	store, supported := tr.scenarioStore.(storage.ScenarioAuthoringStore)
	if !supported {
		return nil, nil, true, errScenarioAuthoringUnsupported
	}
	var committed *storage.ScenarioAuthoringSnapshot
	var sources *scenarioDurableSources
	var bind scenarioSourceBinder
	if sourceStore, admitted := tr.scenarioStore.(storage.ScenarioSourceAuthoringStore); admitted {
		sources = &scenarioDurableSources{store: sourceStore}
		bind = sources.bind
	}
	scenario, err := tr.storeScenarioLockedWithSources(id, req, bind, func(scenario *storage.Scenario, scripts []storage.ScenarioScript, pools []storage.ScenarioDataPoolBinding) error {
		snapshot, writeErr := updateScenarioAuthoringAggregate(store, sources, scenario, scripts, pools, expected)
		if writeErr != nil {
			return writeErr
		}
		// The cache is built from this scenario after the writer returns. Use
		// the committed clocks so cached and durable updated_at stay equal;
		// published-snapshot checks and later saves compare them exactly.
		scenario.CreatedAt, scenario.UpdatedAt = snapshot.Scenario.CreatedAt, snapshot.Scenario.UpdatedAt
		committed = snapshot
		return nil
	})
	return cloneStoredScenarioForReadLocked(scenario), committed, true, err
}

// attachScenarioAuthoringValidator adds the authoring projection and its
// strong tag to a scenario response, but only when the snapshot describes the
// same saved state as the response body (equal updated_at). A client that
// edits from the body and presents the tag can then never overwrite a change
// it did not see; when the two differ the response simply carries no tag.
func attachScenarioAuthoringValidator(w http.ResponseWriter, response, scenario *StoredScenario, snapshot *storage.ScenarioAuthoringSnapshot) {
	if response == nil || scenario == nil || snapshot == nil || snapshot.Scenario == nil || !snapshot.Scenario.UpdatedAt.Equal(scenario.UpdatedAt) {
		return
	}
	projection, err := buildScenarioAuthoringProjection(snapshot)
	if err != nil {
		return
	}
	tag, err := scenarioAuthoringETag(snapshot.ContentRevision)
	if err != nil {
		return
	}
	response.ContentRevision = snapshot.ContentRevision
	response.AuthoringProjection = projection
	w.Header().Set("ETag", tag)
}

// CreateScenarioAuthoringDurable creates a scenario and returns the snapshot
// read inside the create transaction, so the created baseline and its strong
// tag describe exactly what was committed. An empty id mints a new one; a
// supplied id (an idempotent create) refuses an existing scenario with
// errScenarioConcurrentChange. Stores without durable revisions keep the
// ordinary create and return no snapshot.
func (tr *TestRunner) CreateScenarioAuthoringDurable(id string, req CreateScenarioRequest) (*StoredScenario, *storage.ScenarioAuthoringSnapshot, error) {
	tr.scenarioMutationMu.Lock()
	defer tr.scenarioMutationMu.Unlock()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	id = strings.TrimSpace(id)
	createOnly := id != ""
	if !createOnly {
		id = uuid.New().String()
	}
	store, supported := tr.scenarioStore.(storage.ScenarioAuthoringStore)
	if !supported {
		if !createOnly {
			stored, err := tr.storeScenarioLocked(id, req)
			return cloneStoredScenarioForReadLocked(stored), nil, err
		}
		stored, err := tr.storeScenarioLockedWithWriter(id, req, tr.createOnlyScenarioWriterLocked(id))
		return cloneStoredScenarioForReadLocked(stored), nil, err
	}
	var committed *storage.ScenarioAuthoringSnapshot
	// The writer runs with tr.mu released, like every scenario persistence.
	stored, err := tr.storeScenarioLockedWithWriter(id, req, func(scenario *storage.Scenario, scripts []storage.ScenarioScript, pools []storage.ScenarioDataPoolBinding) error {
		snapshot, writeErr := createScenarioAuthoringAggregate(store, scenario, scripts, pools)
		if writeErr != nil {
			return writeErr
		}
		// As on a conditioned update, the cache takes the committed clocks so
		// cached and durable updated_at stay equal.
		scenario.CreatedAt, scenario.UpdatedAt = snapshot.Scenario.CreatedAt, snapshot.Scenario.UpdatedAt
		committed = snapshot
		return nil
	})
	return cloneStoredScenarioForReadLocked(stored), committed, err
}

// createScenarioAuthoringAggregate creates a scenario and returns the
// snapshot its transaction read. An existing ID is a concurrent change.
func createScenarioAuthoringAggregate(store storage.ScenarioAuthoringStore, scenario *storage.Scenario, scripts []storage.ScenarioScript, pools []storage.ScenarioDataPoolBinding) (*storage.ScenarioAuthoringSnapshot, error) {
	result, err := store.CreateScenarioAuthoringIfAbsent(scenario, scripts, pools)
	if err != nil {
		return nil, err
	}
	if result == nil || !result.Applied {
		return nil, errScenarioConcurrentChange
	}
	if result.Snapshot == nil || result.Snapshot.Scenario == nil {
		return nil, storage.ErrScenarioAuthoringOutcomeUnknown
	}
	return result.Snapshot, nil
}

// scenarioDurableSources binds one conditioned save to durable group sources.
// The build compiles from these sources rather than the replica's script
// cache, and the final write claims their revisions, so a script edited,
// archived or left unpublished by another replica refuses the save.
type scenarioDurableSources struct {
	store     storage.ScenarioSourceAuthoringStore
	admission storage.ScenarioSourceAdmission
	bound     bool
}

func (d *scenarioDurableSources) bind(snapshot *TestRunner, groups []CreateGroupEntry) error {
	refs := make([]storage.ScenarioScript, 0, len(groups))
	for _, group := range groups {
		refs = append(refs, storage.ScenarioScript{ScriptID: group.ScriptID})
	}
	durable, err := d.store.GetScenarioSourceAuthoringSnapshot(refs)
	if err != nil {
		return err
	}
	if durable == nil {
		return storage.ErrScenarioSourceAdmissionUnavailable
	}
	for _, source := range durable.Sources {
		if source == nil || source.Script == nil {
			return storage.ErrScenarioSourceAdmissionUnavailable
		}
		cached := snapshot.scripts[source.Script.ID]
		if cached == nil {
			// The replica does not know this source yet; reloading picks it up.
			return storage.ErrScenarioSourceAdmissionConflict
		}
		replaced := *cached
		replaced.Script = source.Script
		snapshot.scripts[source.Script.ID] = &replaced
	}
	d.admission, d.bound = durable.Admission, true
	return nil
}
