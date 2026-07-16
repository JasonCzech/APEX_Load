package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func deploymentLeaseHandlerRequest(method, token, body string, scopes ...string) *http.Request {
	req := httptest.NewRequest(method, "/api/v1/system/deployment-lease", strings.NewReader(body))
	if token != "" {
		req.Header.Set(deploymentLeaseTokenHeader, token)
	}
	if len(scopes) == 0 {
		scopes = []string{"deployments:manage"}
	}
	return withAuthenticatedContext(req, authResolution{ServiceAccount: &AuthenticatedServiceAccount{ID: "deploy-service", Name: "Deployment workflow", Scopes: scopes}})
}

func TestDeploymentLeaseAcquireRequiresAtomicReviewedOverrideAndKeepsCredentialsOutOfEvidence(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "deployment-lease.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	if err := store.SaveTest(&storage.TestRecord{
		ID: "active-run", Name: "active", ProjectID: storage.DefaultProjectID,
		Status: "RUNNING", StartTime: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SaveTest: %v", err)
	}
	server := &Server{store: store}
	token := strings.Repeat("a", 64)
	owner := "github:octo/apex:9001:2"

	blockedBody, _ := json.Marshal(deploymentLeaseRequest{OwnerID: owner, Token: token, TTLSeconds: 600})
	blocked := httptest.NewRecorder()
	server.handleAcquireDeploymentLease(blocked, deploymentLeaseHandlerRequest(http.MethodPost, token, string(blockedBody)))
	if blocked.Code != http.StatusConflict || !strings.Contains(blocked.Body.String(), `"active_tests":1`) {
		t.Fatalf("unapproved acquire = %d %s, want active-test conflict", blocked.Code, blocked.Body.String())
	}

	approvedBody, _ := json.Marshal(deploymentLeaseRequest{
		OwnerID: owner, Token: token, TTLSeconds: 600, AllowActiveTests: true,
		Context: storage.DeploymentLeaseContext{
			Release: "v1.2.3", Environment: "production", ApprovalID: "CHG-12345",
		},
	})
	ordinaryCredential := httptest.NewRecorder()
	server.handleAcquireDeploymentLease(ordinaryCredential, deploymentLeaseHandlerRequest(http.MethodPost, token, string(approvedBody)))
	if ordinaryCredential.Code != http.StatusForbidden {
		t.Fatalf("ordinary deployment credential override = %d %s, want 403", ordinaryCredential.Code, ordinaryCredential.Body.String())
	}

	approved := httptest.NewRecorder()
	server.handleAcquireDeploymentLease(approved, deploymentLeaseHandlerRequest(
		http.MethodPost,
		token,
		string(approvedBody),
		"deployments:manage",
		deploymentActiveTestsOverrideScope,
	))
	if approved.Code != http.StatusOK || !strings.Contains(approved.Body.String(), `"active_tests":1`) {
		t.Fatalf("approved acquire = %d %s, want acquired lease with active count", approved.Code, approved.Body.String())
	}
	if strings.Contains(approved.Body.String(), token) || strings.Contains(approved.Body.String(), deploymentLeaseTokenHash(token)) {
		t.Fatal("lease response disclosed the raw token or its durable digest")
	}

	events, err := store.ListAuditEvents(storage.AuditEventFilter{Action: "system.deployment_lease.acquire", Limit: 10})
	if err != nil {
		t.Fatalf("ListAuditEvents: %v", err)
	}
	if len(events) != 3 || events[0].Status != "success" || events[1].Status != "denied" || events[2].Status != "blocked" {
		t.Fatalf("audit events = %#v, want success, denied, then blocked", events)
	}
	evidence, err := json.Marshal(events)
	if err != nil {
		t.Fatalf("marshal audit evidence: %v", err)
	}
	for _, prohibited := range []string{token, deploymentLeaseTokenHash(token), "approval_reason", "confidential approval reason"} {
		if bytes.Contains(evidence, []byte(prohibited)) {
			t.Fatalf("audit evidence contains prohibited content %q: %s", prohibited, evidence)
		}
	}
}

func TestDeploymentLeaseRequestRejectsUnreviewedAndAcquisitionOnlyFields(t *testing.T) {
	token := strings.Repeat("b", 64)
	server := &Server{}
	tests := []struct {
		name   string
		handle func(http.ResponseWriter, *http.Request)
		body   string
	}{
		{
			name: "unknown approval reason is never accepted",
			handle: func(w http.ResponseWriter, r *http.Request) {
				server.handleAcquireDeploymentLease(w, r)
			},
			body: `{"owner_id":"github:octo/apex:9001:2","allow_active_tests":true,"context":{"approval_id":"CHG-1","approval_reason":"confidential approval reason"}}`,
		},
		{
			name: "override requires approval id",
			handle: func(w http.ResponseWriter, r *http.Request) {
				server.handleAcquireDeploymentLease(w, r)
			},
			body: `{"owner_id":"github:octo/apex:9001:2","allow_active_tests":true}`,
		},
		{
			name: "renew rejects override flag",
			handle: func(w http.ResponseWriter, r *http.Request) {
				server.handleRenewDeploymentLease(w, r)
			},
			body: `{"owner_id":"github:octo/apex:9001:2","allow_active_tests":true}`,
		},
		{
			name: "release rejects context",
			handle: func(w http.ResponseWriter, r *http.Request) {
				server.handleReleaseDeploymentLease(w, r)
			},
			body: `{"owner_id":"github:octo/apex:9001:2","context":{"approval_id":"CHG-1"}}`,
		},
		{
			name: "credential in request body",
			handle: func(w http.ResponseWriter, r *http.Request) {
				server.handleAcquireDeploymentLease(w, r)
			},
			body: `{"owner_id":"github:octo/apex:9001:2","token":"` + token + `"}`,
		},
		{
			name: "overflowing ttl",
			handle: func(w http.ResponseWriter, r *http.Request) {
				server.handleAcquireDeploymentLease(w, r)
			},
			body: `{"owner_id":"github:octo/apex:9001:2","ttl_seconds":9223372036854775807}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			test.handle(response, deploymentLeaseHandlerRequest(http.MethodPost, token, test.body))
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body=%s, want 400", response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), "confidential approval reason") {
				t.Fatal("validation response reflected confidential input")
			}
		})
	}
}

func TestDeploymentLeaseRequestAllowsRollbackSafeTTLAndRejectsLongerLease(t *testing.T) {
	token := strings.Repeat("d", 64)
	for _, test := range []struct {
		name       string
		ttlSeconds int
		wantOK     bool
	}{
		{name: "rollback safe production lease", ttlSeconds: 3600, wantOK: true},
		{name: "lease above server maximum", ttlSeconds: 3601},
	} {
		t.Run(test.name, func(t *testing.T) {
			body, err := json.Marshal(deploymentLeaseRequest{
				OwnerID:    "github:octo/apex:9001:2",
				TTLSeconds: test.ttlSeconds,
			})
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			_, ttl, ok := decodeAndValidateDeploymentLeaseRequest(
				response,
				deploymentLeaseHandlerRequest(http.MethodPost, token, string(body)),
				true,
			)
			if ok != test.wantOK {
				t.Fatalf("accepted = %t, status = %d, body = %s", ok, response.Code, response.Body.String())
			}
			if test.wantOK && ttl != time.Hour {
				t.Fatalf("ttl = %s, want 1h", ttl)
			}
			if !test.wantOK && (response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "between 60 and 3600")) {
				t.Fatalf("rejection = %d %s, want bounded TTL validation", response.Code, response.Body.String())
			}
		})
	}
}

func TestDeploymentLeaseReleaseRetryReturnsTheSameSuccessfulOutcome(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "deployment-lease-release-retry.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	server := &Server{store: store}
	token := strings.Repeat("c", 64)
	owner := "github:octo/apex:9001:2"

	acquireBody, _ := json.Marshal(deploymentLeaseRequest{
		OwnerID: owner, TTLSeconds: 600,
		Context: storage.DeploymentLeaseContext{Release: "v1.2.3", Environment: "production", RunID: "9001"},
	})
	acquired := httptest.NewRecorder()
	server.handleAcquireDeploymentLease(acquired, deploymentLeaseHandlerRequest(http.MethodPost, token, string(acquireBody)))
	if acquired.Code != http.StatusOK {
		t.Fatalf("acquire = %d %s", acquired.Code, acquired.Body.String())
	}

	releaseBody, _ := json.Marshal(deploymentLeaseRequest{OwnerID: owner})
	for attempt := 1; attempt <= 2; attempt++ {
		released := httptest.NewRecorder()
		server.handleReleaseDeploymentLease(released, deploymentLeaseHandlerRequest(http.MethodPost, token, string(releaseBody)))
		if released.Code != http.StatusOK || !strings.Contains(released.Body.String(), `"status":"released"`) {
			t.Fatalf("release attempt %d = %d %s", attempt, released.Code, released.Body.String())
		}
		if strings.Contains(released.Body.String(), token) || strings.Contains(released.Body.String(), deploymentLeaseTokenHash(token)) {
			t.Fatalf("release attempt %d disclosed the token or durable digest", attempt)
		}
	}

	events, err := store.ListAuditEvents(storage.AuditEventFilter{Action: "system.deployment_lease.release", Limit: 10})
	if err != nil || len(events) != 2 || events[0].Status != "success" || events[1].Status != "success" {
		t.Fatalf("release retry audit events = %#v, %v", events, err)
	}
	evidence, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(evidence, []byte(token)) || bytes.Contains(evidence, []byte(deploymentLeaseTokenHash(token))) {
		t.Fatal("release retry audit evidence disclosed the token or durable digest")
	}
}
