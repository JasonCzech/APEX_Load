package javasidecar

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPooledControlClientRejectsMalformedResponses(t *testing.T) {
	_, real, _ := controlServerFixture(t)
	claim := claimControl(t, real)
	q := controlRequest(claim, "reserve", "reserve-1")
	q.VUsers = 3
	q.PrepareTimeoutMS = 1000
	valid, err := real.PooledControl(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*PooledControlResponse)
	}{
		{"script digest", func(r *PooledControlResponse) { r.Decision.ScriptSHA256 = "invalid" }},
		{"contract", func(r *PooledControlResponse) { r.Contract = "old" }},
		{"pooled contract", func(r *PooledControlResponse) { r.PooledContract = "old" }},
		{"request identity", func(r *PooledControlResponse) { r.RequestID = "another" }},
		{"run identity", func(r *PooledControlResponse) { r.RunID = "another" }},
		{"operation", func(r *PooledControlResponse) { r.Operation = "close" }},
		{"token leakage", func(r *PooledControlResponse) { r.RunToken = claim.RunToken }},
		{"missing target", func(r *PooledControlResponse) { r.Decision.TargetClass = "" }},
		{"invalid target", func(r *PooledControlResponse) { r.Decision.TargetClass = "-javaagent:bad" }},
		{"wrong mode", func(r *PooledControlResponse) { r.Decision.Mode = "dedicated" }},
		{"invalid digest", func(r *PooledControlResponse) { r.Decision.ConfigurationSHA256 = "wrong" }},
		{"missing deadline", func(r *PooledControlResponse) { r.PrepareDeadlineUnixMS = 0 }},
		{"host ceiling", func(r *PooledControlResponse) { r.Decision.MaxHosts = 1 }},
		{"memory ceiling", func(r *PooledControlResponse) { r.Decision.MaxMemoryBytes = r.Decision.HostMemoryBytes }},
		{"reused host", func(r *PooledControlResponse) { r.Placements[1].HostID = r.Placements[0].HostID }},
		{"reused slot", func(r *PooledControlResponse) { r.Placements[1].SlotIDs[0] = r.Placements[0].SlotIDs[0] }},
		{"mismatched memory", func(r *PooledControlResponse) { r.Placements[0].MemoryBytes-- }},
		{"missing slot", func(r *PooledControlResponse) { r.Placements[0].SlotIDs = r.Placements[0].SlotIDs[:1] }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := valid
			r.Placements = append([]PooledPlacement(nil), valid.Placements...)
			for i := range r.Placements {
				r.Placements[i].SlotIDs = append([]string(nil), valid.Placements[i].SlotIDs...)
			}
			tc.mutate(&r)
			h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, http.StatusOK, r) }))
			defer h.Close()
			client, err := NewClient(h.URL, h.Client())
			if err != nil {
				t.Fatal(err)
			}
			client.ownerID = "owner"
			client.ownerEpoch = "epoch"
			if _, err := client.PooledControl(context.Background(), q); err == nil || !PooledControlOutcomeAmbiguous(err) {
				t.Fatalf("malformed response accepted or called definitive: %v", err)
			}
		})
	}
}

func TestPooledControlClientErrorsAreBoundedAndClassified(t *testing.T) {
	q := PooledControlRequest{Operation: "claim", RequestID: "claim", ProfileID: "pilot", ExecutionID: "execution", GroupID: "group"}
	for _, tc := range []struct {
		code      string
		ambiguous bool
	}{
		{"pooled_capacity_unavailable", false}, {"pooled_request_conflict", false}, {"pooled_host_reaped", true}, {"pooled_cleanup_failed", true}, {"pooled_control_failed", true}, {"secret from malformed peer", true},
	} {
		t.Run(tc.code, func(t *testing.T) {
			h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { writeError(w, http.StatusConflict, tc.code) }))
			defer h.Close()
			c, err := NewClient(h.URL, h.Client())
			if err != nil {
				t.Fatal(err)
			}
			c.ownerID = "owner"
			c.ownerEpoch = "epoch"
			_, err = c.PooledControl(context.Background(), q)
			if err == nil || PooledControlOutcomeAmbiguous(err) != tc.ambiguous {
				t.Fatalf("classification: %v", err)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatal("remote error content disclosed")
			}
		})
	}
	c, err := NewClient("http://127.0.0.1:1", nil)
	if err != nil {
		t.Fatal(err)
	}
	c.ownerID = "owner"
	c.ownerEpoch = "epoch"
	q.Operation = "invoke"
	if _, err := c.PooledControl(context.Background(), q); err == nil || PooledControlOutcomeAmbiguous(err) {
		t.Fatalf("locally invalid request classification: %v", err)
	}
}

func TestPooledControlClientRequiresPositiveCloseEvidence(t *testing.T) {
	_, real, _ := controlServerFixture(t)
	claim := claimControl(t, real)
	q := controlRequest(claim, "close", "close-1")
	response, err := real.PooledControl(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	response.Reaped = false
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, http.StatusOK, response) }))
	defer h.Close()
	c, err := NewClient(h.URL, h.Client())
	if err != nil {
		t.Fatal(err)
	}
	c.ownerID = "owner"
	c.ownerEpoch = "epoch"
	if _, err := c.PooledControl(context.Background(), q); err == nil || !PooledControlOutcomeAmbiguous(err) {
		t.Fatal("unproven close accepted", err)
	}
}
