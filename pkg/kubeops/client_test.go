package kubeops

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

func TestClientDeletePod(t *testing.T) {
	client, cleanup := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requireBearer(t, r, "token-a")
		if r.Method != http.MethodDelete {
			t.Fatalf("method = %s, want DELETE", r.Method)
		}
		if r.URL.Path != "/api/v1/namespaces/apex/pods/apexload-generator-0" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer cleanup()

	if err := client.DeletePod(context.Background(), "apex", "apexload-generator-0"); err != nil {
		t.Fatalf("DeletePod() error = %v", err)
	}
}

func TestClientRolloutRestartPatchesWorkloadTemplateAnnotation(t *testing.T) {
	tests := []struct {
		name     string
		kind     string
		wantPath string
	}{
		{
			name:     "deployment",
			kind:     "deployment",
			wantPath: "/apis/apps/v1/namespaces/apex/deployments/apexload-controller",
		},
		{
			name:     "statefulset",
			kind:     "statefulset",
			wantPath: "/apis/apps/v1/namespaces/apex/statefulsets/apexload-generator",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client, cleanup := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requireBearer(t, r, "token-a")
				if r.Method != http.MethodPatch {
					t.Fatalf("method = %s, want PATCH", r.Method)
				}
				if r.URL.Path != tc.wantPath {
					t.Fatalf("path = %q, want %q", r.URL.Path, tc.wantPath)
				}
				if got := r.Header.Get("Content-Type"); !strings.Contains(got, "application/strategic-merge-patch+json") {
					t.Fatalf("Content-Type = %q", got)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatalf("decode patch body: %v", err)
				}
				restartedAt := nestedString(body, "spec", "template", "metadata", "annotations", "kubectl.kubernetes.io/restartedAt")
				if restartedAt == "" {
					t.Fatalf("patch body missing restartedAt annotation: %#v", body)
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer cleanup()

			if err := client.RolloutRestart(context.Background(), tc.kind, "apex", strings.TrimPrefix(tc.wantPath[strings.LastIndex(tc.wantPath, "/"):], "/")); err != nil {
				t.Fatalf("RolloutRestart() error = %v", err)
			}
		})
	}
}

func TestClientPodByIP(t *testing.T) {
	tests := []struct {
		name    string
		pods    []map[string]any
		want    string
		wantErr error
	}{
		{
			name: "match",
			pods: []map[string]any{
				podItem("apexload-generator-0", "10.0.0.10"),
				podItem("apexload-generator-1", "10.0.0.11"),
			},
			want: "apexload-generator-1",
		},
		{
			name: "no match",
			pods: []map[string]any{
				podItem("apexload-generator-0", "10.0.0.10"),
			},
			wantErr: ErrPodNotFound,
		},
		{
			name: "multiple matches",
			pods: []map[string]any{
				podItem("apexload-generator-0", "10.0.0.11"),
				podItem("apexload-generator-1", "10.0.0.11"),
			},
			wantErr: ErrPodMultipleMatches,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client, cleanup := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requireBearer(t, r, "token-a")
				if r.Method != http.MethodGet {
					t.Fatalf("method = %s, want GET", r.Method)
				}
				if r.URL.Path != "/api/v1/namespaces/apex/pods" {
					t.Fatalf("path = %q", r.URL.Path)
				}
				if got := r.URL.Query().Get("labelSelector"); got != "app=generator" {
					t.Fatalf("labelSelector = %q", got)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"items": tc.pods})
			}))
			defer cleanup()

			got, err := client.PodByIP(context.Background(), "apex", "app=generator", "10.0.0.11")
			if tc.wantErr != nil {
				if err != tc.wantErr {
					t.Fatalf("PodByIP() error = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("PodByIP() error = %v", err)
			}
			if got.Name != tc.want {
				t.Fatalf("PodByIP().Name = %q, want %q", got.Name, tc.want)
			}
		})
	}
}

func TestClientRereadsTokenAndRetriesAfterAuthFailure(t *testing.T) {
	dir := t.TempDir()
	tokenFile := dir + "/token"
	if err := os.WriteFile(tokenFile, []byte("expired\n"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}

	var attempts atomic.Int32
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch attempts.Add(1) {
		case 1:
			requireBearer(t, r, "expired")
			if err := os.WriteFile(tokenFile, []byte("fresh\n"), 0o600); err != nil {
				t.Fatalf("rotate token: %v", err)
			}
			http.Error(w, "expired", http.StatusUnauthorized)
		case 2:
			requireBearer(t, r, "fresh")
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
		default:
			t.Fatalf("unexpected attempt %d", attempts.Load())
		}
	}))
	defer server.Close()

	caFile := writeServerCA(t, dir, server)
	client, err := NewInClusterClient(Config{
		Namespace:    "apex",
		APIServerURL: server.URL,
		TokenFile:    tokenFile,
		CAFile:       caFile,
	})
	if err != nil {
		t.Fatalf("NewInClusterClient() error = %v", err)
	}

	if _, err := client.ListPods(context.Background(), "apex", "app=generator"); err != nil {
		t.Fatalf("ListPods() error = %v", err)
	}
	if attempts.Load() != 2 {
		t.Fatalf("attempts = %d, want 2", attempts.Load())
	}
}

func TestClientReportsInvalidCA(t *testing.T) {
	dir := t.TempDir()
	tokenFile := dir + "/token"
	caFile := dir + "/ca.crt"
	if err := os.WriteFile(tokenFile, []byte("token-a\n"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	if err := os.WriteFile(caFile, []byte("not a certificate"), 0o600); err != nil {
		t.Fatalf("write CA: %v", err)
	}
	client, err := NewInClusterClient(Config{
		Namespace:    "apex",
		APIServerURL: "https://127.0.0.1:65535",
		TokenFile:    tokenFile,
		CAFile:       caFile,
	})
	if err != nil {
		t.Fatalf("NewInClusterClient() error = %v", err)
	}

	_, err = client.ListPods(context.Background(), "apex", "app=generator")
	if err == nil || !strings.Contains(err.Error(), "PEM certificate") {
		t.Fatalf("ListPods() error = %v, want PEM certificate error", err)
	}
}

func TestClientCachesHTTPClientAcrossCalls(t *testing.T) {
	dir := t.TempDir()
	tokenFile := dir + "/token"
	if err := os.WriteFile(tokenFile, []byte("token-a\n"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
	}))
	defer server.Close()

	caFile := writeServerCA(t, dir, server)
	client, err := NewInClusterClient(Config{
		Namespace:    "apex",
		APIServerURL: server.URL,
		TokenFile:    tokenFile,
		CAFile:       caFile,
	})
	if err != nil {
		t.Fatalf("NewInClusterClient() error = %v", err)
	}

	if _, err := client.ListPods(context.Background(), "apex", "app=generator"); err != nil {
		t.Fatalf("first ListPods() error = %v", err)
	}
	// Corrupt the CA on disk. A client rebuilt per call would re-read it and fail;
	// a cached client keeps working.
	if err := os.WriteFile(caFile, []byte("not a certificate"), 0o600); err != nil {
		t.Fatalf("corrupt CA: %v", err)
	}
	if _, err := client.ListPods(context.Background(), "apex", "app=generator"); err != nil {
		t.Fatalf("second ListPods() after CA corruption error = %v; client was not cached", err)
	}
}

func newTestClient(t *testing.T, handler http.Handler) (*Client, func()) {
	t.Helper()
	dir := t.TempDir()
	tokenFile := dir + "/token"
	if err := os.WriteFile(tokenFile, []byte("token-a\n"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	server := httptest.NewTLSServer(handler)
	caFile := writeServerCA(t, dir, server)
	client, err := NewInClusterClient(Config{
		Namespace:         "apex",
		GeneratorSelector: "app=generator",
		APIServerURL:      server.URL,
		TokenFile:         tokenFile,
		CAFile:            caFile,
	})
	if err != nil {
		server.Close()
		t.Fatalf("NewInClusterClient() error = %v", err)
	}
	return client, server.Close
}

func writeServerCA(t *testing.T, dir string, server *httptest.Server) string {
	t.Helper()
	cert := server.Certificate()
	if _, err := x509.ParseCertificate(cert.Raw); err != nil {
		t.Fatalf("parse server certificate: %v", err)
	}
	caFile := dir + "/ca.crt"
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	if err := os.WriteFile(caFile, pemBytes, 0o600); err != nil {
		t.Fatalf("write CA: %v", err)
	}
	return caFile
}

func requireBearer(t *testing.T, r *http.Request, token string) {
	t.Helper()
	if got := r.Header.Get("Authorization"); got != "Bearer "+token {
		t.Fatalf("Authorization = %q, want Bearer %s", got, token)
	}
}

func podItem(name string, ip string) map[string]any {
	return map[string]any{
		"metadata": map[string]any{
			"name":      name,
			"namespace": "apex",
		},
		"status": map[string]any{
			"podIP": ip,
			"phase": "Running",
		},
	}
}

func nestedString(value map[string]any, keys ...string) string {
	var current any = value
	for _, key := range keys {
		m, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = m[key]
	}
	out, _ := current.(string)
	return out
}
