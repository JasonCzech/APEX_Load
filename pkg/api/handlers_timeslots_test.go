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
	now := time.Now().UTC()
	if err := store.SaveProject(&storage.Project{ID: "proj-a", Name: "Project A", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveProject(proj-a): %v", err)
	}
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

func TestCapacityReservationApprovalIsAtomic(t *testing.T) {
	server, _ := newTimeslotServer(t)
	start := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	rec := timeslotRequest(t, server, http.MethodPost, "/api/v1/capacity/timeslots", map[string]any{
		"project_id": "proj-a", "start_at": start, "end_at": start.Add(time.Hour), "timezone": "UTC",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d body=%s", rec.Code, rec.Body.String())
	}
	var created storage.CapacityReservation
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	approve := func() *httptest.ResponseRecorder {
		return timeslotRequest(t, server, http.MethodPost, "/api/v1/capacity/timeslots/"+created.ID+"/approve", nil)
	}
	if r := approve(); r.Code != http.StatusOK {
		t.Fatalf("first approve = %d body=%s, want 200", r.Code, r.Body.String())
	}
	// A second approve of an already-approved (no-longer-pending) slot is a 409.
	if r := approve(); r.Code != http.StatusConflict {
		t.Fatalf("second approve = %d body=%s, want 409", r.Code, r.Body.String())
	}
	// Reject can still revoke an approved slot.
	if r := timeslotRequest(t, server, http.MethodPost, "/api/v1/capacity/timeslots/"+created.ID+"/reject", nil); r.Code != http.StatusOK {
		t.Fatalf("reject after approve = %d body=%s, want 200", r.Code, r.Body.String())
	}
}

func TestLaunchTimeslotRevalidation(t *testing.T) {
	server, store := newTimeslotServer(t)
	start := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)
	vus := 50
	testID := "test-1"
	sched := start.Add(30 * time.Minute)
	mkReservation := func(id, state string, boundTo *string) {
		if err := store.SaveCapacityReservation(&storage.CapacityReservation{
			ID: id, ProjectID: "p", StartAt: start, EndAt: end,
			ApprovalState: state, BoundTestID: boundTo,
			RequestedCapacity: &storage.CapacityReservationRequest{VUsers: &vus},
		}); err != nil {
			t.Fatalf("save reservation %s: %v", id, err)
		}
	}
	mkTest := func(slotID string) *ManagedTest {
		var tid *string
		if slotID != "" {
			tid = &slotID
		}
		return &ManagedTest{ID: testID, TimeslotID: tid, ScheduledStartAt: &sched, Duration: time.Hour, TotalVUsers: 10}
	}
	now := start
	other := "other-test"
	mkReservation("ok", "approved", &testID)
	mkReservation("rej", "rejected", &testID)
	mkReservation("other", "approved", &other)
	mkReservation("toobig", "approved", &testID)

	cases := []struct {
		name    string
		test    *ManagedTest
		wantErr bool
	}{
		{"no timeslot", mkTest(""), false},
		{"approved bound fits", mkTest("ok"), false},
		{"rejected after bind", mkTest("rej"), true},
		{"bound to another run", mkTest("other"), true},
		{"missing slot", mkTest("ghost"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := server.runner.launchTimeslotRevalidationErrorLocked(tc.test, now)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
	// Approved + bound but the run now exceeds the reserved capacity -> blocked.
	big := mkTest("toobig")
	big.TotalVUsers = 9999
	if err := server.runner.launchTimeslotRevalidationErrorLocked(big, now); err == nil {
		t.Fatal("over-capacity run should block launch")
	}
}

func TestPruneTimeslotRetention(t *testing.T) {
	server, store := newTimeslotServer(t)
	t.Setenv("APEXLOAD_TIMESLOT_RETENTION_DAYS", "7")
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	if err := store.SaveCapacityReservation(&storage.CapacityReservation{ID: "old", ProjectID: "p", StartAt: now.Add(-30 * 24 * time.Hour), EndAt: now.Add(-29 * 24 * time.Hour), ApprovalState: "approved", UpdatedAt: now.Add(-29 * 24 * time.Hour)}); err != nil {
		t.Fatalf("save old: %v", err)
	}
	if err := store.SaveCapacityReservation(&storage.CapacityReservation{ID: "future", ProjectID: "p", StartAt: now.Add(24 * time.Hour), EndAt: now.Add(25 * time.Hour), ApprovalState: "approved", UpdatedAt: now}); err != nil {
		t.Fatalf("save future: %v", err)
	}
	server.pruneTimeslotRetention(now)
	if got, _ := store.GetCapacityReservation("old"); got != nil {
		t.Fatal("elapsed reservation should be pruned")
	}
	if got, _ := store.GetCapacityReservation("future"); got == nil {
		t.Fatal("future reservation should survive retention")
	}
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

func TestCapacityReservationApprovalOverbookingGuard(t *testing.T) {
	server, _ := newTimeslotServer(t)
	settings := server.runner.GetSystemSettings()
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 10
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings: %v", err)
	}
	start := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)

	create := func(name string) string {
		rec := timeslotRequest(t, server, http.MethodPost, "/api/v1/capacity/timeslots", map[string]any{
			"project_id": "proj-a", "start_at": start, "end_at": end, "timezone": "UTC",
			"requested_capacity": map[string]any{"generators": 8},
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s status = %d body=%s", name, rec.Code, rec.Body.String())
		}
		var got storage.CapacityReservation
		_ = json.Unmarshal(rec.Body.Bytes(), &got)
		return got.ID
	}
	approve := func(id string, body any) *httptest.ResponseRecorder {
		return timeslotRequest(t, server, http.MethodPost, "/api/v1/capacity/timeslots/"+id+"/approve", body)
	}

	a := create("a")
	b := create("b")

	if rec := approve(a, nil); rec.Code != http.StatusOK {
		t.Fatalf("approve first = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	// A second overlapping reservation overbooks the pool (8 + 8 > 10) -> blocked.
	if rec := approve(b, nil); rec.Code != http.StatusConflict {
		t.Fatalf("approve overbooking = %d body=%s, want 409", rec.Code, rec.Body.String())
	}
	// Override without a reason is rejected.
	if rec := approve(b, map[string]any{"conflict_override": true}); rec.Code != http.StatusBadRequest {
		t.Fatalf("override without reason = %d, want 400", rec.Code)
	}
	// Override with a reason is allowed (admin accepts the overbooking).
	rec := approve(b, map[string]any{"conflict_override": true, "reason": "approved by capacity owner"})
	if rec.Code != http.StatusOK {
		t.Fatalf("override with reason = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var got storage.CapacityReservation
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.ApprovalState != "approved" {
		t.Fatalf("overridden reservation state = %q, want approved", got.ApprovalState)
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
	now := time.Now().UTC()
	for _, project := range []*storage.Project{
		{ID: "proj-ts", Name: "TS", CreatedAt: now, UpdatedAt: now},
		{ID: "proj-other", Name: "Other", CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.SaveProject(project); err != nil {
			t.Fatalf("save project %s: %v", project.ID, err)
		}
	}
	start := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	scheduled := start
	// test-1's run window fits inside the reservation window below.
	if err := store.SaveTest(&storage.TestRecord{ID: "test-1", Name: "run", Status: "PENDING", ProjectID: "proj-ts",
		ScheduledStartAt: &scheduled, Duration: int64(30 * time.Minute)}); err != nil {
		t.Fatalf("save test: %v", err)
	}
	if err := store.SaveTest(&storage.TestRecord{ID: "test-other", Name: "other", Status: "PENDING", ProjectID: "proj-other"}); err != nil {
		t.Fatalf("save other test: %v", err)
	}

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

func TestCapacityReservationEnlargingApprovedRequiresReapproval(t *testing.T) {
	server, store := newTimeslotServer(t)
	start := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)

	// Seed an approved + bound reservation reserving 2 generators for 1h.
	boundTest := "bound-test"
	if err := store.SaveCapacityReservation(&storage.CapacityReservation{
		ID: "ts-grow", ProjectID: "proj-a",
		StartAt: start, EndAt: start.Add(time.Hour),
		ApprovalState:     "approved",
		BoundTestID:       &boundTest,
		RequestedCapacity: &storage.CapacityReservationRequest{Generators: reservedInt(2)},
	}); err != nil {
		t.Fatalf("seed reservation: %v", err)
	}

	// An editor PUT that enlarges the window+capacity must drop approval and binding.
	upd := timeslotRequest(t, server, http.MethodPut, "/api/v1/capacity/timeslots/ts-grow",
		map[string]any{
			"start_at": start, "end_at": start.Add(12 * time.Hour),
			"requested_capacity": map[string]any{"generators": 40},
		})
	if upd.Code != http.StatusOK {
		t.Fatalf("update status = %d body=%s", upd.Code, upd.Body.String())
	}
	var updated storage.CapacityReservation
	if err := json.Unmarshal(upd.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if updated.ApprovalState != "pending" {
		t.Fatalf("approval_state = %q after enlarging an approved reservation, want pending", updated.ApprovalState)
	}
	if updated.BoundTestID != nil {
		t.Fatalf("binding should be cleared on re-approval, got %v", updated.BoundTestID)
	}

	// A cosmetic edit (timezone only, same window+capacity) must preserve approval.
	if err := store.SaveCapacityReservation(&storage.CapacityReservation{
		ID: "ts-keep", ProjectID: "proj-a",
		StartAt: start, EndAt: start.Add(time.Hour),
		ApprovalState:     "approved",
		RequestedCapacity: &storage.CapacityReservationRequest{Generators: reservedInt(2)},
	}); err != nil {
		t.Fatalf("seed reservation 2: %v", err)
	}
	keep := timeslotRequest(t, server, http.MethodPut, "/api/v1/capacity/timeslots/ts-keep",
		map[string]any{
			"start_at": start, "end_at": start.Add(time.Hour), "timezone": "UTC",
			"requested_capacity": map[string]any{"generators": 2},
		})
	if keep.Code != http.StatusOK {
		t.Fatalf("cosmetic update status = %d body=%s", keep.Code, keep.Body.String())
	}
	var kept storage.CapacityReservation
	if err := json.Unmarshal(keep.Body.Bytes(), &kept); err != nil {
		t.Fatalf("decode kept: %v", err)
	}
	if kept.ApprovalState != "approved" {
		t.Fatalf("cosmetic edit must preserve approval, got %q", kept.ApprovalState)
	}
}

func TestCapacityReservationUpdateDoesNotResurrectConcurrentReject(t *testing.T) {
	server, store := newTimeslotServer(t)
	start := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	if err := store.SaveCapacityReservation(&storage.CapacityReservation{
		ID:            "ts-race",
		ProjectID:     "proj-a",
		StartAt:       start,
		EndAt:         start.Add(time.Hour),
		ApprovalState: "approved",
	}); err != nil {
		t.Fatalf("seed reservation: %v", err)
	}
	if ok, err := store.UpdateCapacityReservationApprovalState("ts-race", []string{"approved"}, "rejected", time.Now()); err != nil || !ok {
		t.Fatalf("reject reservation = (%v, %v), want (true, nil)", ok, err)
	}
	upd := timeslotRequest(t, server, http.MethodPut, "/api/v1/capacity/timeslots/ts-race",
		map[string]any{"start_at": start, "end_at": start.Add(time.Hour), "timezone": "America/New_York"})
	if upd.Code != http.StatusOK {
		t.Fatalf("update status = %d body=%s", upd.Code, upd.Body.String())
	}
	var updated storage.CapacityReservation
	if err := json.Unmarshal(upd.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode update: %v", err)
	}
	if updated.ApprovalState != "rejected" {
		t.Fatalf("approval_state = %q, want rejected preserved", updated.ApprovalState)
	}
}

func TestTerminalStatusReleasesCapacityReservationBinding(t *testing.T) {
	server, store := newTimeslotServer(t)
	start := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	testID := "test-terminal"
	status := "RUNNING"
	if err := store.SaveCapacityReservation(&storage.CapacityReservation{
		ID:              "ts-terminal",
		ProjectID:       "proj-a",
		StartAt:         start,
		EndAt:           start.Add(time.Hour),
		ApprovalState:   "approved",
		BoundTestID:     &testID,
		BoundTestStatus: &status,
	}); err != nil {
		t.Fatalf("seed reservation: %v", err)
	}
	server.runner.mu.Lock()
	server.runner.tests[testID] = &ManagedTest{ID: testID, TimeslotID: reservedStr("ts-terminal")}
	server.runner.mu.Unlock()
	end := start.Add(30 * time.Minute)
	server.runner.updateStoredTestStatus(testID, "COMPLETE", &end, "")
	got, err := store.GetCapacityReservation("ts-terminal")
	if err != nil {
		t.Fatalf("get reservation: %v", err)
	}
	if got.BoundTestID != nil || got.BoundTestStatus != nil {
		t.Fatalf("binding should be released after terminal status, got test=%v status=%v", got.BoundTestID, got.BoundTestStatus)
	}
}

func TestReservationBoundStatusRefreshedOnRead(t *testing.T) {
	server, store := newTimeslotServer(t)
	now := time.Now()
	boundID := "bound-1"
	if err := store.SaveTest(&storage.TestRecord{ID: boundID, ProjectID: "proj-a", Status: "QUEUED",
		TotalVUsers: 10, Duration: int64(10 * time.Minute)}); err != nil {
		t.Fatalf("save test: %v", err)
	}
	queued := "QUEUED"
	if err := store.SaveCapacityReservation(&storage.CapacityReservation{
		ID: "ts-r", ProjectID: "proj-a",
		StartAt: now.Add(-time.Hour), EndAt: now.Add(time.Hour),
		ApprovalState:     "approved",
		BoundTestID:       &boundID,
		BoundTestStatus:   &queued, // stale snapshot captured at bind time
		RequestedCapacity: &storage.CapacityReservationRequest{VUsers: reservedInt(100)},
	}); err != nil {
		t.Fatalf("save reservation: %v", err)
	}

	readStatus := func() *string {
		get := timeslotRequest(t, server, http.MethodGet, "/api/v1/capacity/timeslots/ts-r", nil)
		var res storage.CapacityReservation
		if err := json.Unmarshal(get.Body.Bytes(), &res); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return res.BoundTestStatus
	}

	// The bound test completes → GET reflects the live status, not stale QUEUED.
	if err := store.SaveTest(&storage.TestRecord{ID: boundID, ProjectID: "proj-a", Status: "COMPLETE",
		TotalVUsers: 10, Duration: int64(10 * time.Minute)}); err != nil {
		t.Fatalf("update test: %v", err)
	}
	if got := readStatus(); got == nil || *got != "COMPLETE" {
		t.Fatalf("bound_test_status = %v, want COMPLETE (refreshed at read)", got)
	}

	// Deleting the bound test transactionally releases the timeslot rather than
	// leaving a dangling binding for the read path to synthesize as DELETED.
	if err := store.DeleteTest(boundID); err != nil {
		t.Fatalf("delete test: %v", err)
	}
	if got := readStatus(); got != nil {
		t.Fatalf("bound_test_status = %v, want nil after deletion releases the binding", got)
	}
}

func TestStandaloneBindEnforcesWindowAndCapacity(t *testing.T) {
	server, store := newTimeslotServer(t)
	now := time.Now()
	if err := store.SaveCapacityReservation(&storage.CapacityReservation{
		ID: "ts-bind", ProjectID: "proj-a",
		StartAt: now.Add(-time.Hour), EndAt: now.Add(time.Hour),
		ApprovalState:     "approved",
		RequestedCapacity: &storage.CapacityReservationRequest{VUsers: reservedInt(100)},
	}); err != nil {
		t.Fatalf("save reservation: %v", err)
	}

	saveTest := func(id string, vusers int, dur time.Duration) {
		if err := store.SaveTest(&storage.TestRecord{
			ID: id, ProjectID: "proj-a", Status: "QUEUED",
			TotalVUsers: vusers, Duration: int64(dur),
		}); err != nil {
			t.Fatalf("save test %s: %v", id, err)
		}
	}
	bind := func(testID string) int {
		return timeslotRequest(t, server, http.MethodPost, "/api/v1/capacity/timeslots/ts-bind/bind",
			map[string]any{"test_id": testID}).Code
	}

	// VUser overflow (500 > 100 reserved) → 400.
	saveTest("over-vu", 500, 10*time.Minute)
	if code := bind("over-vu"); code != http.StatusBadRequest {
		t.Fatalf("VUser-overflow bind = %d, want 400", code)
	}
	// Window overflow (3h run, 1h window) → 400.
	saveTest("over-window", 50, 3*time.Hour)
	if code := bind("over-window"); code != http.StatusBadRequest {
		t.Fatalf("window-overflow bind = %d, want 400", code)
	}
	// A fitting test binds successfully → 200.
	saveTest("fits", 50, 10*time.Minute)
	if code := bind("fits"); code != http.StatusOK {
		t.Fatalf("fitting bind = %d, want 200", code)
	}
}

func TestCapacityReservationRouteAccessRequirements(t *testing.T) {
	cases := []struct {
		method    string
		path      string
		wantRoles []string
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
