package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

const remoteRunLinksSettingKey = "remote.run_links.v1"

const (
	maxRemoteRunLinks          = 10_000
	maxRemoteRunLinksRawBytes  = 16 << 20
	remoteRunLinkCASMaxRetries = 32
)

var errRemoteRunLinkIdentityConflict = errors.New("remote run link identity conflicts with an existing authorization anchor")

// remoteRunLinksMu serializes the read-modify-write of the remote-run links
// settings blob (a single store key) so concurrent remote-run creates do not
// lost-update one another.
var remoteRunLinksMu sync.Mutex

type RemoteRunCreateRequest struct {
	CreateTestRequest
	Test             *CreateTestRequest `json:"test,omitempty"`
	RemoteProjectID  string             `json:"remote_project_id,omitempty"`
	StartImmediately bool               `json:"start_immediately,omitempty"`
	StartOptions     *StartTestOptions  `json:"start_options,omitempty"`
}

func (req *RemoteRunCreateRequest) UnmarshalJSON(data []byte) error {
	var wrapper struct {
		Test             *CreateTestRequest `json:"test"`
		RemoteProjectID  string             `json:"remote_project_id"`
		StartImmediately bool               `json:"start_immediately"`
		StartOptions     *StartTestOptions  `json:"start_options"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return err
	}
	createReq := CreateTestRequest{}
	if wrapper.Test != nil {
		createReq = *wrapper.Test
	} else if err := json.Unmarshal(data, &createReq); err != nil {
		return err
	}
	req.CreateTestRequest = createReq
	req.Test = wrapper.Test
	req.RemoteProjectID = strings.TrimSpace(wrapper.RemoteProjectID)
	req.StartImmediately = wrapper.StartImmediately
	req.StartOptions = wrapper.StartOptions
	return nil
}

type RemoteRunLink struct {
	LocalRunID      string    `json:"local_run_id"`
	RemoteID        string    `json:"remote_id"`
	RemoteTestID    string    `json:"remote_test_id"`
	RemoteURL       string    `json:"remote_url"`
	LocalProjectID  string    `json:"local_project_id,omitempty"`
	LocalScenarioID string    `json:"local_scenario_id,omitempty"`
	RemoteProjectID string    `json:"remote_project_id,omitempty"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type RemoteRunProxyResponse struct {
	Remote LibraryRemote             `json:"remote"`
	Link   RemoteRunLink             `json:"link"`
	Test   *ManagedTest              `json:"test,omitempty"`
	Action map[string]any            `json:"action,omitempty"`
	Pushed []LibraryImportResultItem `json:"pushed,omitempty"`
}

func (s *Server) handleCreateRemoteRun(w http.ResponseWriter, r *http.Request) {
	var req RemoteRunCreateRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	remote, ok, releaseCreateFence, err := s.reserveRemoteRunCreateConfig(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "remote workspace not found")
		return
	}
	defer releaseCreateFence()
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	createReq := req.CreateTestRequest
	if req.Test != nil {
		createReq = *req.Test
	}
	prepared, pushed, localProjectID, localScenarioID, remoteProjectID, err := s.prepareRemoteRunCreateRequest(r.Context(), remote, createReq, req.RemoteProjectID)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errServiceAccountLibraryBundleScope) {
			status = http.StatusForbidden
		} else if errors.Is(err, errRemoteRunScenarioPolicy) {
			status = http.StatusConflict
		}
		writeError(w, status, err.Error())
		return
	}
	// Preassign the remote identity and make the compensating delete durable
	// before issuing the cross-controller create. If this process dies after the
	// remote commit but before receiving/persisting its response, restart
	// reconciliation can still address the exact orphan.
	requestedRemoteTestID := uuid.NewString()
	prepared.RequestedTestID = requestedRemoteTestID
	cleanup := newRemoteRunCleanupRecord(remote, requestedRemoteTestID)
	if err := s.enqueueRemoteRunCleanup(cleanup); err != nil {
		writeError(w, http.StatusServiceUnavailable, "remote test was not created because its cleanup intent could not be persisted: "+err.Error())
		return
	}
	// The durable cleanup intent now fences transport/credential mutation across
	// the remaining network and link-commit steps. Retire the short-lived local
	// snapshot reservation so unrelated metadata edits are not held for the full
	// remote request duration.
	releaseCreateFence()

	var remoteTest ManagedTest
	if err := s.remoteJSON(r.Context(), remote, http.MethodPost, "/api/v1/tests", prepared, &remoteTest); err != nil {
		if cleanupErr := s.attemptRemoteRunCleanup(cleanup); cleanupErr != nil {
			slog.Warn("remote create outcome is uncertain; cleanup remains pending", "remote_id", remote.ID, "remote_test_id", requestedRemoteTestID, "err", cleanupErr)
			s.startRemoteRunCleanupRetry()
		}
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if strings.TrimSpace(remoteTest.ID) != requestedRemoteTestID {
		// A peer that does not honor requested_test_id is unsafe for recoverable
		// creation. Track any returned identity separately so both possible side
		// effects are cleaned without ever starting either one.
		if returnedID := strings.TrimSpace(remoteTest.ID); returnedID != "" {
			returnedCleanup := newRemoteRunCleanupRecord(remote, returnedID)
			if enqueueErr := s.enqueueRemoteRunCleanup(returnedCleanup); enqueueErr != nil {
				slog.Error("failed to persist mismatched remote test cleanup", "remote_id", remote.ID, "remote_test_id", returnedID, "err", enqueueErr)
				if cleanupErr := s.attemptRemoteRunCleanup(returnedCleanup); cleanupErr != nil {
					slog.Error("failed to directly clean mismatched remote test without an outbox record", "remote_id", remote.ID, "remote_test_id", returnedID, "err", cleanupErr)
				}
			} else if cleanupErr := s.attemptRemoteRunCleanup(returnedCleanup); cleanupErr != nil {
				slog.Warn("mismatched remote test cleanup remains pending", "remote_id", remote.ID, "remote_test_id", returnedID, "err", cleanupErr)
				s.startRemoteRunCleanupRetry()
			}
		}
		if cleanupErr := s.attemptRemoteRunCleanup(cleanup); cleanupErr != nil {
			slog.Warn("requested remote test cleanup remains pending", "remote_id", remote.ID, "remote_test_id", requestedRemoteTestID, "err", cleanupErr)
			s.startRemoteRunCleanupRetry()
		}
		writeError(w, http.StatusBadGateway, "remote library did not honor the requested recoverable test identity")
		return
	}
	// A remote controller that predates policy selection ignores the choice
	// and would judge the run by its project's default: never start that.
	if chosen := strings.TrimSpace(prepared.TelemetryAcceptancePolicyID); chosen != "" && chosen != runPolicyProjectDefault && remoteTest.TelemetryAcceptancePolicyID != chosen {
		if cleanupErr := s.attemptRemoteRunCleanup(cleanup); cleanupErr != nil {
			slog.Warn("unconfirmed remote telemetry policy cleanup remains pending", "remote_id", remote.ID, "remote_test_id", requestedRemoteTestID, "err", cleanupErr)
			s.startRemoteRunCleanupRetry()
		}
		writeError(w, http.StatusConflict, fmt.Sprintf("the remote controller did not confirm telemetry acceptance policy %.128q, so it would judge the run by its project's default; upgrade it, or create the run without the choice", chosen))
		return
	}
	// Likewise a controller that predates the budget choice would classify the
	// run by its system threshold settings.
	if chosen := normalizeTelemetryPolicyChoice(prepared.TelemetryBudgetPolicyID); chosen != "" && remoteTest.TelemetryBudgetPolicyID != chosen {
		if cleanupErr := s.attemptRemoteRunCleanup(cleanup); cleanupErr != nil {
			slog.Warn("unconfirmed remote telemetry budget policy cleanup remains pending", "remote_id", remote.ID, "remote_test_id", requestedRemoteTestID, "err", cleanupErr)
			s.startRemoteRunCleanupRetry()
		}
		writeError(w, http.StatusConflict, fmt.Sprintf("the remote controller did not confirm telemetry budget policy %.128q, so it would classify the run by its system threshold settings; upgrade it, or create the run without the choice", chosen))
		return
	}
	link, err := s.upsertRemoteRunLink(RemoteRunLink{
		LocalRunID:      uuid.New().String(),
		RemoteID:        remote.ID,
		RemoteTestID:    remoteTest.ID,
		RemoteURL:       remoteRunPageURL(remote.BaseURL, remoteTest.ID),
		LocalProjectID:  localProjectID,
		LocalScenarioID: localScenarioID,
		RemoteProjectID: remoteProjectID,
		Status:          remoteTest.Status,
	})
	if err != nil {
		// A remotely-created test must never be started before its local,
		// project-scoped authorization anchor is durable. The cleanup intent was
		// committed before the remote call, so cancellation and process failure do
		// not create an untracked side effect.
		if cleanupErr := s.attemptRemoteRunCleanup(cleanup); cleanupErr != nil {
			slog.Warn("unlinked remote test cleanup remains pending", "remote_id", remote.ID, "remote_test_id", remoteTest.ID, "err", cleanupErr)
			s.startRemoteRunCleanupRetry()
		}
		writeError(w, http.StatusBadGateway, "remote test was not started because its local authorization link could not be persisted: "+err.Error())
		return
	}
	if err := s.removeRemoteRunCleanup(cleanup.ID); err != nil {
		// Reconciliation checks the durable project-scoped link before deleting a
		// remote test, so a stale cleanup record can never remove this linked run.
		slog.Warn("linked remote test cleanup intent remains pending", "remote_id", remote.ID, "remote_test_id", remoteTest.ID, "err", err)
		s.startRemoteRunCleanupRetry()
	}

	var action map[string]any
	if req.StartImmediately {
		startOptions := StartTestOptions{}
		if req.StartOptions != nil {
			startOptions = *req.StartOptions
		}
		action = map[string]any{}
		status, err := s.remoteJSONAccepted(r.Context(), remote, http.MethodPost, remoteTestActionPath(remoteTest.ID, "start"), startOptions, &action, map[int]bool{http.StatusConflict: true})
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		if status == http.StatusConflict {
			slog.Warn("remote run start returned conflict", "remote_id", remote.ID, "remote_test_id", remoteTest.ID)
		}
		if refreshed, err := s.remoteRunStatus(r.Context(), remote, remoteTest.ID); err == nil {
			remoteTest = *refreshed
		}
		link.Status = remoteTest.Status
		if updated, updatedOK, updateErr := s.updateExistingRemoteRunLink(link); updateErr != nil {
			slog.Warn("failed to persist remote run status after start", "remote_id", remote.ID, "remote_test_id", remoteTest.ID, "err", updateErr)
		} else if updatedOK {
			link = updated
		}
	}
	s.recordAuditEvent(r, "remote.run.create", "success", "remote_run", remoteTest.ID, localProjectID, map[string]any{
		"remote_id":         remote.ID,
		"remote_name":       remote.Name,
		"remote_project_id": remoteProjectID,
		"start_immediately": req.StartImmediately,
		"pushed_items":      len(pushed),
	})
	writeJSON(w, http.StatusCreated, RemoteRunProxyResponse{
		Remote: s.sanitizeLibraryRemote(&remote),
		Link:   link,
		Test:   maskManagedTest(&remoteTest),
		Action: maskRemoteRunAction(action),
		Pushed: pushed,
	})
}

func (s *Server) handleGetRemoteRun(w http.ResponseWriter, r *http.Request) {
	remote, ok, err := s.snapshotLibraryRemoteConfig(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "remote workspace not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	remoteTestID := strings.TrimSpace(r.PathValue("remote_test_id"))
	link, ok := s.existingRemoteRunLink(remote.ID, remoteTestID)
	if !ok || strings.TrimSpace(link.LocalProjectID) == "" {
		// Remote credentials belong to the controller, not to the caller. Never
		// use them to discover an arbitrary remote run or to manufacture an
		// authorization anchor from a successful remote response.
		writeError(w, http.StatusNotFound, "project-scoped remote run link not found")
		return
	}
	test, err := s.remoteRunStatus(r.Context(), remote, remoteTestID)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	link.RemoteURL = remoteRunPageURL(remote.BaseURL, remoteTestID)
	link.Status = test.Status
	if updated, updatedOK, updateErr := s.updateExistingRemoteRunLink(link); updateErr != nil {
		slog.Warn("failed to persist remote run status", "remote_id", remote.ID, "remote_test_id", remoteTestID, "err", updateErr)
	} else if updatedOK {
		link = updated
	}
	writeJSON(w, http.StatusOK, RemoteRunProxyResponse{Remote: s.sanitizeLibraryRemote(&remote), Link: link, Test: maskManagedTest(test)})
}

func (s *Server) handleRemoteRunAction(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		remote, ok, err := s.snapshotLibraryRemoteConfig(r.PathValue("id"))
		if !ok {
			writeError(w, http.StatusNotFound, "remote workspace not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		remoteTestID := strings.TrimSpace(r.PathValue("remote_test_id"))
		link, ok := s.existingRemoteRunLink(remote.ID, remoteTestID)
		if !ok || strings.TrimSpace(link.LocalProjectID) == "" {
			writeError(w, http.StatusNotFound, "project-scoped remote run link not found")
			return
		}
		var body any
		if action == "start" {
			var req StartTestOptions
			if !decodeOptionalJSONBodyAllowingUnknownFields(w, r, &req) {
				return
			}
			body = req
		}
		var actionResponse map[string]any
		if _, err := s.remoteJSONAccepted(r.Context(), remote, http.MethodPost, remoteTestActionPath(remoteTestID, action), body, &actionResponse, map[int]bool{http.StatusConflict: true}); err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		var test *ManagedTest
		if refreshed, err := s.remoteRunStatus(r.Context(), remote, remoteTestID); err == nil {
			test = refreshed
		}
		status := remoteRunStatusFromAction(actionResponse)
		if test != nil && strings.TrimSpace(test.Status) != "" {
			status = test.Status
		}
		link.RemoteURL = remoteRunPageURL(remote.BaseURL, remoteTestID)
		link.Status = status
		if updated, updatedOK, updateErr := s.updateExistingRemoteRunLink(link); updateErr != nil {
			slog.Warn("failed to persist remote run action status", "remote_id", remote.ID, "remote_test_id", remoteTestID, "action", action, "err", updateErr)
		} else if updatedOK {
			link = updated
		}
		s.recordAuditEvent(r, "remote.run."+action, "success", "remote_run", remoteTestID, link.LocalProjectID, map[string]any{"remote_id": remote.ID, "remote_name": remote.Name})
		writeJSON(w, http.StatusOK, RemoteRunProxyResponse{Remote: s.sanitizeLibraryRemote(&remote), Link: link, Test: maskManagedTest(test), Action: maskRemoteRunAction(actionResponse)})
	}
}

// snapshotLibraryRemoteConfig pins a deep-copied transport configuration and its
// resolved credential. The remoteConfig lock protects only this local snapshot;
// no caller holds it while waiting on another controller.
func (s *Server) snapshotLibraryRemoteConfig(id string) (LibraryRemote, bool, error) {
	// Refresh the HA-backed state before taking remoteConfigMu. Writers acquire
	// remoteConfigMu before libraryStateMutationMu, so retaining the state lock
	// while waiting for the config lock would invert that order. The refresh is
	// complete before the config snapshot and therefore cannot expose a partially
	// applied in-memory update.
	s.libraryStateMutationMu.Lock()
	s.loadLibraryState()
	s.libraryStateMutationMu.Unlock()

	s.remoteConfigMu.RLock()
	remote, ok := s.libraryRemote(strings.TrimSpace(id))
	s.remoteConfigMu.RUnlock()
	if !ok {
		return LibraryRemote{}, false, nil
	}
	// Credential stores may be backed by a keyring or external provider. Resolve
	// them after releasing the config lock; the copied credential reference pins
	// the requested version, and a concurrent rotation/deletion can at worst make
	// this request fail closed.
	apiKey, err := s.remoteLibraryAPIKey(remote)
	if err != nil {
		return remote, true, err
	}
	remote.APIKey = apiKey
	return remote, true, nil
}

// reserveRemoteRunCreateConfig pins one transport snapshot and registers the
// small pre-outbox interval as in flight under the same lock used by remote
// update/delete. The returned release is idempotent so callers can retire the
// fence as soon as cleanup is durable while still deferring it on every error
// path.
func (s *Server) reserveRemoteRunCreateConfig(id string) (LibraryRemote, bool, func(), error) {
	if s == nil {
		return LibraryRemote{}, false, func() {}, nil
	}
	id = strings.TrimSpace(id)
	// Keep durable HA refresh I/O outside remoteConfigMu. As in the ordinary
	// snapshot path, release libraryStateMutationMu before taking the config lock
	// to preserve the writers' lock order.
	s.libraryStateMutationMu.Lock()
	s.loadLibraryState()
	s.libraryStateMutationMu.Unlock()

	s.remoteConfigMu.Lock()
	remote, ok := s.libraryRemote(id)
	if !ok {
		s.remoteConfigMu.Unlock()
		return LibraryRemote{}, false, func() {}, nil
	}
	if s.remoteRunCreates == nil {
		s.remoteRunCreates = make(map[string]int)
	}
	s.remoteRunCreates[id]++
	s.remoteConfigMu.Unlock()

	var once sync.Once
	release := func() {
		once.Do(func() {
			s.remoteConfigMu.Lock()
			if s.remoteRunCreates[id] <= 1 {
				delete(s.remoteRunCreates, id)
			} else {
				s.remoteRunCreates[id]--
			}
			s.remoteConfigMu.Unlock()
		})
	}
	// Register the short create fence before resolving credentials so a
	// concurrent update/delete cannot retire the referenced secret. Resolution
	// itself remains outside remoteConfigMu and therefore cannot stall unrelated
	// config readers or a writer that will fail fast on the reservation.
	apiKey, err := s.remoteLibraryAPIKey(remote)
	if err != nil {
		release()
		return LibraryRemote{}, true, func() {}, err
	}
	remote.APIKey = apiKey
	return remote, true, release, nil
}

func (s *Server) remoteRunCreateInFlightLocked(id string) bool {
	return s != nil && s.remoteRunCreates[strings.TrimSpace(id)] > 0
}

func maskRemoteRunAction(action map[string]any) map[string]any {
	if action == nil {
		return nil
	}
	masked := make(map[string]any, len(action))
	for key, value := range action {
		masked[key] = value
	}
	rawTest, ok := action["test"]
	if !ok || rawTest == nil {
		return masked
	}
	data, err := json.Marshal(rawTest)
	if err != nil {
		delete(masked, "test")
		return masked
	}
	var test ManagedTest
	if err := json.Unmarshal(data, &test); err != nil {
		delete(masked, "test")
		return masked
	}
	masked["test"] = maskManagedTest(&test)
	return masked
}

// errRemoteRunScenarioPolicy refuses a remote run whose scenario chose a
// telemetry acceptance or budget policy the request does not translate.
var errRemoteRunScenarioPolicy = errors.New("the scenario's telemetry policy choice is local to this controller")

func (s *Server) prepareRemoteRunCreateRequest(ctx context.Context, remote LibraryRemote, req CreateTestRequest, explicitRemoteProjectID string) (CreateTestRequest, []LibraryImportResultItem, string, string, string, error) {
	localProjectID, err := s.remoteRunCreateLocalProject(req)
	if err != nil {
		return req, nil, "", "", "", err
	}
	localScenarioID := strings.TrimSpace(req.ScenarioID)
	// A scenario's telemetry acceptance policy names a local policy, which the
	// pushed library bundle does not carry: the remote would judge the run by
	// its own copy's choice or its project's default, never the local choice.
	// The caller names the remote policy (or project_default) explicitly.
	if localScenarioID != "" && strings.TrimSpace(req.TelemetryAcceptancePolicyID) == "" {
		if scenario, ok := s.runner.GetScenario(localScenarioID); ok && scenario != nil && scenario.TelemetryAcceptancePolicyID != "" {
			return req, nil, "", "", "", fmt.Errorf("%w: scenario %s chooses telemetry acceptance policy %q, which does not travel to the remote controller; name a remote policy with telemetry_acceptance_policy_id, or %s", errRemoteRunScenarioPolicy, localScenarioID, scenario.TelemetryAcceptancePolicyID, runPolicyProjectDefault)
		}
	}
	// Likewise its telemetry budget policy: the remote would classify the run
	// by its copy's own choice or its system threshold settings.
	if localScenarioID != "" && strings.TrimSpace(req.TelemetryBudgetPolicyID) == "" {
		if scenario, ok := s.runner.GetScenario(localScenarioID); ok && scenario != nil && scenario.TelemetryBudgetPolicyID != "" {
			return req, nil, "", "", "", fmt.Errorf("%w: scenario %s chooses telemetry budget policy %q, which does not travel to the remote controller; name a remote policy with telemetry_budget_policy_id, or %s for the remote's system threshold settings", errRemoteRunScenarioPolicy, localScenarioID, scenario.TelemetryBudgetPolicyID, runPolicyProjectDefault)
		}
	}
	// The destination is an administrator-configured trust mapping. A project
	// editor may select local assets, but must not be able to redirect the
	// controller's broader remote credential into an arbitrary remote project.
	remoteProjectID, err := allowedRemoteProjectID(remote, localProjectID, explicitRemoteProjectID)
	if err != nil {
		return req, nil, "", "", "", err
	}
	pushed := make([]LibraryImportResultItem, 0)
	imported := map[string]string{}
	push := func(kind string, id string, includeDependencies bool) (string, error) {
		id = strings.TrimSpace(id)
		if id == "" {
			return "", nil
		}
		key := normalizeLibraryKind(kind) + ":" + id
		if mapped := imported[key]; mapped != "" {
			return mapped, nil
		}
		response, err := s.pushRemoteWorkspaceItem(ctx, remote, kind, id, localProjectID, remoteProjectID, includeDependencies)
		if err != nil {
			return "", err
		}
		for _, item := range response.Import.Items {
			itemKey := normalizeLibraryKind(item.Kind) + ":" + item.SourceItemID
			imported[itemKey] = item.LocalItemID
			pushed = append(pushed, item)
		}
		if mapped := imported[key]; mapped != "" {
			return mapped, nil
		}
		return "", fmt.Errorf("remote workspace did not return an imported %s id for %s", normalizeLibraryKind(kind), id)
	}
	if req.ScenarioID != "" {
		mapped, err := push("scenario", req.ScenarioID, true)
		if err != nil {
			return req, nil, "", "", "", err
		}
		req.ScenarioID = mapped
	}
	if req.ScriptID != "" {
		mapped, err := push("script", req.ScriptID, false)
		if err != nil {
			return req, nil, "", "", "", err
		}
		req.ScriptID = mapped
	}
	for i := range req.Groups {
		mapped, err := push("script", req.Groups[i].ScriptID, false)
		if err != nil {
			return req, nil, "", "", "", err
		}
		if mapped != "" {
			req.Groups[i].ScriptID = mapped
		}
	}
	req.ProjectID = remoteProjectID
	return req, pushed, localProjectID, localScenarioID, remoteProjectID, nil
}

func (s *Server) remoteRunCreateLocalProject(req CreateTestRequest) (string, error) {
	if s != nil && s.store != nil {
		return remoteRunCreateProjectFromStore(req, s.store)
	}
	projects := make(map[string]struct{})
	add := func(projectID string) {
		if strings.TrimSpace(projectID) != "" {
			projects[normalizeProjectID(strings.TrimSpace(projectID))] = struct{}{}
		}
	}
	add(req.ProjectID)
	if strings.TrimSpace(req.ScenarioID) != "" {
		scenario, ok := s.runner.GetScenario(strings.TrimSpace(req.ScenarioID))
		if !ok || scenario == nil {
			return "", fmt.Errorf("local scenario not found")
		}
		add(scenario.ProjectID)
	}
	for _, scriptID := range remoteRunCreateScriptIDs(req) {
		script, ok := s.runner.GetScript(scriptID)
		if !ok || script == nil {
			return "", fmt.Errorf("local script %s not found", scriptID)
		}
		add(script.ProjectID)
	}
	return singleRemoteRunCreateProject(projects)
}

func remoteRunCreateProjectFromStore(req CreateTestRequest, store storage.Store) (string, error) {
	if store == nil {
		return "", fmt.Errorf("remote run project storage is not configured")
	}
	projects := make(map[string]struct{})
	add := func(projectID string) {
		if strings.TrimSpace(projectID) != "" {
			projects[normalizeProjectID(strings.TrimSpace(projectID))] = struct{}{}
		}
	}
	add(req.ProjectID)
	seenScripts := make(map[string]struct{})
	addScript := func(scriptID string) error {
		scriptID = strings.TrimSpace(scriptID)
		if scriptID == "" {
			return nil
		}
		if _, seen := seenScripts[scriptID]; seen {
			return nil
		}
		seenScripts[scriptID] = struct{}{}
		script, err := getPublishedImportScript(store, scriptID)
		if err != nil || script == nil {
			return fmt.Errorf("local script %s not found", scriptID)
		}
		add(script.ProjectID)
		return nil
	}
	if strings.TrimSpace(req.ScenarioID) != "" {
		_, scripts, err := getPublishedImportScenario(store, strings.TrimSpace(req.ScenarioID))
		if err != nil {
			return "", fmt.Errorf("local scenario not found")
		}
		for _, script := range scripts {
			if err := addScript(script.ScriptID); err != nil {
				return "", err
			}
		}
	}
	for _, scriptID := range remoteRunCreateScriptIDs(req) {
		if err := addScript(scriptID); err != nil {
			return "", err
		}
	}
	return singleRemoteRunCreateProject(projects)
}

func remoteRunCreateScriptIDs(req CreateTestRequest) []string {
	ids := make([]string, 0, len(req.Groups)+1)
	if id := strings.TrimSpace(req.ScriptID); id != "" {
		ids = append(ids, id)
	}
	for _, group := range req.Groups {
		if id := strings.TrimSpace(group.ScriptID); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

func singleRemoteRunCreateProject(projects map[string]struct{}) (string, error) {
	if len(projects) == 0 {
		return storage.DefaultProjectID, nil
	}
	if len(projects) != 1 {
		return "", fmt.Errorf("remote run assets and project_id must belong to one local project")
	}
	for projectID := range projects {
		return projectID, nil
	}
	return "", fmt.Errorf("remote run local project could not be resolved")
}

func resolveRemoteRunCreateProjectID(r *http.Request, store storage.Store) (string, int, string) {
	var req RemoteRunCreateRequest
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	createReq := req.CreateTestRequest
	if req.Test != nil {
		createReq = *req.Test
	}
	projectID, err := remoteRunCreateProjectFromStore(createReq, store)
	if err != nil {
		return "", http.StatusBadRequest, err.Error()
	}
	return projectID, 0, ""
}

func (s *Server) pushRemoteWorkspaceItem(ctx context.Context, remote LibraryRemote, kind string, id string, localProjectID string, remoteProjectID string, includeDependencies bool) (*LibraryRemoteTransferResponse, error) {
	localProjectID = normalizeProjectID(localProjectID)
	bundle, err := s.buildLibraryBundle(kind, id, localProjectID, includeDependencies)
	if err != nil {
		return nil, err
	}
	if err := requireServiceAccountLibraryBundleScopesContext(ctx, bundle); err != nil {
		return nil, err
	}
	imported, err := s.remoteLibraryImport(ctx, remote, LibraryImportRequest{Bundle: *bundle, ProjectID: remoteProjectID, Mode: "push"})
	if err != nil {
		return nil, err
	}
	s.rememberRemoteImportLinks(remote.ID, bundle, imported, kind, id)
	return &LibraryRemoteTransferResponse{Remote: s.sanitizeLibraryRemote(&remote), Bundle: bundle, Import: imported}, nil
}

func (s *Server) remoteRunStatus(ctx context.Context, remote LibraryRemote, remoteTestID string) (*ManagedTest, error) {
	remoteTestID = strings.TrimSpace(remoteTestID)
	if remoteTestID == "" {
		return nil, fmt.Errorf("remote_test_id is required")
	}
	var test ManagedTest
	if err := s.remoteJSON(ctx, remote, http.MethodGet, "/api/v1/tests/"+url.PathEscape(remoteTestID), nil, &test); err != nil {
		return nil, err
	}
	return &test, nil
}

func (s *Server) localAssetProjectID(kind string, id string) string {
	switch normalizeLibraryKind(kind) {
	case "script":
		if script, ok := s.runner.GetScript(strings.TrimSpace(id)); ok && script != nil {
			return normalizeProjectID(script.ProjectID)
		}
	case "scenario":
		if scenario, ok := s.runner.GetScenario(strings.TrimSpace(id)); ok && scenario != nil {
			return normalizeProjectID(scenario.ProjectID)
		}
	case "result":
		if test, ok := s.runner.GetTest(strings.TrimSpace(id)); ok && test != nil {
			return normalizeProjectID(test.ProjectID)
		}
	}
	return ""
}

func remoteTestActionPath(remoteTestID string, action string) string {
	return "/api/v1/tests/" + url.PathEscape(strings.TrimSpace(remoteTestID)) + "/" + strings.TrimSpace(action)
}

func remoteRunPageURL(baseURL string, remoteTestID string) string {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(baseURL), "/"))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/#/command-center/" + url.PathEscape(remoteTestID)
	}
	parsed.Fragment = "/command-center/" + strings.TrimSpace(remoteTestID)
	return parsed.String()
}

func remoteRunStatusFromAction(action map[string]any) string {
	for _, key := range []string{"remote_status", "status"} {
		if value, ok := action[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (s *Server) upsertRemoteRunLink(update RemoteRunLink) (RemoteRunLink, error) {
	update.RemoteID = strings.TrimSpace(update.RemoteID)
	update.RemoteTestID = strings.TrimSpace(update.RemoteTestID)
	update.LocalRunID = strings.TrimSpace(update.LocalRunID)
	update.LocalProjectID = normalizeOptionalProjectID(update.LocalProjectID)
	update.RemoteProjectID = normalizeOptionalProjectID(update.RemoteProjectID)
	if update.RemoteID == "" || update.RemoteTestID == "" {
		return RemoteRunLink{}, fmt.Errorf("remote workspace and test IDs are required")
	}
	if update.LocalRunID == "" {
		update.LocalRunID = uuid.New().String()
	}
	return s.mutateRemoteRunLinks(func(linkSet *[]RemoteRunLink) (RemoteRunLink, bool, error) {
		links := *linkSet
		now := time.Now().UTC()
		for i := range links {
			if links[i].RemoteID != update.RemoteID || links[i].RemoteTestID != update.RemoteTestID {
				continue
			}
			if remoteRunLinkIdentityConflicts(links[i], update) {
				return RemoteRunLink{}, false, errRemoteRunLinkIdentityConflict
			}
			// Authorization identity is immutable after creation. Only fill legacy
			// empty identity fields; ordinary refreshes may update metadata only.
			fillRemoteRunLinkIdentity(&links[i], update)
			if strings.TrimSpace(update.RemoteURL) != "" {
				links[i].RemoteURL = update.RemoteURL
			}
			if strings.TrimSpace(update.Status) != "" {
				links[i].Status = update.Status
			}
			links[i].UpdatedAt = now
			return links[i], true, nil
		}
		if len(links) >= maxRemoteRunLinks {
			return RemoteRunLink{}, false, fmt.Errorf("remote run link limit of %d reached", maxRemoteRunLinks)
		}
		update.CreatedAt = now
		update.UpdatedAt = now
		links = append(links, update)
		*linkSet = links
		return update, true, nil
	})
}

func normalizeOptionalProjectID(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return normalizeProjectID(value)
}

func remoteRunLinkIdentityConflicts(existing, update RemoteRunLink) bool {
	pairs := [][2]string{
		{strings.TrimSpace(existing.LocalRunID), strings.TrimSpace(update.LocalRunID)},
		{normalizeOptionalProjectID(existing.LocalProjectID), normalizeOptionalProjectID(update.LocalProjectID)},
		{strings.TrimSpace(existing.LocalScenarioID), strings.TrimSpace(update.LocalScenarioID)},
		{normalizeOptionalProjectID(existing.RemoteProjectID), normalizeOptionalProjectID(update.RemoteProjectID)},
	}
	for _, pair := range pairs {
		if pair[0] != "" && pair[1] != "" && pair[0] != pair[1] {
			return true
		}
	}
	return false
}

func fillRemoteRunLinkIdentity(existing *RemoteRunLink, update RemoteRunLink) {
	if existing.LocalRunID == "" {
		existing.LocalRunID = update.LocalRunID
	}
	if strings.TrimSpace(existing.LocalProjectID) == "" {
		existing.LocalProjectID = update.LocalProjectID
	}
	if strings.TrimSpace(existing.LocalScenarioID) == "" {
		existing.LocalScenarioID = update.LocalScenarioID
	}
	if strings.TrimSpace(existing.RemoteProjectID) == "" {
		existing.RemoteProjectID = update.RemoteProjectID
	}
}

// existingRemoteRunLink returns only a durable, project-scoped authorization
// anchor created by the remote-run creation workflow. Legacy unscoped entries
// intentionally fail closed and must be migrated by an administrator instead
// of being claimed by the first caller who knows a remote test ID.
func (s *Server) existingRemoteRunLink(remoteID, remoteTestID string) (RemoteRunLink, bool) {
	remoteRunLinksMu.Lock()
	defer remoteRunLinksMu.Unlock()
	links, err := loadRemoteRunLinksFromStore(s.store)
	if err != nil {
		slog.Warn("failed to load remote run links", "err", err)
		return RemoteRunLink{}, false
	}
	return findProjectScopedRemoteRunLink(links, remoteID, remoteTestID)
}

// updateExistingRemoteRunLink refreshes status metadata without ever creating
// a link. This invariant prevents GET/action routes from turning a remote
// credential lookup into a new local authorization record.
func (s *Server) updateExistingRemoteRunLink(update RemoteRunLink) (RemoteRunLink, bool, error) {
	updated, err := s.mutateRemoteRunLinks(func(linkSet *[]RemoteRunLink) (RemoteRunLink, bool, error) {
		links := *linkSet
		for i := range links {
			if links[i].RemoteID != strings.TrimSpace(update.RemoteID) || links[i].RemoteTestID != strings.TrimSpace(update.RemoteTestID) || strings.TrimSpace(links[i].LocalProjectID) == "" {
				continue
			}
			if strings.TrimSpace(update.RemoteURL) != "" {
				links[i].RemoteURL = update.RemoteURL
			}
			if strings.TrimSpace(update.Status) != "" {
				links[i].Status = update.Status
			}
			links[i].UpdatedAt = time.Now().UTC()
			return links[i], true, nil
		}
		return RemoteRunLink{}, false, nil
	})
	if err != nil {
		return RemoteRunLink{}, false, err
	}
	return updated, updated.RemoteID != "", nil
}

func findProjectScopedRemoteRunLink(links []RemoteRunLink, remoteID, remoteTestID string) (RemoteRunLink, bool) {
	remoteID = strings.TrimSpace(remoteID)
	remoteTestID = strings.TrimSpace(remoteTestID)
	if remoteID == "" || remoteTestID == "" {
		return RemoteRunLink{}, false
	}
	for _, link := range links {
		if link.RemoteID == remoteID && link.RemoteTestID == remoteTestID && strings.TrimSpace(link.LocalProjectID) != "" {
			link.LocalProjectID = normalizeProjectID(link.LocalProjectID)
			return link, true
		}
	}
	return RemoteRunLink{}, false
}

func (s *Server) loadRemoteRunLinks() []RemoteRunLink {
	if s == nil {
		return nil
	}
	links, err := loadRemoteRunLinksFromStore(s.store)
	if err != nil {
		slog.Warn("failed to decode remote run links", "err", err)
		return nil
	}
	return links
}

func loadRemoteRunLinksFromStore(store interface {
	GetSetting(string) (string, error)
}) ([]RemoteRunLink, error) {
	if store == nil {
		return nil, fmt.Errorf("remote run link storage is not configured")
	}
	raw, err := store.GetSetting(remoteRunLinksSettingKey)
	if err != nil {
		return nil, err
	}
	return decodeRemoteRunLinks(raw)
}

func decodeRemoteRunLinks(raw string) ([]RemoteRunLink, error) {
	if len(raw) > maxRemoteRunLinksRawBytes {
		return nil, fmt.Errorf("remote run links exceed the %d-byte limit", maxRemoteRunLinksRawBytes)
	}
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var links []RemoteRunLink
	if err := json.Unmarshal([]byte(raw), &links); err != nil {
		return nil, err
	}
	if len(links) > maxRemoteRunLinks {
		return nil, fmt.Errorf("remote run links exceed the %d-entry limit", maxRemoteRunLinks)
	}
	seen := make(map[string]struct{}, len(links))
	for i := range links {
		links[i].RemoteID = strings.TrimSpace(links[i].RemoteID)
		links[i].RemoteTestID = strings.TrimSpace(links[i].RemoteTestID)
		if links[i].RemoteID == "" || links[i].RemoteTestID == "" {
			return nil, fmt.Errorf("remote run link %d has an empty identity", i)
		}
		key := links[i].RemoteID + "\x00" + links[i].RemoteTestID
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("remote run links contain duplicate identity %q/%q", links[i].RemoteID, links[i].RemoteTestID)
		}
		seen[key] = struct{}{}
	}
	return links, nil
}

func (s *Server) mutateRemoteRunLinks(mutate func(*[]RemoteRunLink) (RemoteRunLink, bool, error)) (RemoteRunLink, error) {
	if s == nil || s.store == nil {
		return RemoteRunLink{}, fmt.Errorf("remote run link storage is not configured")
	}
	remoteRunLinksMu.Lock()
	defer remoteRunLinksMu.Unlock()
	for attempt := 0; attempt < remoteRunLinkCASMaxRetries; attempt++ {
		raw, err := s.store.GetSetting(remoteRunLinksSettingKey)
		if err != nil {
			return RemoteRunLink{}, fmt.Errorf("load remote run links: %w", err)
		}
		links, err := decodeRemoteRunLinks(raw)
		if err != nil {
			return RemoteRunLink{}, fmt.Errorf("decode remote run links: %w", err)
		}
		result, changed, err := mutate(&links)
		if err != nil || !changed {
			return result, err
		}
		payload, err := json.Marshal(links)
		if err != nil {
			return RemoteRunLink{}, fmt.Errorf("encode remote run links: %w", err)
		}
		if len(payload) > maxRemoteRunLinksRawBytes {
			return RemoteRunLink{}, fmt.Errorf("remote run links exceed the %d-byte limit", maxRemoteRunLinksRawBytes)
		}
		encoded := string(payload)
		if cas, ok := s.store.(librarySettingsCASStore); ok {
			updated, err := cas.CompareAndSwapSetting(remoteRunLinksSettingKey, raw, encoded)
			if err != nil {
				return RemoteRunLink{}, fmt.Errorf("persist remote run links: %w", err)
			}
			if !updated {
				continue
			}
			return result, nil
		}
		if s.runner != nil && s.runner.leadership != nil {
			return RemoteRunLink{}, fmt.Errorf("persist remote run links: HA storage must support compare-and-swap settings")
		}
		if err := s.store.SaveSetting(remoteRunLinksSettingKey, encoded); err != nil {
			return RemoteRunLink{}, fmt.Errorf("persist remote run links: %w", err)
		}
		return result, nil
	}
	return RemoteRunLink{}, fmt.Errorf("persist remote run links: shared settings changed concurrently; retry the request")
}
