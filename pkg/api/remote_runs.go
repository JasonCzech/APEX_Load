package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const remoteRunLinksSettingKey = "remote.run_links.v1"

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
	remote, ok := s.libraryRemote(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "remote workspace not found")
		return
	}
	var req RemoteRunCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	createReq := req.CreateTestRequest
	if req.Test != nil {
		createReq = *req.Test
	}
	prepared, pushed, localProjectID, localScenarioID, remoteProjectID, err := s.prepareRemoteRunCreateRequest(r.Context(), remote, createReq, req.RemoteProjectID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var remoteTest ManagedTest
	if err := s.remoteJSON(r.Context(), remote, http.MethodPost, "/api/v1/tests", prepared, &remoteTest); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
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
	}

	link := s.upsertRemoteRunLink(RemoteRunLink{
		LocalRunID:      uuid.New().String(),
		RemoteID:        remote.ID,
		RemoteTestID:    remoteTest.ID,
		RemoteURL:       remoteRunPageURL(remote.BaseURL, remoteTest.ID),
		LocalProjectID:  localProjectID,
		LocalScenarioID: localScenarioID,
		RemoteProjectID: remoteProjectID,
		Status:          remoteTest.Status,
	})
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
		Test:   &remoteTest,
		Action: action,
		Pushed: pushed,
	})
}

func (s *Server) handleGetRemoteRun(w http.ResponseWriter, r *http.Request) {
	remote, ok := s.libraryRemote(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "remote workspace not found")
		return
	}
	remoteTestID := strings.TrimSpace(r.PathValue("remote_test_id"))
	test, err := s.remoteRunStatus(r.Context(), remote, remoteTestID)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	link := s.upsertRemoteRunLink(RemoteRunLink{
		RemoteID:     remote.ID,
		RemoteTestID: remoteTestID,
		RemoteURL:    remoteRunPageURL(remote.BaseURL, remoteTestID),
		Status:       test.Status,
	})
	writeJSON(w, http.StatusOK, RemoteRunProxyResponse{Remote: s.sanitizeLibraryRemote(&remote), Link: link, Test: test})
}

func (s *Server) handleRemoteRunAction(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		remote, ok := s.libraryRemote(r.PathValue("id"))
		if !ok {
			writeError(w, http.StatusNotFound, "remote workspace not found")
			return
		}
		remoteTestID := strings.TrimSpace(r.PathValue("remote_test_id"))
		var body any
		if action == "start" {
			var req StartTestOptions
			if r.Body != nil && r.ContentLength != 0 {
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
					return
				}
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
		link := s.upsertRemoteRunLink(RemoteRunLink{
			RemoteID:     remote.ID,
			RemoteTestID: remoteTestID,
			RemoteURL:    remoteRunPageURL(remote.BaseURL, remoteTestID),
			Status:       status,
		})
		s.recordAuditEvent(r, "remote.run."+action, "success", "remote_run", remoteTestID, link.LocalProjectID, map[string]any{"remote_id": remote.ID, "remote_name": remote.Name})
		writeJSON(w, http.StatusOK, RemoteRunProxyResponse{Remote: s.sanitizeLibraryRemote(&remote), Link: link, Test: test, Action: actionResponse})
	}
}

func (s *Server) prepareRemoteRunCreateRequest(ctx context.Context, remote LibraryRemote, req CreateTestRequest, explicitRemoteProjectID string) (CreateTestRequest, []LibraryImportResultItem, string, string, string, error) {
	localProjectID := normalizeProjectID(req.ProjectID)
	localScenarioID := strings.TrimSpace(req.ScenarioID)
	if localProjectID == "" {
		localProjectID = s.localAssetProjectID("scenario", localScenarioID)
	}
	remoteProjectID := normalizeProjectID(firstNonEmpty(explicitRemoteProjectID, remote.DefaultProjectID, req.ProjectID))
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
		response, err := s.pushRemoteWorkspaceItem(ctx, remote, kind, id, remoteProjectID, includeDependencies)
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
		if localProjectID == "" {
			localProjectID = s.localAssetProjectID("script", req.ScriptID)
		}
		mapped, err := push("script", req.ScriptID, false)
		if err != nil {
			return req, nil, "", "", "", err
		}
		req.ScriptID = mapped
	}
	for i := range req.Groups {
		if localProjectID == "" {
			localProjectID = s.localAssetProjectID("script", req.Groups[i].ScriptID)
		}
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

func (s *Server) pushRemoteWorkspaceItem(ctx context.Context, remote LibraryRemote, kind string, id string, remoteProjectID string, includeDependencies bool) (*LibraryRemoteTransferResponse, error) {
	bundle, err := s.buildLibraryBundle(kind, id, remoteProjectID, includeDependencies)
	if err != nil {
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

func (s *Server) upsertRemoteRunLink(update RemoteRunLink) RemoteRunLink {
	remoteRunLinksMu.Lock()
	defer remoteRunLinksMu.Unlock()
	now := time.Now().UTC()
	links := s.loadRemoteRunLinks()
	for i := range links {
		if links[i].RemoteID == update.RemoteID && links[i].RemoteTestID == update.RemoteTestID {
			if strings.TrimSpace(update.LocalRunID) != "" {
				links[i].LocalRunID = update.LocalRunID
			}
			if strings.TrimSpace(update.RemoteURL) != "" {
				links[i].RemoteURL = update.RemoteURL
			}
			if strings.TrimSpace(update.LocalProjectID) != "" {
				links[i].LocalProjectID = update.LocalProjectID
			}
			if strings.TrimSpace(update.LocalScenarioID) != "" {
				links[i].LocalScenarioID = update.LocalScenarioID
			}
			if strings.TrimSpace(update.RemoteProjectID) != "" {
				links[i].RemoteProjectID = update.RemoteProjectID
			}
			if strings.TrimSpace(update.Status) != "" {
				links[i].Status = update.Status
			}
			links[i].UpdatedAt = now
			s.saveRemoteRunLinks(links)
			return links[i]
		}
	}
	if strings.TrimSpace(update.LocalRunID) == "" {
		update.LocalRunID = uuid.New().String()
	}
	update.CreatedAt = now
	update.UpdatedAt = now
	links = append(links, update)
	s.saveRemoteRunLinks(links)
	return update
}

func (s *Server) loadRemoteRunLinks() []RemoteRunLink {
	if s == nil || s.store == nil {
		return nil
	}
	raw, err := s.store.GetSetting(remoteRunLinksSettingKey)
	if err != nil || strings.TrimSpace(raw) == "" {
		return nil
	}
	var links []RemoteRunLink
	if err := json.Unmarshal([]byte(raw), &links); err != nil {
		slog.Warn("failed to decode remote run links", "err", err)
		return nil
	}
	return links
}

func (s *Server) saveRemoteRunLinks(links []RemoteRunLink) {
	if s == nil || s.store == nil {
		return
	}
	payload, err := json.Marshal(links)
	if err != nil {
		slog.Warn("failed to encode remote run links", "err", err)
		return
	}
	if err := s.store.SaveSetting(remoteRunLinksSettingKey, string(payload)); err != nil {
		slog.Warn("failed to persist remote run links", "err", err)
	}
}
