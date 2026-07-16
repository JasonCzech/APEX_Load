package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCreateCIRunSendsDeploymentLeaseTokenOnlyInDedicatedHeader(t *testing.T) {
	token := strings.Repeat("a", 64)
	t.Setenv("APEXLOAD_DEPLOYMENT_LEASE_TOKEN", token)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-APEXLoad-Deployment-Lease-Token"); got != token {
			t.Fatalf("lease header = %q", got)
		}
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), token) {
			t.Fatal("deployment lease token leaked into CI request body")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"schema_version":1,"test_id":"test-1","status":"QUEUED"}`)
	}))
	defer server.Close()
	client := NewApiClientWithAuth(server.URL, "key", "")
	if _, err := client.CreateCIRun(context.Background(), CIRunRequest{ProjectID: "project", ScriptID: "script"}, "idempotency"); err != nil {
		t.Fatal(err)
	}
}

func TestCreateCIRunRejectsMalformedDeploymentLeaseTokenBeforeNetwork(t *testing.T) {
	t.Setenv("APEXLOAD_DEPLOYMENT_LEASE_TOKEN", "not-a-token")
	client := NewApiClientWithAuth("https://controller.invalid", "key", "")
	if _, err := client.CreateCIRun(context.Background(), CIRunRequest{}, "idempotency"); err == nil || !strings.Contains(err.Error(), "invalid length") {
		t.Fatalf("malformed lease token error = %v", err)
	}
}
