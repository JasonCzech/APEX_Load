package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

type losingCollectorRotationStore struct {
	*storage.SQLiteStore
}

func (s *losingCollectorRotationStore) UpdateMonitoringCollectorTokenHashIfUnchanged(_, _, _, _ string) (bool, error) {
	return false, nil
}

func TestCollectorHeartbeatRejectsOversizedPayload(t *testing.T) {
	server, _ := newTimeslotServer(t)
	create := timeslotRequest(t, server, http.MethodPost, "/api/v1/monitoring/collectors",
		map[string]any{"project_id": "proj-a", "name": "edge", "allowed_metric_packs": []string{"linux-hosts"}})
	var createResp struct {
		Collector storage.MonitoringCollector `json:"collector"`
		Token     string                      `json:"token"`
	}
	_ = json.Unmarshal(create.Body.Bytes(), &createResp)
	id, token := createResp.Collector.ID, createResp.Token

	// Too many packs → 413 (checked before pack-scope, so any names trip it).
	tooManyPacks := make([]map[string]any, 0, maxCollectorPacks+1)
	for i := 0; i <= maxCollectorPacks; i++ {
		tooManyPacks = append(tooManyPacks, map[string]any{"pack": "linux-hosts", "samples": map[string]float64{"x": 1}})
	}
	if rec := collectorRequest(t, server, http.MethodPost, "/api/v1/monitoring/collectors/"+id+"/heartbeat", token,
		map[string]any{"packs": tooManyPacks}); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("too-many-packs heartbeat = %d, want 413", rec.Code)
	}

	// Too many samples in one pack → 413.
	samples := make(map[string]float64, maxCollectorSamplesPerPack+50)
	for i := 0; i <= maxCollectorSamplesPerPack+10; i++ {
		samples[fmt.Sprintf("m%d", i)] = float64(i)
	}
	if rec := collectorRequest(t, server, http.MethodPost, "/api/v1/monitoring/collectors/"+id+"/heartbeat", token,
		map[string]any{"packs": []map[string]any{{"pack": "linux-hosts", "samples": samples}}}); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("too-many-samples heartbeat = %d, want 413", rec.Code)
	}

	// A normal heartbeat still works.
	hb := map[string]any{"packs": []map[string]any{{"pack": "linux-hosts", "samples": map[string]float64{"cpu_pct": 9}}}}
	if rec := collectorRequest(t, server, http.MethodPost, "/api/v1/monitoring/collectors/"+id+"/heartbeat", token, hb); rec.Code != http.StatusOK {
		t.Fatalf("normal heartbeat = %d body=%s", rec.Code, rec.Body.String())
	}
}

// collectorRequest mirrors timeslotRequest but attaches a collector bearer token —
// the agent-facing heartbeat route is collector-token authenticated, not RBAC.
func collectorRequest(t *testing.T, server *Server, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	server.mux.ServeHTTP(rec, req)
	return rec
}

func TestMonitoringCollectorLifecycle(t *testing.T) {
	server, store := newTimeslotServer(t)

	// Validation.
	if rec := timeslotRequest(t, server, http.MethodPost, "/api/v1/monitoring/collectors", map[string]any{"project_id": "p"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing name status = %d, want 400", rec.Code)
	}
	if rec := timeslotRequest(t, server, http.MethodPost, "/api/v1/monitoring/collectors", map[string]any{"name": "c"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing project status = %d, want 400", rec.Code)
	}

	create := timeslotRequest(t, server, http.MethodPost, "/api/v1/monitoring/collectors",
		map[string]any{"project_id": "proj-a", "name": "edge-eu", "allowed_metric_packs": []string{"oracle-db"}})
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	var createResp struct {
		Collector storage.MonitoringCollector `json:"collector"`
		Token     string                      `json:"token"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if createResp.Token == "" {
		t.Fatal("create must return a one-time token")
	}
	if createResp.Collector.ID == "" || createResp.Collector.Health != "unreachable" {
		t.Fatalf("created collector = %+v", createResp.Collector)
	}
	id := createResp.Collector.ID

	// The token hash must never be serialized.
	if strings.Contains(create.Body.String(), "token_hash") {
		t.Fatal("token_hash must not appear in API responses")
	}

	list := timeslotRequest(t, server, http.MethodGet, "/api/v1/monitoring/collectors?project_id=proj-a", nil)
	var listResp struct {
		Collectors []storage.MonitoringCollector `json:"collectors"`
		Count      int                           `json:"count"`
	}
	_ = json.Unmarshal(list.Body.Bytes(), &listResp)
	if listResp.Count != 1 {
		t.Fatalf("list count = %d, want 1", listResp.Count)
	}

	// PATCH: valid health + invalid health.
	if rec := timeslotRequest(t, server, http.MethodPatch, "/api/v1/monitoring/collectors/"+id, map[string]any{"health": "bogus"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid health status = %d, want 400", rec.Code)
	}
	patch := timeslotRequest(t, server, http.MethodPatch, "/api/v1/monitoring/collectors/"+id, map[string]any{"health": "healthy", "description": "eu edge"})
	if patch.Code != http.StatusOK {
		t.Fatalf("patch status = %d body=%s", patch.Code, patch.Body.String())
	}

	// Rotate token: new token differs and the stored hash changes.
	hashBefore := storedCollectorHash(t, store, id)
	rotate := timeslotRequest(t, server, http.MethodPost, "/api/v1/monitoring/collectors/"+id+"/rotate-token", nil)
	if rotate.Code != http.StatusOK {
		t.Fatalf("rotate status = %d", rotate.Code)
	}
	var rotateResp struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rotate.Body.Bytes(), &rotateResp)
	if rotateResp.Token == "" || rotateResp.Token == createResp.Token {
		t.Fatalf("rotate must return a new token, got %q (orig %q)", rotateResp.Token, createResp.Token)
	}
	if hashAfter := storedCollectorHash(t, store, id); hashAfter == hashBefore {
		t.Fatal("token hash must change after rotation")
	}

	if del := timeslotRequest(t, server, http.MethodDelete, "/api/v1/monitoring/collectors/"+id, nil); del.Code != http.StatusOK {
		t.Fatalf("delete status = %d", del.Code)
	}
}

func TestMonitoringCollectorRotationConflictDoesNotReturnPlaintextToken(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "collector-rotation.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project := &storage.Project{ID: "project-a", Name: "Project A", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	collector := &storage.MonitoringCollector{
		ID: "collector-a", ProjectID: project.ID, Name: "Collector A", TokenHash: hashAPIKey("old-token"), Health: "unreachable",
	}
	if err := store.SaveMonitoringCollector(collector); err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{Store: &losingCollectorRotationStore{SQLiteStore: store}})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/monitoring/collectors/"+collector.ID+"/rotate-token", nil)
	req.SetPathValue("id", collector.ID)
	resp := httptest.NewRecorder()

	server.handleRotateMonitoringCollectorToken(resp, req)

	if resp.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusConflict, resp.Body.String())
	}
	if strings.Contains(resp.Body.String(), `"token"`) {
		t.Fatalf("conflict response returned plaintext credential: %s", resp.Body.String())
	}
}

func storedCollectorHash(t *testing.T, store storage.Store, id string) string {
	t.Helper()
	c, err := store.GetMonitoringCollector(id)
	if err != nil {
		t.Fatalf("load collector: %v", err)
	}
	return c.TokenHash
}

func TestCollectorHeartbeatAndMetrics(t *testing.T) {
	server, _ := newTimeslotServer(t)

	create := timeslotRequest(t, server, http.MethodPost, "/api/v1/monitoring/collectors",
		map[string]any{"project_id": "proj-a", "name": "edge-eu", "allowed_metric_packs": []string{"linux-hosts"}})
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	var createResp struct {
		Collector storage.MonitoringCollector `json:"collector"`
		Token     string                      `json:"token"`
	}
	_ = json.Unmarshal(create.Body.Bytes(), &createResp)
	id, token := createResp.Collector.ID, createResp.Token

	hb := map[string]any{
		"health": "healthy",
		"packs":  []map[string]any{{"pack": "linux-hosts", "samples": map[string]float64{"cpu_pct": 12.5, "mem_used_mb": 2048}}},
	}

	// No token → 401.
	if rec := collectorRequest(t, server, http.MethodPost, "/api/v1/monitoring/collectors/"+id+"/heartbeat", "", hb); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no-token heartbeat = %d, want 401", rec.Code)
	}
	// Wrong token → 401.
	if rec := collectorRequest(t, server, http.MethodPost, "/api/v1/monitoring/collectors/"+id+"/heartbeat", "srk_wrong", hb); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad-token heartbeat = %d, want 401", rec.Code)
	}
	// Disallowed pack → 422.
	bad := map[string]any{"packs": []map[string]any{{"pack": "jvm", "samples": map[string]float64{"x": 1}}}}
	if rec := collectorRequest(t, server, http.MethodPost, "/api/v1/monitoring/collectors/"+id+"/heartbeat", token, bad); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("disallowed-pack heartbeat = %d, want 422", rec.Code)
	}

	// Valid heartbeat → 200, accepted.
	ok := collectorRequest(t, server, http.MethodPost, "/api/v1/monitoring/collectors/"+id+"/heartbeat", token, hb)
	if ok.Code != http.StatusOK {
		t.Fatalf("heartbeat = %d body=%s", ok.Code, ok.Body.String())
	}
	var hbResp struct {
		Status        string   `json:"status"`
		Health        string   `json:"health"`
		AcceptedPacks []string `json:"accepted_packs"`
	}
	_ = json.Unmarshal(ok.Body.Bytes(), &hbResp)
	if hbResp.Status != "accepted" || hbResp.Health != "healthy" || len(hbResp.AcceptedPacks) != 1 {
		t.Fatalf("heartbeat response = %+v", hbResp)
	}

	// The collector row now reflects the heartbeat (health + last_seen_at).
	get := timeslotRequest(t, server, http.MethodGet, "/api/v1/monitoring/collectors/"+id, nil)
	var collector storage.MonitoringCollector
	_ = json.Unmarshal(get.Body.Bytes(), &collector)
	if collector.Health != "healthy" || collector.LastSeenAt == nil {
		t.Fatalf("collector after heartbeat = health %q last_seen %v", collector.Health, collector.LastSeenAt)
	}

	// The latest metrics snapshot is queryable and fresh.
	metrics := timeslotRequest(t, server, http.MethodGet, "/api/v1/monitoring/collectors/"+id+"/metrics", nil)
	if metrics.Code != http.StatusOK {
		t.Fatalf("metrics status = %d", metrics.Code)
	}
	var snap struct {
		Health string `json:"health"`
		Stale  bool   `json:"stale"`
		Packs  []struct {
			Pack    string             `json:"pack"`
			Samples map[string]float64 `json:"samples"`
		} `json:"packs"`
	}
	_ = json.Unmarshal(metrics.Body.Bytes(), &snap)
	if snap.Health != "healthy" || snap.Stale || len(snap.Packs) != 1 {
		t.Fatalf("metrics snapshot = %+v", snap)
	}
	if snap.Packs[0].Pack != "linux-hosts" || snap.Packs[0].Samples["cpu_pct"] != 12.5 {
		t.Fatalf("metrics pack not routed: %+v", snap.Packs[0])
	}
}

func TestMonitoringCollectorRouteAccessRequirements(t *testing.T) {
	for _, tc := range []struct {
		method    string
		path      string
		wantRoles []string
	}{
		{http.MethodPost, "/api/v1/monitoring/collectors", []string{RoleProjectAdmin, RoleSystemAdmin}},
		{http.MethodGet, "/api/v1/monitoring/collectors", []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}},
		{http.MethodPatch, "/api/v1/monitoring/collectors/c1", []string{RoleProjectAdmin, RoleSystemAdmin}},
		{http.MethodDelete, "/api/v1/monitoring/collectors/c1", []string{RoleProjectAdmin, RoleSystemAdmin}},
		{http.MethodPost, "/api/v1/monitoring/collectors/c1/rotate-token", []string{RoleProjectAdmin, RoleSystemAdmin}},
		{http.MethodGet, "/api/v1/monitoring/collectors/c1/metrics", []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			requirement, ok := accessRequirementForRequest(req)
			if !ok || len(requirement.GlobalRoles) != len(tc.wantRoles) {
				t.Fatalf("%s %s GlobalRoles = %#v, want %#v (ok=%v)", tc.method, tc.path, requirement.GlobalRoles, tc.wantRoles, ok)
			}
		})
	}
}
