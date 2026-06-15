package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func newTimeslotServer(t *testing.T) (*Server, storage.Store) {
	t.Helper()
	store, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	return server, store
}

func timeslotRequest(t *testing.T, server *Server, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.mux.ServeHTTP(rec, req)
	return rec
}

func TestCapacityReservationCreateValidation(t *testing.T) {
	server, _ := newTimeslotServer(t)
	start := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)

	tests := []struct {
		name string
		body map[string]any
		want int
	}{
		{"happy path", map[string]any{"project_id": "proj-a", "start_at": start, "end_at": end, "timezone": "UTC"}, http.StatusCreated},
		{"missing project", map[string]any{"start_at": start, "end_at": end}, http.StatusBadRequest},
		{"inverted window", map[string]any{"project_id": "proj-a", "start_at": end, "end_at": start}, http.StatusBadRequest},
		{"bad timezone", map[string]any{"project_id": "proj-a", "start_at": start, "end_at": end, "timezone": "Mars/Phobos"}, http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := timeslotRequest(t, server, http.MethodPost, "/api/v1/capacity/timeslots", tc.body)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d body=%s", rec.Code, tc.want, rec.Body.String())
			}
			if tc.want == http.StatusCreated {
				var got storage.CapacityReservation
				if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
					t.Fatalf("decode: %v", err)
				}
				if got.ID == "" || got.ApprovalState != "pending" {
					t.Fatalf("created reservation = %+v, want id + pending approval", got)
				}
			}
		})
	}
}

func TestCapacityReservationListAndAvailability(t *testing.T) {
	server, _ := newTimeslotServer(t)
	start := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)
	create := timeslotRequest(t, server, http.MethodPost, "/api/v1/capacity/timeslots",
		map[string]any{"project_id": "proj-a", "start_at": start, "end_at": end})
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}

	list := timeslotRequest(t, server, http.MethodGet, "/api/v1/capacity/timeslots?project_id=proj-a", nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d", list.Code)
	}
	var listResp struct {
		Timeslots []storage.CapacityReservation `json:"timeslots"`
		Count     int                           `json:"count"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if listResp.Count != 1 || len(listResp.Timeslots) != 1 {
		t.Fatalf("list = %+v, want 1 reservation", listResp)
	}

	// An overlapping window must report unavailable with one conflict.
	avail := timeslotRequest(t, server, http.MethodPost, "/api/v1/capacity/timeslots/availability",
		map[string]any{"project_id": "proj-a", "start_at": start.Add(time.Hour), "end_at": end.Add(time.Hour)})
	if avail.Code != http.StatusOK {
		t.Fatalf("availability status = %d body=%s", avail.Code, avail.Body.String())
	}
	var availResp struct {
		Available bool `json:"available"`
		Count     int  `json:"count"`
	}
	if err := json.Unmarshal(avail.Body.Bytes(), &availResp); err != nil {
		t.Fatalf("decode availability: %v", err)
	}
	if availResp.Available || availResp.Count != 1 {
		t.Fatalf("availability = %+v, want unavailable with 1 conflict", availResp)
	}

	// A non-overlapping window is available.
	clear := timeslotRequest(t, server, http.MethodPost, "/api/v1/capacity/timeslots/availability",
		map[string]any{"project_id": "proj-a", "start_at": end.Add(time.Hour), "end_at": end.Add(2 * time.Hour)})
	var clearResp struct {
		Available bool `json:"available"`
	}
	_ = json.Unmarshal(clear.Body.Bytes(), &clearResp)
	if !clearResp.Available {
		t.Fatalf("non-overlapping window should be available, body=%s", clear.Body.String())
	}
}

func TestCapacityReservationGetNotFound(t *testing.T) {
	server, _ := newTimeslotServer(t)
	rec := timeslotRequest(t, server, http.MethodGet, "/api/v1/capacity/timeslots/missing", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 body=%s", rec.Code, rec.Body.String())
	}
}

func TestCapacityReservationBindLifecycle(t *testing.T) {
	server, store := newTimeslotServer(t)
	if err := store.SaveProject(&storage.Project{ID: "proj-ts", Name: "TS", CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("save project: %v", err)
	}
	if err := store.SaveTest(&storage.TestRecord{ID: "test-1", Name: "run", Status: "PENDING", ProjectID: "proj-ts"}); err != nil {
		t.Fatalf("save test: %v", err)
	}
	if err := store.SaveTest(&storage.TestRecord{ID: "test-other", Name: "other", Status: "PENDING", ProjectID: "proj-other"}); err != nil {
		t.Fatalf("save other test: %v", err)
	}

	start := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	create := timeslotRequest(t, server, http.MethodPost, "/api/v1/capacity/timeslots",
		map[string]any{"project_id": "proj-ts", "start_at": start, "end_at": start.Add(time.Hour)})
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	var reservation storage.CapacityReservation
	if err := json.Unmarshal(create.Body.Bytes(), &reservation); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	bindPath := "/api/v1/capacity/timeslots/" + reservation.ID + "/bind"

	// Binding a still-pending reservation is rejected.
	if rec := timeslotRequest(t, server, http.MethodPost, bindPath, map[string]any{"test_id": "test-1"}); rec.Code != http.StatusConflict {
		t.Fatalf("pending bind status = %d, want 409 body=%s", rec.Code, rec.Body.String())
	}

	// Approve via the dedicated project-admin endpoint (not a general editor update).
	approve := timeslotRequest(t, server, http.MethodPost, "/api/v1/capacity/timeslots/"+reservation.ID+"/approve", nil)
	if approve.Code != http.StatusOK {
		t.Fatalf("approve status = %d body=%s", approve.Code, approve.Body.String())
	}

	// A test from another project cannot be bound.
	if rec := timeslotRequest(t, server, http.MethodPost, bindPath, map[string]any{"test_id": "test-other"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("cross-project bind status = %d, want 400 body=%s", rec.Code, rec.Body.String())
	}

	// First valid bind succeeds.
	if rec := timeslotRequest(t, server, http.MethodPost, bindPath, map[string]any{"test_id": "test-1"}); rec.Code != http.StatusOK {
		t.Fatalf("bind status = %d, want 200 body=%s", rec.Code, rec.Body.String())
	}
	// Second bind is rejected (already bound).
	if rec := timeslotRequest(t, server, http.MethodPost, bindPath, map[string]any{"test_id": "test-1"}); rec.Code != http.StatusConflict {
		t.Fatalf("double bind status = %d, want 409 body=%s", rec.Code, rec.Body.String())
	}
}

func TestCapacityReservationUpdateCannotSelfApprove(t *testing.T) {
	server, _ := newTimeslotServer(t)
	start := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	create := timeslotRequest(t, server, http.MethodPost, "/api/v1/capacity/timeslots",
		map[string]any{"project_id": "proj-a", "start_at": start, "end_at": start.Add(time.Hour)})
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	var reservation storage.CapacityReservation
	if err := json.Unmarshal(create.Body.Bytes(), &reservation); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// A general (editor-level) update must NOT be able to flip approval to approved.
	upd := timeslotRequest(t, server, http.MethodPut, "/api/v1/capacity/timeslots/"+reservation.ID,
		map[string]any{"start_at": start, "end_at": start.Add(2 * time.Hour), "approval_state": "approved"})
	if upd.Code != http.StatusOK {
		t.Fatalf("update status = %d body=%s", upd.Code, upd.Body.String())
	}
	var updated storage.CapacityReservation
	if err := json.Unmarshal(upd.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode update: %v", err)
	}
	if updated.ApprovalState != "pending" {
		t.Fatalf("approval_state = %q after editor update, want pending (approval must not be self-granted)", updated.ApprovalState)
	}
	if !updated.EndAt.Equal(start.Add(2 * time.Hour)) {
		t.Fatalf("update should still change the window: end_at = %v", updated.EndAt)
	}
}

func TestCapacityReservationRouteAccessRequirements(t *testing.T) {
	cases := []struct {
		method     string
		path       string
		wantRoles  []string
	}{
		{http.MethodPost, "/api/v1/capacity/timeslots", []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin}},
		{http.MethodGet, "/api/v1/capacity/timeslots", []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}},
		{http.MethodPost, "/api/v1/capacity/timeslots/availability", []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}},
		{http.MethodGet, "/api/v1/capacity/timeslots/ts-1", []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}},
		{http.MethodPut, "/api/v1/capacity/timeslots/ts-1", []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin}},
		{http.MethodDelete, "/api/v1/capacity/timeslots/ts-1", []string{RoleProjectAdmin, RoleSystemAdmin}},
		{http.MethodPost, "/api/v1/capacity/timeslots/ts-1/approve", []string{RoleProjectAdmin, RoleSystemAdmin}},
		{http.MethodPost, "/api/v1/capacity/timeslots/ts-1/reject", []string{RoleProjectAdmin, RoleSystemAdmin}},
		{http.MethodPost, "/api/v1/capacity/timeslots/ts-1/bind", []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin}},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			requirement, ok := accessRequirementForRequest(req)
			if !ok {
				t.Fatalf("no access requirement matched for %s %s", tc.method, tc.path)
			}
			if len(requirement.GlobalRoles) != len(tc.wantRoles) {
				t.Fatalf("GlobalRoles = %#v, want %#v", requirement.GlobalRoles, tc.wantRoles)
			}
			for i, role := range tc.wantRoles {
				if requirement.GlobalRoles[i] != role {
					t.Fatalf("GlobalRoles[%d] = %q, want %q (full=%#v)", i, requirement.GlobalRoles[i], role, requirement.GlobalRoles)
				}
			}
		})
	}
}
