package javasidecar

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRenewOwnerDoesNotTakeOver(t *testing.T) {
	s, c, _ := controlServerFixture(t)
	owner, epoch, _, _ := c.ownerAndRuntimeLease()
	frozen := &Client{baseURL: c.baseURL, httpClient: c.httpClient, authToken: c.authToken, ownerID: owner, ownerEpoch: epoch}
	s.ownerMu.Lock()
	before := s.owner.leaseDeadline
	s.ownerMu.Unlock()
	if err := frozen.RenewOwner(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.ownerMu.Lock()
	after := s.owner.leaseDeadline
	s.ownerMu.Unlock()
	if !after.After(before) {
		t.Fatal("renewal did not extend lease")
	}
	if _, err := c.ClaimOwner(context.Background(), "replacement"); err != nil {
		t.Fatal(err)
	}
	requireControlError(t, frozen.RenewOwner(context.Background()), "owner_fenced")
	if err := c.RenewOwner(context.Background()); err != nil {
		t.Fatal("stale renewal displaced owner", err)
	}
	s.ownerMu.Lock()
	s.owner.leaseDeadline = time.Now().Add(-time.Second)
	s.ownerMu.Unlock()
	if err := c.RenewOwner(context.Background()); err == nil {
		t.Fatal("expired epoch revived")
	}
}

func TestRenewOwnerValidatesReceipt(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*RenewOwnerResponse)
	}{
		{"valid", func(*RenewOwnerResponse) {}},
		{"contract", func(r *RenewOwnerResponse) { r.Contract = "other" }},
		{"owner", func(r *RenewOwnerResponse) { r.OwnerID = "other" }},
		{"epoch", func(r *RenewOwnerResponse) { r.OwnerEpoch = "other" }},
		{"renewed", func(r *RenewOwnerResponse) { r.Renewed = false }},
		{"ttl", func(r *RenewOwnerResponse) { r.LeaseTTLMS++ }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var q RenewOwnerRequest
				if r.URL.Path != "/v1/owner/renew" || r.Method != "POST" || json.NewDecoder(r.Body).Decode(&q) != nil || q.Contract != ContractVersion || q.OwnerID != "owner" || q.OwnerEpoch != "epoch" {
					t.Error("invalid request")
				}
				response := RenewOwnerResponse{Contract: ContractVersion, OwnerID: "owner", OwnerEpoch: "epoch", Renewed: true, LeaseTTLMS: RunnerOwnerLeaseTTL.Milliseconds()}
				tc.change(&response)
				_ = json.NewEncoder(w).Encode(response)
			}))
			defer h.Close()
			c, err := NewClient(h.URL, h.Client())
			if err != nil {
				t.Fatal(err)
			}
			if err = c.RenewOwner(context.Background()); err == nil {
				t.Fatal("unclaimed owner accepted")
			}
			c.ownerID, c.ownerEpoch = "owner", "epoch"
			err = c.RenewOwner(context.Background())
			if (err == nil) != (tc.name == "valid") {
				t.Fatal(err)
			}
		})
	}
}

func TestRenewOwnerRejectsInvalidControlRequests(t *testing.T) {
	s, c, _ := controlServerFixture(t)
	owner, epoch, _, _ := c.ownerAndRuntimeLease()
	for _, tc := range []struct {
		name, method, token, body string
		status                    int
	}{
		{"unauthenticated", "POST", "", `{}`, http.StatusUnauthorized},
		{"method", "GET", testSidecarAuthToken, `{}`, http.StatusMethodNotAllowed},
		{"unknown_field", "POST", testSidecarAuthToken, `{"unexpected":true}`, http.StatusBadRequest},
		{"contract", "POST", testSidecarAuthToken, `{"contract":"old"}`, http.StatusPreconditionFailed},
		{"identity", "POST", testSidecarAuthToken, `{"contract":"` + ContractVersion + `","owner_id":"bad identity","owner_epoch":"epoch"}`, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := httptest.NewRequest(tc.method, "/v1/owner/renew", strings.NewReader(tc.body))
			q.Header.Set("X-Apexload-Java-Runner-Token", tc.token)
			w := httptest.NewRecorder()
			s.ServeHTTP(w, q)
			if w.Code != tc.status {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
	// Validation failures never change the currently owned epoch.
	s.ownerMu.Lock()
	defer s.ownerMu.Unlock()
	if s.owner.ownerID != owner || s.owner.epoch != epoch {
		t.Fatal("invalid request changed ownership")
	}
}
