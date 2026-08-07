package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

// createExclusivityPool creates a fleet pool and returns its id.
func createExclusivityPool(t *testing.T, server *Server, name string, members []map[string]any) string {
	t.Helper()
	body := map[string]any{"name": name, "cloud_provider": "kubernetes"}
	if members != nil {
		body["members"] = members
	}
	rec := timeslotRequest(t, server, http.MethodPost, "/api/v1/fleet/pools", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create pool %s = %d (%s)", name, rec.Code, rec.Body.String())
	}
	var payload struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode created pool %s: %v", name, err)
	}
	return payload.ID
}

// TestCreatePoolRejectsGeneratorOwnedByAnotherPool proves the API answers 409
// with the owning pool named, rather than surfacing the unique-index violation
// as a 500.
func TestCreatePoolRejectsGeneratorOwnedByAnotherPool(t *testing.T) {
	server, _ := newTimeslotServer(t)
	first := createExclusivityPool(t, server, "pool-a", []map[string]any{
		{"generator_id": "gen-1", "target": "10.0.0.1:9000"},
	})

	rec := timeslotRequest(t, server, http.MethodPost, "/api/v1/fleet/pools", map[string]any{
		"name": "pool-b", "cloud_provider": "kubernetes",
		"members": []map[string]any{{"generator_id": "gen-1", "target": "10.0.0.1:9000"}},
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("create pool-b = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "gen-1") || !strings.Contains(body, first) {
		t.Fatalf("conflict body = %s, want it to name generator gen-1 and pool %s", body, first)
	}
}

// TestPatchGeneratorRejectsCrossPoolClaim covers the other write path into
// membership: the generator metadata PATCH the fleet UI uses.
func TestPatchGeneratorRejectsCrossPoolClaim(t *testing.T) {
	server, _ := newTimeslotServer(t)
	createExclusivityPool(t, server, "pool-a", []map[string]any{
		{"generator_id": "gen-1", "target": "10.0.0.1:9000"},
	})
	second := createExclusivityPool(t, server, "pool-b", nil)

	rec := timeslotRequest(t, server, http.MethodPatch, "/api/v1/fleet/generators/gen-1", map[string]any{
		"fleet_pool_id": second, "target": "10.0.0.1:9000",
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("patch generator = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}
}

// TestStorageExclusivityViolationIsAConflictNotABadRequest covers the path where
// the pre-check does not run — a store without the lookup capability, or a race
// with a concurrent write. The unique index is the real guarantee, and its
// violation must still read as a conflict rather than a malformed request.
func TestStorageExclusivityViolationIsAConflictNotABadRequest(t *testing.T) {
	rec := httptest.NewRecorder()
	writeFleetMembershipError(rec, storage.ErrGeneratorAlreadyInPool, http.StatusBadRequest)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "already belongs to another fleet pool") {
		t.Fatalf("body = %s, want it to explain the conflict", rec.Body.String())
	}

	// An unrelated failure keeps the caller's fallback status.
	rec = httptest.NewRecorder()
	writeFleetMembershipError(rec, errors.New("min_size cannot exceed max_size"), http.StatusBadRequest)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want the 400 fallback", rec.Code)
	}
}

// TestResavingOwnMembersIsNotAConflict keeps the check from blocking ordinary
// edits: re-saving a pool with the members it already owns must succeed.
func TestResavingOwnMembersIsNotAConflict(t *testing.T) {
	server, _ := newTimeslotServer(t)
	poolID := createExclusivityPool(t, server, "pool-a", []map[string]any{
		{"generator_id": "gen-1", "target": "10.0.0.1:9000"},
	})

	rec := timeslotRequest(t, server, http.MethodPut, "/api/v1/fleet/pools/"+poolID, map[string]any{
		"name": "pool-a-renamed", "cloud_provider": "kubernetes",
		"members": []map[string]any{{"generator_id": "gen-1", "target": "10.0.0.1:9001"}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("update own pool = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
}
