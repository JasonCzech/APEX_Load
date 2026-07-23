package javasidecar

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestNewClientRequiresLoopbackHTTPOrigin(t *testing.T) {
	for _, raw := range []string{
		"https://127.0.0.1:8098",
		"http://example.test:8098",
		"http://0.0.0.0:8098",
		"http://127.0.0.1:8098/base",
		"http://user:pass@127.0.0.1:8098",
	} {
		if client, err := NewClient(raw, nil); err == nil || client != nil {
			t.Fatalf("NewClient(%q) succeeded", raw)
		}
	}
	for _, raw := range []string{"http://127.0.0.1:8098", "http://[::1]:8098", "http://localhost:8098"} {
		if _, err := NewClient(raw, nil); err != nil {
			t.Fatalf("NewClient(%q): %v", raw, err)
		}
	}
}

func TestClientRefusesRedirects(t *testing.T) {
	var redirected atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/health", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/redirected", http.StatusFound)
	})
	mux.HandleFunc("/redirected", func(w http.ResponseWriter, _ *http.Request) {
		redirected.Store(true)
		_ = json.NewEncoder(w).Encode(HealthResponse{Contract: ContractVersion, Status: "healthy"})
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Health(context.Background())
	if err == nil || !ExecutionOutcomeAmbiguous(err) {
		t.Fatalf("Health() error = %#v", err)
	}
	if redirected.Load() {
		t.Fatal("sidecar client followed a redirect")
	}
}

func TestExecutionOutcomeAmbiguityClassification(t *testing.T) {
	t.Run("local encoding rejection is safe to retry", func(t *testing.T) {
		client, err := NewClient("http://127.0.0.1:8098", &http.Client{
			Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				t.Fatal("locally invalid request reached the transport")
				return nil, nil
			}),
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = client.Execute(context.Background(), ExecutionRequest{
			ExecutionID: "request-1",
			Command: ActionCommand{
				ClassName: "Actions",
				Params:    map[string]any{"unsupported": func() {}},
			},
		})
		if err == nil || ExecutionOutcomeAmbiguous(err) {
			t.Fatalf("Execute() error = %#v, want authoritative local rejection", err)
		}
	})

	t.Run("valid remote rejection is authoritative", func(t *testing.T) {
		transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
			body, _ := json.Marshal(ErrorResponse{Contract: ContractVersion, ErrorCode: "session_unusable"})
			return &http.Response{
				StatusCode: http.StatusConflict,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(string(body))),
			}, nil
		})
		client, err := NewClient("http://127.0.0.1:8098", &http.Client{Transport: transport})
		if err != nil {
			t.Fatal(err)
		}
		_, err = client.Execute(context.Background(), ExecutionRequest{ExecutionID: "request-1"})
		var remote *RemoteError
		if !errors.As(err, &remote) || ExecutionOutcomeAmbiguous(err) {
			t.Fatalf("Execute() error = %#v, want authoritative RemoteError", err)
		}
	})

	t.Run("transport failure is ambiguous", func(t *testing.T) {
		transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("response lost")
		})
		client, err := NewClient("http://127.0.0.1:8098", &http.Client{Transport: transport})
		if err != nil {
			t.Fatal(err)
		}
		_, err = client.Execute(context.Background(), ExecutionRequest{ExecutionID: "request-1"})
		if err == nil || !ExecutionOutcomeAmbiguous(err) {
			t.Fatalf("Execute() error = %#v, want ambiguous transport failure", err)
		}
	})

	if !ExecutionOutcomeAmbiguous(errors.New("alternate client error")) {
		t.Fatal("unknown client errors must fail closed as ambiguous")
	}
}

func TestClientFailsClosedOnContractMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(HealthResponse{Contract: "future-contract", Status: "healthy"})
	}))
	defer server.Close()
	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Health(context.Background()); err == nil || !strings.Contains(err.Error(), "contract mismatch") {
		t.Fatalf("Health() error = %v", err)
	}
}

func TestClientPropagatesAuthAndOwnerLease(t *testing.T) {
	var executeLease, cancelLease, closeLease bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Apexload-Java-Runner-Token") != "runner-token" {
			t.Errorf("request %s omitted runner authentication", r.URL.Path)
		}
		switch {
		case r.URL.Path == "/v1/owner/claim":
			var request ClaimOwnerRequest
			_ = json.NewDecoder(r.Body).Decode(&request)
			_ = json.NewEncoder(w).Encode(ClaimOwnerResponse{
				Contract: ContractVersion, OwnerID: request.OwnerID,
				Epoch: "owner-epoch-1", Claimed: true,
			})
		case r.URL.Path == "/v1/executions":
			var request ExecutionRequest
			_ = json.NewDecoder(r.Body).Decode(&request)
			executeLease = request.OwnerID == "generator-a" && request.OwnerEpoch == "owner-epoch-1"
			_ = json.NewEncoder(w).Encode(ExecutionResponse{
				Contract: ContractVersion, ExecutionID: request.ExecutionID, Success: true,
			})
		case strings.HasSuffix(r.URL.Path, "/cancel"):
			var request CancelRequest
			_ = json.NewDecoder(r.Body).Decode(&request)
			cancelLease = request.OwnerID == "generator-a" && request.OwnerEpoch == "owner-epoch-1"
			_ = json.NewEncoder(w).Encode(CancelResponse{
				Contract: ContractVersion, ExecutionID: "exec-1", Canceled: true,
			})
		case strings.HasSuffix(r.URL.Path, "/close"):
			var request CloseSessionRequest
			_ = json.NewDecoder(r.Body).Decode(&request)
			closeLease = request.OwnerID == "generator-a" && request.OwnerEpoch == "owner-epoch-1"
			_ = json.NewEncoder(w).Encode(CloseSessionResponse{
				Contract: ContractVersion, SessionID: "session-1", Closed: true,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewClientWithToken(server.URL, "runner-token", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ClaimOwner(context.Background(), "generator-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Execute(context.Background(), ExecutionRequest{ExecutionID: "exec-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Cancel(context.Background(), "exec-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CloseSession(context.Background(), "session-1"); err != nil {
		t.Fatal(err)
	}
	if !executeLease || !cancelLease || !closeLease {
		t.Fatalf("owner lease propagation = execute:%v cancel:%v close:%v", executeLease, cancelLease, closeLease)
	}
}

func TestWireRequestCannotSteerOperatorConfiguration(t *testing.T) {
	encoded, err := json.Marshal(ExecutionRequest{
		Contract: ContractVersion, ExecutionID: "request-1",
		Runtime: RuntimeSettings{JDKSelection: "internal"},
		Command: ActionCommand{ClassName: "Actions"},
	})
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, forbidden := range []string{"runner_command", "harness_jar", "work_dir", "shared_classpath", "max_processes"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("wire request exposed operator field %q: %s", forbidden, text)
		}
	}
}

func TestClientBoundsResponseBodies(t *testing.T) {
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(strings.Repeat("x", int(MaxResponseBytes)+1))),
		}, nil
	})
	client, err := NewClient("http://127.0.0.1:8098", &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Health(context.Background()); err == nil || !strings.Contains(err.Error(), "exceeds the size limit") {
		t.Fatalf("Health() error = %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
