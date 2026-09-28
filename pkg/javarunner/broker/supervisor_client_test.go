package broker

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func supervisorClientFixture(t *testing.T, cfg SupervisorConfig, handler http.Handler) (*SupervisorClient, *httptest.Server) {
	t.Helper()
	ca, serverCert, brokerCert, _, roots := controlCertificates(t, cfg.HostID)
	server := httptest.NewUnstartedServer(handler)
	var e error
	server.TLS, e = SupervisorTLS(serverCert, ca, cfg.BrokerClientURI)
	if e != nil {
		t.Fatal(e)
	}
	server.StartTLS()
	t.Cleanup(server.Close)
	client, e := NewSupervisorClient(server.URL, "spiffe://apexload/java-host/"+cfg.HostID, cfg, &tls.Config{RootCAs: roots, Certificates: []tls.Certificate{brokerCert}})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(client.Close)
	return client, server
}
func TestSupervisorClientControlLaneIndependentOfInvocation(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	cfg := controlConfig()
	cfg.LeaseTTL = 5 * time.Second
	c := newControl(t, cfg, &controlRuntime{invoke: func(ctx context.Context, _ string, _ string, _ map[string]string) ([]byte, error) {
		close(entered)
		select {
		case <-release:
			return []byte("done"), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}})
	waitReady(t, c)
	client, _ := supervisorClientFixture(t, cfg, c)
	if _, e := client.Control(t.Context(), "commit"); e != nil {
		t.Fatal(e)
	}
	result := make(chan error, 1)
	go func() { _, e := client.Invoke(t.Context(), "init", nil); result <- e }()
	<-entered
	if _, e := client.Invoke(t.Context(), "action", nil); !errors.Is(e, ErrState) {
		t.Fatal("busy invocation queued", e)
	}
	if _, e := client.Control(t.Context(), "renew"); e != nil {
		t.Fatal("renew blocked", e)
	}
	if _, e := client.Control(t.Context(), "close"); e != nil {
		t.Fatal(e)
	}
	select {
	case e := <-result:
		if e == nil {
			t.Fatal("retired invocation succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("close blocked on runtime")
	}
	if _, e := client.Invoke(t.Context(), "init", nil); !errors.Is(e, ErrState) {
		t.Fatal("closed host revived", e)
	}
}
func TestSupervisorClientRejectsForgedResponseWithoutReplay(t *testing.T) {
	for _, kind := range []string{"wrong_host", "wrong_request", "unknown_code", "redirect", "trailing", "empty_result"} {
		t.Run(kind, func(t *testing.T) {
			var calls atomic.Int32
			cfg := controlConfig()
			client, _ := supervisorClientFixture(t, cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var q SupervisorRequest
				_ = json.NewDecoder(r.Body).Decode(&q)
				out := SupervisorResponse{Contract: q.Contract, HostID: q.HostID, BrokerEpoch: q.BrokerEpoch, ConfigurationSHA256: q.ConfigurationSHA256, RequestID: q.RequestID, State: "committed", Payload: []byte("result")}
				out.LeaseTTLMS = cfg.LeaseTTL.Milliseconds()
				out.PreparationTimeoutMS = cfg.PreparationTimeout.Milliseconds()
				out.InvocationTimeoutMS = cfg.InvocationTimeout.Milliseconds()
				out.InvocationID = SupervisorInvocationID(q.Sequence)
				switch kind {
				case "wrong_host":
					out.HostID = "other"
				case "wrong_request":
					out.RequestID = "other"
				case "unknown_code":
					out.Failure = "private backend error"
					w.WriteHeader(http.StatusConflict)
				case "redirect":
					w.Header().Set("Location", "https://example.invalid/stolen")
					w.WriteHeader(http.StatusTemporaryRedirect)
					return
				case "empty_result":
					out.Payload = nil
				}
				_ = json.NewEncoder(w).Encode(out)
				if kind == "trailing" {
					_, _ = w.Write([]byte("{}"))
				}
			}))
			if _, e := client.Invoke(t.Context(), "init", nil); !errors.Is(e, ErrSupervisorTransport) {
				t.Fatal(e)
			}
			if _, e := client.Invoke(t.Context(), "init", nil); !errors.Is(e, ErrState) {
				t.Fatal("uncertain invocation replayed", e)
			}
			if calls.Load() != 1 {
				t.Fatal("request replayed", calls.Load())
			}
		})
	}
}

func TestSupervisorClientRejectsRuntimePolicyDriftBeforeCommit(t *testing.T) {
	for _, kind := range []string{"lease", "preparation", "invocation"} {
		t.Run(kind, func(t *testing.T) {
			expected := controlConfig()
			actual := expected
			switch kind {
			case "lease":
				actual.LeaseTTL += time.Second
			case "preparation":
				actual.PreparationTimeout += time.Second
			case "invocation":
				actual.InvocationTimeout += time.Second
			}
			control := newControl(t, actual, &controlRuntime{})
			waitReady(t, control)
			client, _ := supervisorClientFixture(t, expected, control)
			if _, e := client.Control(t.Context(), "ready"); !errors.Is(e, ErrSupervisorTransport) {
				t.Fatal("runtime policy drift accepted", e)
			}
			if _, e := client.Control(t.Context(), "commit"); !errors.Is(e, ErrSupervisorTransport) {
				t.Fatal("drifted runtime committed", e)
			}
		})
	}
}
func TestSupervisorClientRequiresHostCertificateIdentity(t *testing.T) {
	cfg := controlConfig()
	client, server := supervisorClientFixture(t, cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("wrong-identity TLS reached handler") }))
	transport := client.http.Transport.(*http.Transport)
	cfg.HostID = "wrong"
	bad, e := NewSupervisorClient(server.URL, "spiffe://apexload/java-host/wrong", cfg, transport.TLSClientConfig)
	if e != nil {
		t.Fatal(e)
	}
	defer bad.Close()
	if _, e := bad.Control(t.Context(), "ready"); !errors.Is(e, ErrSupervisorTransport) {
		t.Fatal(e)
	}
}

func TestSupervisorClientRequiresBrokerCredentialAndBoundedConfig(t *testing.T) {
	_, _, _, hostCert, roots := controlCertificates(t)
	cfg := controlConfig()
	config := &tls.Config{RootCAs: roots, Certificates: []tls.Certificate{hostCert}}
	if _, e := NewSupervisorClient("https://127.0.0.1:9443", "spiffe://apexload/java-host/host", cfg, config); !errors.Is(e, ErrState) {
		t.Fatal("host credential accepted for broker role", e)
	}
	_, _, brokerCert, _, _ := controlCertificates(t)
	config.Certificates = []tls.Certificate{brokerCert}
	untrusted := config.Clone()
	untrusted.RootCAs = nil
	if _, e := NewSupervisorClient("https://127.0.0.1:9443", "spiffe://apexload/java-host/host", cfg, untrusted); !errors.Is(e, ErrState) {
		t.Fatal("implicit system trust accepted", e)
	}

	for _, mutate := range []func(*SupervisorConfig){func(c *SupervisorConfig) { c.LeaseTTL = 0 }, func(c *SupervisorConfig) { c.PreparationTimeout = time.Hour + time.Second }, func(c *SupervisorConfig) { c.InvocationTimeout = 0 }, func(c *SupervisorConfig) { c.BrokerClientURI = "invalid" }, func(c *SupervisorConfig) { c.ConfigurationSHA256 = "invalid" }} {
		invalid := cfg
		mutate(&invalid)
		if _, e := NewSupervisorClient("https://127.0.0.1:9443", "spiffe://apexload/java-host/host", invalid, config); !errors.Is(e, ErrState) {
			t.Fatal("invalid client configuration accepted", e)
		}
	}
}

func TestSupervisorAwaitTransportNeverIssuesControlOrUserCode(t *testing.T) {
	var calls atomic.Int32
	cfg := controlConfig()
	client, _ := supervisorClientFixture(t, cfg, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	if e := client.AwaitTransport(t.Context()); e != nil {
		t.Fatal(e)
	}
	if calls.Load() != 0 {
		t.Fatal("transport probe issued HTTP request")
	}
	client.Close()
	if e := client.AwaitTransport(t.Context()); !errors.Is(e, ErrSupervisorTransport) {
		t.Fatal("closed transport revived", e)
	}
}
func TestSupervisorAwaitTransportDeadlineAndWrongIdentity(t *testing.T) {
	cfg := controlConfig()
	client, server := supervisorClientFixture(t, cfg, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("untrusted identity reached HTTP") }))
	transport := client.http.Transport.(*http.Transport)
	cfg.HostID = "wrong"
	bad, e := NewSupervisorClient(server.URL, "spiffe://apexload/java-host/wrong", cfg, transport.TLSClientConfig)
	if e != nil {
		t.Fatal(e)
	}
	defer bad.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 80*time.Millisecond)
	defer cancel()
	if e = bad.AwaitTransport(ctx); !errors.Is(e, ErrSupervisorTransport) {
		t.Fatal(e)
	}
	if !bad.terminal.Load() {
		t.Fatal("failed transport remained admissible")
	}
}
