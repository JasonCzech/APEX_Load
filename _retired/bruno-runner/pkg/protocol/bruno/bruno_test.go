package bruno

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/importartifact"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func acknowledgedSession(request createSessionRequest) createSessionResponse {
	return createSessionResponse{
		SessionID: request.SessionID, SessionLeaseToken: request.SessionLeaseToken,
		LeaseTTLMS: runnerSessionLeaseTTL.Milliseconds(),
	}
}

func TestCreateSessionRenewsLeaseWhileProvisioning(t *testing.T) {
	var mu sync.Mutex
	var sessionID, leaseToken string
	renewed := make(chan struct{})
	var renewedOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/v1/sessions":
			var payload createSessionRequest
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				http.Error(response, err.Error(), http.StatusBadRequest)
				return
			}
			mu.Lock()
			sessionID, leaseToken = payload.SessionID, payload.SessionLeaseToken
			mu.Unlock()
			select {
			case <-renewed:
				response.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(response).Encode(acknowledgedSession(payload))
			case <-time.After(3 * runnerSessionLeaseRenew):
				http.Error(response, "session lease was not renewed during provisioning", http.StatusGatewayTimeout)
			}
		case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/lease/renew"):
			mu.Lock()
			wantPath := "/v1/sessions/" + sessionID + "/lease/renew"
			wantToken := leaseToken
			mu.Unlock()
			if request.URL.Path != wantPath || request.Header.Get("X-Apexload-Session-Lease-Token") != wantToken {
				http.Error(response, "wrong session lease generation", http.StatusConflict)
				return
			}
			renewedOnce.Do(func() { close(renewed) })
			_ = json.NewEncoder(response).Encode(map[string]any{
				"renewed": true, "lease_ttl_ms": runnerSessionLeaseTTL.Milliseconds(),
			})
		default:
			http.Error(response, "unexpected route", http.StatusNotFound)
		}
	}))
	defer server.Close()

	instance := &Protocol{
		client:        server.Client(),
		runnerURL:     server.URL,
		bundleID:      "bundle-provisioning",
		sandbox:       "developer",
		compatibility: CompatibilityVersion,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	gotSessionID, gotLeaseToken, err := instance.createSession(ctx, nil)
	if err != nil {
		t.Fatalf("createSession: %v", err)
	}
	mu.Lock()
	wantSessionID, wantLeaseToken := sessionID, leaseToken
	mu.Unlock()
	if gotSessionID != wantSessionID || gotLeaseToken != wantLeaseToken {
		t.Fatalf("created session generation = %q/%q, want %q/%q", gotSessionID, gotLeaseToken, wantSessionID, wantLeaseToken)
	}
}

func TestProtocolLifecycleAndStreamingResults(t *testing.T) {
	t.Setenv("APEXLOAD_BRUNO_DISTRIBUTED_EXECUTION_ENABLED", "false")
	t.Setenv("APEXLOAD_BRUNO_RUNNER_OWNER_LEASE_V1", "false")
	t.Setenv("APEXLOAD_BRUNO_MAX_JUMPS", "50")
	t.Setenv("APEXLOAD_BRUNO_MAX_RUN_REQUEST_DEPTH", "8")
	t.Setenv("APEXLOAD_BRUNO_MAX_PARALLEL_REQUESTS", "2")
	t.Setenv("APEXLOAD_BRUNO_MAX_OUTPUT_BYTES", "524288")
	t.Setenv("APEXLOAD_BRUNO_MAX_STREAM_TIMEOUT_MS", "5000")

	bundle := []byte("deterministic tar bytes")
	sum := sha256.Sum256(bundle)
	digest := hex.EncodeToString(sum[:])
	restore := importartifact.SetResolver(importartifact.ResolverFunc(func(_ context.Context, request importartifact.Request) ([]byte, error) {
		if request.Reference != "artifact://bundle" || request.ProjectID != "project-a" {
			return nil, fmt.Errorf("unexpected artifact request: %#v", request)
		}
		return bundle, nil
	}))
	defer restore()

	var mu sync.Mutex
	var opened, created, executed, cancelled, sessionClosed, bundleClosed bool
	var runnerSessionID string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/v1/health":
			_ = json.NewEncoder(response).Encode(map[string]any{"compatibility_version": CompatibilityVersion})
		case request.Method == http.MethodPost && request.URL.Path == "/v1/bundles/open":
			data, _ := io.ReadAll(request.Body)
			if string(data) != string(bundle) || request.Header.Get("X-Bruno-SHA256") != digest {
				t.Errorf("opened bundle/header = %q/%q", data, request.Header.Get("X-Bruno-SHA256"))
			}
			opened = true
			response.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(response).Encode(openBundleResponse{BundleID: "bundle-1"})
		case request.Method == http.MethodPost && request.URL.Path == "/v1/sessions":
			var payload createSessionRequest
			_ = json.NewDecoder(request.Body).Decode(&payload)
			if payload.BundleID != "bundle-1" || payload.ProjectID != "project-a" || payload.Sandbox != "safe" || payload.VUserID != 7 {
				t.Errorf("session payload = %#v", payload)
			}
			if strings.Join(payload.AllowedTargetHosts, ",") != "api.example.test,*.assets.example.test" {
				t.Errorf("target policy = %#v", payload.AllowedTargetHosts)
			}
			if payload.Limits["max_jumps"] != 50 ||
				payload.Limits["max_run_request_depth"] != 8 ||
				payload.Limits["max_parallel_requests"] != 2 ||
				payload.Limits["max_output_bytes"] != 524288 ||
				payload.Limits["default_stream_timeout_ms"] != 5000 {
				t.Errorf("operator-clamped runner limits = %#v", payload.Limits)
			}
			created = true
			runnerSessionID = payload.SessionID
			response.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(response).Encode(acknowledgedSession(payload))
		case request.Method == http.MethodPost && request.URL.Path == "/v1/sessions/"+runnerSessionID+"/execute":
			executed = true
			response.Header().Set("Content-Type", "application/x-ndjson")
			events := []runnerEvent{
				{Type: "result", Result: runnerResult{
					ActionName:    "SOAP request",
					DurationMS:    12.5,
					StatusCode:    500,
					Success:       true,
					SourcePath:    "Execute.yml",
					SourceUID:     "uid-1",
					RequestType:   "http",
					OperationKind: "request",
				}},
				{Type: "result", Result: runnerResult{
					ActionName:    "Response status code is 500",
					DurationMS:    0.5,
					Success:       true,
					OperationKind: "test",
				}},
				{Type: "complete", Result: runnerResult{
					ActionName: "Run collection",
					DurationMS: 15,
					Success:    true,
				}},
			}
			for _, event := range events {
				_ = json.NewEncoder(response).Encode(event)
			}
		case request.Method == http.MethodPost && request.URL.Path == "/v1/sessions/"+runnerSessionID+"/cancel":
			cancelled = true
			response.WriteHeader(http.StatusNoContent)
		case request.Method == http.MethodDelete && request.URL.Path == "/v1/sessions/"+runnerSessionID:
			sessionClosed = true
			response.WriteHeader(http.StatusNoContent)
		case request.Method == http.MethodDelete && request.URL.Path == "/v1/bundles/bundle-1":
			bundleClosed = true
			response.WriteHeader(http.StatusNoContent)
		default:
			http.Error(response, "unexpected route", http.StatusNotFound)
		}
	}))
	defer server.Close()

	instance := &Protocol{}
	if err := instance.Init(context.Background(), map[string]any{
		"project_id":                      "project-a",
		"vuser_id":                        float64(7),
		"bruno_runner_url":                server.URL,
		"bruno_bundle_ref":                "artifact://bundle",
		"bruno_bundle_sha256":             digest,
		"bruno_compatibility_version":     CompatibilityVersion,
		"bruno_sandbox":                   "safe",
		"bruno_default_stream_timeout_ms": float64(10_000),
		"bruno_max_output_bytes":          float64(1 << 20),
		"bruno_max_jumps":                 float64(100),
		"bruno_max_run_request_depth":     float64(10),
		"bruno_max_parallel_requests":     float64(4),
		protocol.TargetPolicyConfigKey:    []string{"api.example.test", "*.assets.example.test"},
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := instance.HealthCheck(context.Background()); err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
	var children []*protocol.Result
	aggregate, err := instance.ExecuteStream(context.Background(), &protocol.Action{
		Name: "Run collection",
		Type: "bruno_run",
		Params: map[string]any{
			"selection":         "collection",
			"stream_timeout_ms": float64(10_000),
		},
	}, func(result *protocol.Result) error {
		children = append(children, result)
		return nil
	})
	if err != nil {
		t.Fatalf("ExecuteStream: %v", err)
	}
	if len(children) != 2 || children[0].SourcePath != "Execute.yml" || children[0].StatusCode != 500 || children[1].OperationKind != "test" {
		t.Fatalf("children = %#v", children)
	}
	if aggregate == nil || aggregate.SampleKind != "aggregate" || !aggregate.Success {
		t.Fatalf("aggregate = %#v", aggregate)
	}
	if err := instance.Teardown(context.Background()); err != nil {
		t.Fatalf("Teardown: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !opened || !created || !executed || cancelled || !sessionClosed || !bundleClosed {
		t.Fatalf("lifecycle opened=%v created=%v executed=%v cancelled=%v sessionClosed=%v bundleClosed=%v", opened, created, executed, cancelled, sessionClosed, bundleClosed)
	}
}

func TestProtocolSharesBundleAcrossVUsers(t *testing.T) {
	bundle := []byte("shared bundle")
	sum := sha256.Sum256(bundle)
	digest := hex.EncodeToString(sum[:])
	restore := importartifact.SetResolver(importartifact.ResolverFunc(func(context.Context, importartifact.Request) ([]byte, error) {
		return bundle, nil
	}))
	defer restore()

	var mu sync.Mutex
	opens, closes, sessions := 0, 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/v1/bundles/open":
			opens++
			response.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(response).Encode(openBundleResponse{BundleID: "shared"})
		case request.Method == http.MethodPost && request.URL.Path == "/v1/sessions":
			sessions++
			var payload createSessionRequest
			_ = json.NewDecoder(request.Body).Decode(&payload)
			response.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(response).Encode(acknowledgedSession(payload))
		case request.Method == http.MethodDelete && strings.HasPrefix(request.URL.Path, "/v1/sessions/"):
			response.WriteHeader(http.StatusNoContent)
		case request.Method == http.MethodDelete && request.URL.Path == "/v1/bundles/shared":
			closes++
			response.WriteHeader(http.StatusNoContent)
		default:
			http.Error(response, "unexpected", http.StatusNotFound)
		}
	}))
	defer server.Close()

	config := map[string]any{
		"project_id":                  "project-a",
		"bruno_runner_url":            server.URL,
		"bruno_bundle_ref":            "artifact://bundle",
		"bruno_bundle_sha256":         digest,
		"bruno_compatibility_version": CompatibilityVersion,
		"bruno_sandbox":               "safe",
	}
	first, second := &Protocol{}, &Protocol{}
	if err := first.Init(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	if err := second.Init(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	if err := first.Teardown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := second.Teardown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if opens != 1 || closes != 1 || sessions != 2 {
		t.Fatalf("opens=%d closes=%d sessions=%d", opens, closes, sessions)
	}
}

func TestProtocolRejectsRunnerRedirectBeforeReplayingSessionSecrets(t *testing.T) {
	bundle := []byte("redirect-safe bundle")
	sum := sha256.Sum256(bundle)
	digest := hex.EncodeToString(sum[:])
	var redirected atomic.Int64
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirected.Add(1)
	}))
	defer destination.Close()

	source := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/v1/bundles/open":
			response.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(response).Encode(openBundleResponse{BundleID: "redirect-bundle"})
		case request.Method == http.MethodPost && request.URL.Path == "/v1/sessions":
			response.Header().Set("Location", destination.URL+"/capture")
			response.WriteHeader(http.StatusTemporaryRedirect)
		case request.Method == http.MethodDelete && request.URL.Path == "/v1/bundles/redirect-bundle":
			response.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(response, request)
		}
	}))
	defer source.Close()

	instance := &Protocol{}
	err := instance.Init(context.Background(), map[string]any{
		"project_id":                  "project-redirect",
		"bruno_runner_url":            source.URL,
		"bruno_bundle_bytes":          bundle,
		"bruno_bundle_sha256":         digest,
		"bruno_compatibility_version": CompatibilityVersion,
		"bruno_sandbox":               "safe",
		"bruno_secret_bindings":       map[string]string{"API_TOKEN": "redirect-test-material"},
	})
	if err == nil {
		t.Fatal("Init unexpectedly followed a runner redirect")
	}
	if redirected.Load() != 0 {
		t.Fatal("redirect destination received the secret-bearing session request")
	}
}

func TestRunnerHealthRejectsControlRedirects(t *testing.T) {
	var redirected atomic.Int64
	destination := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		redirected.Add(1)
		_ = json.NewEncoder(response).Encode(map[string]string{"compatibility_version": CompatibilityVersion})
	}))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Location", destination.URL+"/v1/health")
		response.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	t.Setenv("APEXLOAD_BRUNO_RUNNER_URL", source.URL)

	runnerHealthCache.Lock()
	runnerHealthCache.url = ""
	runnerHealthCache.checkedAt = time.Time{}
	runnerHealthCache.healthy = false
	runnerHealthCache.Unlock()
	if RunnerHealthy(context.Background()) {
		t.Fatal("redirecting runner health endpoint was accepted")
	}
	if redirected.Load() != 0 {
		t.Fatal("runner health check followed a control-plane redirect")
	}
}

func TestRuntimeCapabilitiesRequireRunnerOwnerLeaseEvidence(t *testing.T) {
	restore := importartifact.SetResolver(importartifact.ResolverFunc(func(context.Context, importartifact.Request) ([]byte, error) {
		return []byte("bundle"), nil
	}))
	t.Cleanup(restore)
	t.Setenv("APEXLOAD_BRUNO_DISTRIBUTED_EXECUTION_ENABLED", "true")
	t.Setenv("APEXLOAD_BRUNO_ALLOW_DIRECT_EGRESS", "true")
	for _, test := range []struct {
		name              string
		ownerLeaseEnabled string
		reportedFeatures  []string
		wantAvailable     bool
	}{
		{name: "operator lease disabled", ownerLeaseEnabled: "false", reportedFeatures: []string{runnerOwnerLeaseFeature, runnerSessionLeaseFeature}},
		{name: "runner omits leases", ownerLeaseEnabled: "true"},
		{name: "runner reports owner lease only", ownerLeaseEnabled: "true", reportedFeatures: []string{runnerOwnerLeaseFeature}},
		{name: "runner reports session lease only", ownerLeaseEnabled: "true", reportedFeatures: []string{runnerSessionLeaseFeature}},
		{name: "operator and runner require both leases", ownerLeaseEnabled: "true", reportedFeatures: []string{runnerOwnerLeaseFeature, runnerSessionLeaseFeature}, wantAvailable: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("APEXLOAD_BRUNO_RUNNER_OWNER_LEASE_V1", test.ownerLeaseEnabled)
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				switch request.URL.Path {
				case "/v1/health":
					_ = json.NewEncoder(response).Encode(map[string]any{"compatibility_version": CompatibilityVersion})
				case "/v1/capabilities":
					_ = json.NewEncoder(response).Encode(map[string]any{
						"compatibility_version": CompatibilityVersion,
						"features":              test.reportedFeatures,
						"cleanup_failed":        false,
					})
				default:
					http.NotFound(response, request)
				}
			}))
			defer server.Close()
			t.Setenv("APEXLOAD_BRUNO_RUNNER_URL", server.URL)
			runnerHealthCache.Lock()
			runnerHealthCache.url = ""
			runnerHealthCache.checkedAt = time.Time{}
			runnerHealthCache.healthy = false
			runnerHealthCache.features = nil
			runnerHealthCache.Unlock()

			capabilities := (&Protocol{}).RuntimeCapabilities()
			if capabilities.Available != test.wantAvailable {
				t.Fatalf("runtime available = %v, want %v (features %v)", capabilities.Available, test.wantAvailable, capabilities.Features)
			}
			for _, feature := range []string{runnerOwnerLeaseFeature, runnerSessionLeaseFeature} {
				if got := protocol.RuntimeFeaturesSatisfy(capabilities.Features, feature); got != test.wantAvailable {
					t.Fatalf("feature %q advertised = %v in %v, want %v", feature, got, capabilities.Features, test.wantAvailable)
				}
			}
			if got := DistributedExecutionAvailable(); got != test.wantAvailable {
				t.Fatalf("DistributedExecutionAvailable() = %v, want %v", got, test.wantAvailable)
			}
			if !RunnerHealthy(context.Background()) {
				t.Fatal("compatible runner with capabilities endpoint was unhealthy")
			}
		})
	}
}

func TestInitRejectsDistributedBrunoWhenRunnerLeasesAreDisabled(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()
	t.Setenv("APEXLOAD_BRUNO_DISTRIBUTED_EXECUTION_ENABLED", "true")
	t.Setenv("APEXLOAD_BRUNO_RUNNER_OWNER_LEASE_V1", "false")

	err := (&Protocol{}).Init(context.Background(), map[string]any{
		"bruno_runner_url": server.URL,
	})
	if err == nil || !strings.Contains(err.Error(), runnerOwnerLeaseFeature) || !strings.Contains(err.Error(), runnerSessionLeaseFeature) {
		t.Fatalf("Init error = %v, want both required runner lease features", err)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("runner requests = %d, want none before distributed lease admission", got)
	}
}

func TestInitRejectsDistributedBrunoWhenRunnerOmitsLeaseFeature(t *testing.T) {
	for _, test := range []struct {
		name     string
		features []string
	}{
		{name: "both leases", features: nil},
		{name: "session lease", features: []string{runnerOwnerLeaseFeature}},
		{name: "owner lease", features: []string{runnerSessionLeaseFeature}},
	} {
		t.Run("missing "+test.name, func(t *testing.T) {
			var ownerClaims atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				switch request.URL.Path {
				case "/v1/health":
					_ = json.NewEncoder(response).Encode(map[string]any{"compatibility_version": CompatibilityVersion})
				case "/v1/capabilities":
					_ = json.NewEncoder(response).Encode(map[string]any{
						"compatibility_version": CompatibilityVersion,
						"features":              test.features,
						"cleanup_failed":        false,
					})
				case "/v1/owner/claim":
					ownerClaims.Add(1)
					http.Error(response, "owner claim must not be reached", http.StatusInternalServerError)
				default:
					http.NotFound(response, request)
				}
			}))
			defer server.Close()
			t.Setenv("APEXLOAD_BRUNO_DISTRIBUTED_EXECUTION_ENABLED", "true")
			t.Setenv("APEXLOAD_BRUNO_RUNNER_OWNER_LEASE_V1", "true")

			err := (&Protocol{}).Init(context.Background(), map[string]any{
				"bruno_runner_url": server.URL,
			})
			if err == nil || !strings.Contains(err.Error(), runnerOwnerLeaseFeature) || !strings.Contains(err.Error(), runnerSessionLeaseFeature) {
				t.Fatalf("Init error = %v, want both required runner lease features", err)
			}
			if got := ownerClaims.Load(); got != 0 {
				t.Fatalf("owner claims = %d, want none before distributed lease feature admission", got)
			}
		})
	}
}

func TestInitAcceptsDistributedBrunoWithRunnerLeaseContract(t *testing.T) {
	bundle := []byte("distributed lease contract bundle")
	sum := sha256.Sum256(bundle)
	digest := hex.EncodeToString(sum[:])
	var ownerClaims atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/v1/health":
			_ = json.NewEncoder(response).Encode(map[string]any{"compatibility_version": CompatibilityVersion})
		case request.Method == http.MethodGet && request.URL.Path == "/v1/capabilities":
			_ = json.NewEncoder(response).Encode(map[string]any{
				"compatibility_version": CompatibilityVersion,
				"features":              []string{runnerOwnerLeaseFeature, runnerSessionLeaseFeature},
				"cleanup_failed":        false,
			})
		case request.Method == http.MethodPost && request.URL.Path == "/v1/owner/claim":
			var claim map[string]any
			_ = json.NewDecoder(request.Body).Decode(&claim)
			ownerClaims.Add(1)
			_ = json.NewEncoder(response).Encode(map[string]any{
				"owner_id":     claim["owner_id"],
				"owner_epoch":  strings.Repeat("a", 64),
				"lease_ttl_ms": runnerOwnerLeaseTTL.Milliseconds(),
				"accepted":     true,
			})
		case request.Method == http.MethodPost && request.URL.Path == "/v1/bundles/open":
			response.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(response).Encode(openBundleResponse{BundleID: "distributed-bundle"})
		case request.Method == http.MethodPost && request.URL.Path == "/v1/sessions":
			var payload createSessionRequest
			_ = json.NewDecoder(request.Body).Decode(&payload)
			response.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(response).Encode(acknowledgedSession(payload))
		case request.Method == http.MethodDelete:
			response.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	t.Setenv("APEXLOAD_BRUNO_DISTRIBUTED_EXECUTION_ENABLED", "true")
	t.Setenv("APEXLOAD_BRUNO_RUNNER_OWNER_LEASE_V1", "true")

	instance := &Protocol{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := instance.Init(ctx, map[string]any{
		"bruno_runner_url":            server.URL,
		"bruno_bundle_bytes":          bundle,
		"bruno_bundle_sha256":         digest,
		"bruno_compatibility_version": CompatibilityVersion,
		"bruno_sandbox":               "safe",
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if got := ownerClaims.Load(); got != 1 {
		t.Fatalf("owner claims = %d, want 1", got)
	}
	if err := instance.Teardown(context.Background()); err != nil {
		t.Fatalf("Teardown: %v", err)
	}
}

func TestProtocolRecoversLostCreateResponseWithSameClientSessionID(t *testing.T) {
	bundle := []byte("idempotent create bundle")
	sum := sha256.Sum256(bundle)
	digest := hex.EncodeToString(sum[:])
	var attempts atomic.Int64
	var committedID string
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/v1/bundles/open":
			response.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(response).Encode(openBundleResponse{BundleID: "idempotent-bundle"})
		case request.Method == http.MethodPost && request.URL.Path == "/v1/sessions":
			var payload createSessionRequest
			_ = json.NewDecoder(request.Body).Decode(&payload)
			mu.Lock()
			if committedID == "" {
				committedID = payload.SessionID
			}
			sameID := committedID == payload.SessionID
			mu.Unlock()
			if !sameID {
				t.Error("create retry used a different client session id")
			}
			if attempts.Add(1) == 1 {
				hijacker, ok := response.(http.Hijacker)
				if !ok {
					t.Fatal("test server does not support hijacking")
				}
				connection, _, hijackErr := hijacker.Hijack()
				if hijackErr != nil {
					t.Fatal(hijackErr)
				}
				_ = connection.Close()
				return
			}
			response.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(response).Encode(acknowledgedSession(payload))
		case request.Method == http.MethodDelete && strings.HasPrefix(request.URL.Path, "/v1/sessions/"):
			response.WriteHeader(http.StatusNoContent)
		case request.Method == http.MethodDelete && request.URL.Path == "/v1/bundles/idempotent-bundle":
			response.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	instance := &Protocol{}
	if err := instance.Init(context.Background(), map[string]any{
		"project_id":                  "project-idempotent",
		"bruno_runner_url":            server.URL,
		"bruno_bundle_bytes":          bundle,
		"bruno_bundle_sha256":         digest,
		"bruno_compatibility_version": CompatibilityVersion,
		"bruno_sandbox":               "safe",
		"bruno_secret_bindings":       map[string]string{"API_TOKEN": "idempotency-test-material"},
	}); err != nil {
		t.Fatalf("Init after lost response: %v", err)
	}
	if attempts.Load() != 2 {
		t.Fatalf("create attempts = %d, want one recovery retry", attempts.Load())
	}
	if err := instance.Teardown(context.Background()); err != nil {
		t.Fatalf("Teardown: %v", err)
	}
}

func TestProtocolDeletesClientSessionIDAfterAllCreateResponsesAreLost(t *testing.T) {
	bundle := []byte("lost create cleanup bundle")
	sum := sha256.Sum256(bundle)
	digest := hex.EncodeToString(sum[:])
	var mu sync.Mutex
	var requestedIDs []string
	var deletedID string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/v1/bundles/open":
			response.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(response).Encode(openBundleResponse{BundleID: "lost-response-bundle"})
		case request.Method == http.MethodPost && request.URL.Path == "/v1/sessions":
			var payload createSessionRequest
			_ = json.NewDecoder(request.Body).Decode(&payload)
			mu.Lock()
			requestedIDs = append(requestedIDs, payload.SessionID)
			mu.Unlock()
			hijacker, ok := response.(http.Hijacker)
			if !ok {
				t.Error("test server does not support hijacking")
				return
			}
			connection, _, err := hijacker.Hijack()
			if err != nil {
				t.Errorf("hijack create response: %v", err)
				return
			}
			_ = connection.Close()
		case request.Method == http.MethodDelete && strings.HasPrefix(request.URL.Path, "/v1/sessions/"):
			mu.Lock()
			deletedID = strings.TrimPrefix(request.URL.Path, "/v1/sessions/")
			mu.Unlock()
			response.WriteHeader(http.StatusNoContent)
		case request.Method == http.MethodDelete && request.URL.Path == "/v1/bundles/lost-response-bundle":
			response.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	instance := &Protocol{}
	err := instance.Init(context.Background(), map[string]any{
		"project_id":                  "project-lost-response",
		"bruno_runner_url":            server.URL,
		"bruno_bundle_bytes":          bundle,
		"bruno_bundle_sha256":         digest,
		"bruno_compatibility_version": CompatibilityVersion,
		"bruno_sandbox":               "safe",
		"bruno_secret_bindings":       map[string]string{"API_TOKEN": "lost-response-material"},
	})
	if err == nil {
		t.Fatal("Init unexpectedly succeeded after both create responses were lost")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requestedIDs) != 2 || requestedIDs[0] == "" || requestedIDs[0] != requestedIDs[1] {
		t.Fatalf("create request IDs = %#v, want two identical client IDs", requestedIDs)
	}
	if deletedID != requestedIDs[0] {
		t.Fatalf("cleanup deleted session %q, want %q", deletedID, requestedIDs[0])
	}
}

func TestAmbiguousCreateCleanupRetainsContainmentBarrier(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/v1/sessions":
			var payload createSessionRequest
			_ = json.NewDecoder(request.Body).Decode(&payload)
			response.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(response).Encode(createSessionResponse{
				SessionID: payload.SessionID, SessionLeaseToken: "mismatched-lease-token",
				LeaseTTLMS: runnerSessionLeaseTTL.Milliseconds(),
			})
		case request.Method == http.MethodDelete && strings.HasPrefix(request.URL.Path, "/v1/sessions/"):
			http.Error(response, "cleanup remains unproven", http.StatusServiceUnavailable)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	instance := &Protocol{
		client: server.Client(), runnerURL: server.URL, bundleID: "bundle-create-uncertain",
		sandbox: "developer", compatibility: CompatibilityVersion,
	}
	if _, _, err := instance.createSession(context.Background(), nil); !errors.Is(err, protocol.ErrContainmentUnproven) {
		t.Fatalf("createSession error = %v, want ErrContainmentUnproven", err)
	}
	instance.mu.Lock()
	instance.bundleID = ""
	instance.mu.Unlock()
	if err := instance.Teardown(context.Background()); !errors.Is(err, protocol.ErrContainmentUnproven) {
		t.Fatalf("Teardown error = %v, want retained ErrContainmentUnproven", err)
	}
}

func TestProtocolReuploadsCachedBundleAfterRunnerExpiry(t *testing.T) {
	bundle := []byte("runner-expiring shared bundle")
	sum := sha256.Sum256(bundle)
	digest := hex.EncodeToString(sum[:])

	var mu sync.Mutex
	opens, sessionAttempts := 0, 0
	expiredFirstBundle := false
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/v1/bundles/open":
			opens++
			response.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(response).Encode(openBundleResponse{BundleID: fmt.Sprintf("bundle-%d", opens)})
		case request.Method == http.MethodPost && request.URL.Path == "/v1/sessions":
			sessionAttempts++
			var payload createSessionRequest
			_ = json.NewDecoder(request.Body).Decode(&payload)
			if expiredFirstBundle && payload.BundleID == "bundle-1" {
				http.Error(response, "bundle not found or expired", http.StatusNotFound)
				return
			}
			response.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(response).Encode(acknowledgedSession(payload))
		case request.Method == http.MethodDelete:
			response.WriteHeader(http.StatusNoContent)
		default:
			http.Error(response, "unexpected", http.StatusNotFound)
		}
	}))
	defer server.Close()

	config := map[string]any{
		"project_id":                  "project-a",
		"bruno_runner_url":            server.URL,
		"bruno_bundle_bytes":          bundle,
		"bruno_bundle_sha256":         digest,
		"bruno_compatibility_version": CompatibilityVersion,
		"bruno_sandbox":               "safe",
	}
	first := &Protocol{}
	if err := first.Init(context.Background(), config); err != nil {
		t.Fatalf("first Init: %v", err)
	}
	mu.Lock()
	expiredFirstBundle = true
	mu.Unlock()
	second := &Protocol{}
	if err := second.Init(context.Background(), config); err != nil {
		t.Fatalf("second Init after bundle expiry: %v", err)
	}
	if first.bundleID != "bundle-1" || second.bundleID != "bundle-2" {
		t.Fatalf("bundle ids = %q, %q", first.bundleID, second.bundleID)
	}
	mu.Lock()
	gotOpens, gotAttempts := opens, sessionAttempts
	mu.Unlock()
	if gotOpens != 2 || gotAttempts != 3 {
		t.Fatalf("opens=%d session_attempts=%d, want one reupload and one retry", gotOpens, gotAttempts)
	}
	if err := first.Teardown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := second.Teardown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestDeveloperModeFailsClosedWhenDisabled(t *testing.T) {
	t.Setenv("APEXLOAD_BRUNO_DEVELOPER_EXECUTION_ENABLED", "false")
	instance := &Protocol{}
	err := instance.Init(context.Background(), map[string]any{
		"bruno_sandbox":               "developer",
		"bruno_compatibility_version": CompatibilityVersion,
	})
	if err == nil || !strings.Contains(err.Error(), "operator-enabled isolated runner") {
		t.Fatalf("Init error = %v", err)
	}
}

func TestProtocolRejectsUnresolvedSecretBindingsBeforeArtifactAccess(t *testing.T) {
	instance := &Protocol{}
	err := instance.Init(context.Background(), map[string]any{
		"bruno_compatibility_version": CompatibilityVersion,
		"bruno_sandbox":               "safe",
		"bruno_runner_url":            "http://127.0.0.1:8097",
		"bruno_secret_bindings": map[string]string{
			"PASSWORD": "{{secret.PROJECT_PASSWORD}}",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "unresolved Bruno secret binding") || !strings.Contains(err.Error(), "PASSWORD") {
		t.Fatalf("Init error = %v", err)
	}
}

func TestProtocolRejectsUnauthenticatedRemoteRunner(t *testing.T) {
	for _, runnerURL := range []string{"http://runner.internal:8097", "https://runner.internal:8097"} {
		t.Run(runnerURL, func(t *testing.T) {
			instance := &Protocol{}
			err := instance.Init(context.Background(), map[string]any{
				"bruno_compatibility_version": CompatibilityVersion,
				"bruno_sandbox":               "safe",
				"bruno_runner_url":            runnerURL,
			})
			if err == nil || !strings.Contains(err.Error(), "must use a loopback host") {
				t.Fatalf("Init error = %v", err)
			}
		})
	}
}

func TestLoopbackRunnerHostRecognition(t *testing.T) {
	for _, host := range []string{"localhost", "127.0.0.1", "::1"} {
		if !isLoopbackHost(host) {
			t.Errorf("isLoopbackHost(%q) = false", host)
		}
	}
	for _, host := range []string{"runner.internal", "10.0.0.2", ""} {
		if isLoopbackHost(host) {
			t.Errorf("isLoopbackHost(%q) = true", host)
		}
	}
}

func TestGovernedEgressConfiguredRequiresBrokerOrExplicitTestOverride(t *testing.T) {
	t.Setenv("APEXLOAD_BRUNO_EGRESS_BROKER_URL", "")
	t.Setenv("APEXLOAD_BRUNO_ALLOW_DIRECT_EGRESS", "false")
	if GovernedEgressConfigured() {
		t.Fatal("governed egress reported available without a broker")
	}
	t.Setenv("APEXLOAD_BRUNO_EGRESS_BROKER_URL", "http://broker.internal:8080")
	if !GovernedEgressConfigured() {
		t.Fatal("valid egress broker was not recognized")
	}
	t.Setenv("APEXLOAD_BRUNO_EGRESS_BROKER_URL", "http://user:password@broker.internal:8080")
	if GovernedEgressConfigured() {
		t.Fatal("broker URL with embedded credentials was accepted")
	}
	t.Setenv("APEXLOAD_BRUNO_EGRESS_BROKER_URL", "")
	t.Setenv("APEXLOAD_BRUNO_ALLOW_DIRECT_EGRESS", "true")
	if !GovernedEgressConfigured() {
		t.Fatal("explicit direct-egress conformance override was not recognized")
	}
}

func TestBoundedReaderAcceptsExactLimitAndRejectsAdditionalBytes(t *testing.T) {
	exact, err := io.ReadAll(&boundedReader{reader: bytes.NewBufferString("abcd"), remaining: 4})
	if err != nil || string(exact) != "abcd" {
		t.Fatalf("exact-limit read = %q, %v", exact, err)
	}
	if _, err := io.ReadAll(&boundedReader{reader: bytes.NewBufferString("abcde"), remaining: 4}); err == nil ||
		!strings.Contains(err.Error(), "exceeds configured limit") {
		t.Fatalf("over-limit read error = %v", err)
	}
}

func TestRunnerHTTPErrorNeverIncludesResponseBody(t *testing.T) {
	const marker = "runner-echoed-secret-marker"
	err := runnerHTTPError(&http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Status:     "503 attacker-controlled-status",
		Body:       io.NopCloser(strings.NewReader(`{"error":"` + marker + `"}`)),
	}, 64<<10)
	if strings.Contains(err.Error(), marker) || strings.Contains(err.Error(), "attacker-controlled-status") {
		t.Fatalf("runner error disclosed response material: %v", err)
	}
	if !strings.Contains(err.Error(), "503 Service Unavailable") {
		t.Fatalf("runner error lost canonical status: %v", err)
	}
	var statusErr *runnerHTTPStatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("runner status classification = %#v", err)
	}

	notFound := runnerHTTPError(&http.Response{
		StatusCode: http.StatusNotFound,
		Body:       io.NopCloser(strings.NewReader(marker)),
	}, 64<<10)
	if !runnerResourceUnavailable(notFound) || strings.Contains(notFound.Error(), marker) {
		t.Fatalf("resource-unavailable classification/disclosure = %v", notFound)
	}
}

func TestProtocolStreamErrorNeverIncludesRunnerMessage(t *testing.T) {
	const marker = "runner-stream-secret-marker"
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/v1/sessions/session-1/execute" {
			http.NotFound(response, request)
			return
		}
		response.Header().Set("Content-Type", "application/x-ndjson")
		_ = json.NewEncoder(response).Encode(runnerEvent{Type: "error", Error: marker})
	}))
	defer server.Close()
	instance := &Protocol{
		client:        server.Client(),
		runnerURL:     server.URL,
		sessionID:     "session-1",
		outputLimit:   64 << 10,
		streamTimeout: time.Second,
	}
	_, err := instance.ExecuteStream(context.Background(), &protocol.Action{
		Name: "Run collection",
		Type: "bruno_run",
	}, func(*protocol.Result) error { return nil })
	if err == nil || strings.Contains(err.Error(), marker) || err.Error() != "Bruno runner execution failed" {
		t.Fatalf("stream error disclosure = %v", err)
	}
}

func TestProtocolUnknownStreamEventNeverIncludesRunnerType(t *testing.T) {
	const marker = "unknown-event-secret-marker"
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/x-ndjson")
		_ = json.NewEncoder(response).Encode(runnerEvent{Type: marker})
	}))
	defer server.Close()
	instance := &Protocol{
		client:        server.Client(),
		runnerURL:     server.URL,
		sessionID:     "session-1",
		outputLimit:   64 << 10,
		streamTimeout: time.Second,
	}
	_, err := instance.ExecuteStream(context.Background(), &protocol.Action{Name: "Run", Type: "bruno_run"}, nil)
	if err == nil || strings.Contains(err.Error(), marker) {
		t.Fatalf("unknown stream event disclosure = %v", err)
	}
}

func TestProtocolTeardownRetainsIdentityAndBundleOwnershipForRetry(t *testing.T) {
	t.Setenv("APEXLOAD_BRUNO_CLEANUP_TIMEOUT", "25ms")
	bundle := []byte("retry-safe teardown bundle")
	sum := sha256.Sum256(bundle)
	digest := hex.EncodeToString(sum[:])
	var sessionDeletes atomic.Int64
	var bundleDeletes atomic.Int64
	var hangSessionDelete atomic.Bool
	var runnerSessionID atomic.Value
	hangSessionDelete.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/v1/bundles/open":
			response.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(response).Encode(openBundleResponse{BundleID: "retry-bundle"})
		case request.Method == http.MethodPost && request.URL.Path == "/v1/sessions":
			var payload createSessionRequest
			_ = json.NewDecoder(request.Body).Decode(&payload)
			runnerSessionID.Store(payload.SessionID)
			response.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(response).Encode(acknowledgedSession(payload))
		case request.Method == http.MethodDelete && request.URL.Path == "/v1/sessions/"+runnerSessionID.Load().(string):
			sessionDeletes.Add(1)
			if hangSessionDelete.Load() {
				<-request.Context().Done()
				return
			}
			response.WriteHeader(http.StatusNoContent)
		case request.Method == http.MethodDelete && request.URL.Path == "/v1/bundles/retry-bundle":
			bundleDeletes.Add(1)
			response.WriteHeader(http.StatusNoContent)
		default:
			http.Error(response, "unexpected", http.StatusNotFound)
		}
	}))
	defer server.Close()

	instance := &Protocol{}
	if err := instance.Init(context.Background(), map[string]any{
		"project_id":                  "project-retry",
		"bruno_runner_url":            server.URL,
		"bruno_bundle_bytes":          bundle,
		"bruno_bundle_sha256":         digest,
		"bruno_compatibility_version": CompatibilityVersion,
		"bruno_sandbox":               "safe",
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	start := time.Now()
	firstCleanupCtx, firstCleanupCancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	firstCleanupErr := instance.Teardown(firstCleanupCtx)
	if firstCleanupErr == nil {
		t.Fatal("first Teardown unexpectedly succeeded")
	}
	if !errors.Is(firstCleanupErr, protocol.ErrContainmentUnproven) {
		t.Fatalf("ambiguous cleanup error = %v, want ErrContainmentUnproven", firstCleanupErr)
	}
	firstCleanupCancel()
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("first Teardown took %s despite bounded cleanup", elapsed)
	}
	instance.mu.Lock()
	retainedSession, retainedBundle := instance.sessionID, instance.bundleID
	instance.mu.Unlock()
	if retainedSession != runnerSessionID.Load().(string) || retainedBundle != "retry-bundle" || bundleDeletes.Load() != 0 {
		t.Fatalf("failed cleanup lost identity/ownership: session=%q bundle=%q bundle_deletes=%d", retainedSession, retainedBundle, bundleDeletes.Load())
	}

	hangSessionDelete.Store(false)
	if err := instance.Teardown(context.Background()); err != nil {
		t.Fatalf("retry Teardown: %v", err)
	}
	if err := instance.Teardown(context.Background()); err != nil {
		t.Fatalf("idempotent Teardown: %v", err)
	}
	if sessionDeletes.Load() != 2 || bundleDeletes.Load() != 1 {
		t.Fatalf("cleanup calls session=%d bundle=%d, want retry + exactly-once bundle delete", sessionDeletes.Load(), bundleDeletes.Load())
	}
}

func TestConcurrentProtocolTeardownCoalescesCleanup(t *testing.T) {
	bundle := []byte("concurrent teardown bundle")
	sum := sha256.Sum256(bundle)
	digest := hex.EncodeToString(sum[:])
	deleteEntered := make(chan struct{})
	releaseDelete := make(chan struct{})
	var sessionDeletes atomic.Int64
	var bundleDeletes atomic.Int64
	var runnerSessionID atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/v1/bundles/open":
			response.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(response).Encode(openBundleResponse{BundleID: "concurrent-bundle"})
		case request.Method == http.MethodPost && request.URL.Path == "/v1/sessions":
			var payload createSessionRequest
			_ = json.NewDecoder(request.Body).Decode(&payload)
			runnerSessionID.Store(payload.SessionID)
			response.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(response).Encode(acknowledgedSession(payload))
		case request.Method == http.MethodDelete && request.URL.Path == "/v1/sessions/"+runnerSessionID.Load().(string):
			if sessionDeletes.Add(1) == 1 {
				close(deleteEntered)
			}
			<-releaseDelete
			response.WriteHeader(http.StatusNoContent)
		case request.Method == http.MethodDelete && request.URL.Path == "/v1/bundles/concurrent-bundle":
			bundleDeletes.Add(1)
			response.WriteHeader(http.StatusNoContent)
		default:
			http.Error(response, "unexpected", http.StatusNotFound)
		}
	}))
	defer server.Close()
	instance := &Protocol{}
	if err := instance.Init(context.Background(), map[string]any{
		"project_id":                  "project-concurrent",
		"bruno_runner_url":            server.URL,
		"bruno_bundle_bytes":          bundle,
		"bruno_bundle_sha256":         digest,
		"bruno_compatibility_version": CompatibilityVersion,
		"bruno_sandbox":               "safe",
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	errs := make(chan error, 2)
	go func() { errs <- instance.Teardown(context.Background()) }()
	select {
	case <-deleteEntered:
	case <-time.After(time.Second):
		t.Fatal("first teardown did not reach runner")
	}
	go func() { errs <- instance.Teardown(context.Background()) }()
	close(releaseDelete)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent Teardown: %v", err)
		}
	}
	if sessionDeletes.Load() != 1 || bundleDeletes.Load() != 1 {
		t.Fatalf("concurrent cleanup calls session=%d bundle=%d", sessionDeletes.Load(), bundleDeletes.Load())
	}
}

type panicRoundTripper func(*http.Request) (*http.Response, error)

func (fn panicRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestProtocolTeardownPanicDoesNotPermanentlyFenceRetry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodDelete || request.URL.Path != "/v1/sessions/retry-session" {
			http.NotFound(response, request)
			return
		}
		response.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	instance := &Protocol{
		client: &http.Client{Transport: panicRoundTripper(func(*http.Request) (*http.Response, error) {
			panic("injected transport panic")
		})},
		runnerURL: server.URL,
		sessionID: "retry-session",
	}
	panicked := false
	func() {
		defer func() { panicked = recover() != nil }()
		_ = instance.Teardown(context.Background())
	}()
	if !panicked {
		t.Fatal("injected transport panic did not propagate")
	}
	instance.mu.Lock()
	inFlight, retainedSession := instance.teardownInFlight, instance.sessionID
	instance.client = server.Client()
	instance.mu.Unlock()
	if inFlight != nil || retainedSession != "retry-session" {
		t.Fatalf("panic left teardown fenced or lost identity: in_flight=%v session=%q", inFlight != nil, retainedSession)
	}
	if err := instance.Teardown(context.Background()); err != nil {
		t.Fatalf("Teardown retry after panic: %v", err)
	}
}

func TestInitFailureCleanupUsesFreshBoundedContextAndRemainsRetryable(t *testing.T) {
	t.Setenv("APEXLOAD_BRUNO_CLEANUP_TIMEOUT", "25ms")
	bundle := []byte("init cleanup retry bundle")
	sum := sha256.Sum256(bundle)
	digest := hex.EncodeToString(sum[:])
	var allowDelete atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/v1/bundles/open":
			response.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(response).Encode(openBundleResponse{BundleID: "init-failure-bundle"})
		case request.Method == http.MethodPost && request.URL.Path == "/v1/sessions":
			http.Error(response, "credential-marker-must-not-escape", http.StatusServiceUnavailable)
		case request.Method == http.MethodDelete && request.URL.Path == "/v1/bundles/init-failure-bundle":
			if !allowDelete.Load() {
				<-request.Context().Done()
				return
			}
			response.WriteHeader(http.StatusNoContent)
		default:
			http.Error(response, "unexpected", http.StatusNotFound)
		}
	}))
	defer server.Close()
	instance := &Protocol{}
	start := time.Now()
	err := instance.Init(context.Background(), map[string]any{
		"project_id":                  "project-init-cleanup",
		"bruno_runner_url":            server.URL,
		"bruno_bundle_bytes":          bundle,
		"bruno_bundle_sha256":         digest,
		"bruno_compatibility_version": CompatibilityVersion,
		"bruno_sandbox":               "safe",
	})
	if err == nil || strings.Contains(err.Error(), "credential-marker-must-not-escape") {
		t.Fatalf("Init error = %v", err)
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("Init failure cleanup took %s", elapsed)
	}
	allowDelete.Store(true)
	if err := instance.Teardown(context.Background()); err != nil {
		t.Fatalf("retry cleanup after failed Init: %v", err)
	}
}
