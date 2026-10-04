package appdynamics

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAppDynamicsIndependentCapabilities(t *testing.T) {
	var diagnostics, analytics atomic.Int32
	c := fixtureClient(t, AuthBasic, func(r *http.Request) (*http.Response, error) {
		switch {
		case r.URL.Host == "analytics.example":
			analytics.Add(1)
			return jsonResponse(200, `[]`), nil
		case strings.HasSuffix(r.URL.Path, "/applications"):
			return jsonResponse(200, `[{"id":1,"name":"Services"}]`), nil
		case strings.Contains(r.URL.Path, "metric"):
			return jsonResponse(403, `{}`), nil
		default:
			diagnostics.Add(1)
			return jsonResponse(200, `[]`), nil
		}
	})
	caps, err := c.Test(context.Background())
	if err != nil || caps.Metrics || !caps.Events || !caps.Violations || !caps.Snapshots || !caps.Analytics || caps.Status["metrics"] != "forbidden" || caps.Status["analytics"] != "available_no_data" || diagnostics.Load() != 3 || analytics.Load() != 1 {
		t.Fatal(caps, err, diagnostics.Load(), analytics.Load())
	}
}

func TestAppDynamicsSelectionProbesOnlySelectedCapabilities(t *testing.T) {
	var events atomic.Int32
	c := fixtureClient(t, AuthBasic, func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/applications/1"):
			return jsonResponse(200, `[{"id":1,"name":"Services"}]`), nil
		case strings.HasSuffix(r.URL.Path, "/events"):
			events.Add(1)
			return jsonResponse(200, `[]`), nil
		default:
			t.Fatal("unselected capability was queried", r.URL.Path)
			return jsonResponse(403, `{}`), nil
		}
	})
	caps, err := c.TestSelection(context.Background(), Selection{ProjectID: "p", Applications: []string{"1"}, Events: true})
	if err != nil || !caps.Events || caps.Metrics || caps.Snapshots || caps.Analytics || caps.Status["metrics"] != "not_selected" || events.Load() != 1 {
		t.Fatal(caps, err, events.Load())
	}
}

func TestAppDynamicsApprovedApplicationFullCapabilities(t *testing.T) {
	for _, discoveryDenied := range []bool{false, true} {
		t.Run(map[bool]string{false: "independent capabilities", true: "scope unavailable"}[discoveryDenied], func(t *testing.T) {
			var diagnostics, analytics atomic.Int32
			c := fixtureClient(t, AuthBasic, func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "analytics.example" {
					analytics.Add(1)
					body, err := io.ReadAll(r.Body)
					if err != nil || string(body) != `SELECT COUNT(*) AS value FROM transactions WHERE application = "Approved Services"` {
						t.Fatal("Analytics query escaped the approved application", string(body), err)
					}
					return jsonResponse(200, `[]`), nil
				}
				path := strings.TrimPrefix(r.URL.Path, "/controller")
				if path != "/rest/applications/7" && !strings.HasPrefix(path, "/rest/applications/7/") {
					t.Fatal("unapproved application was queried", r.URL.Path)
				}
				switch {
				case strings.HasSuffix(r.URL.Path, "/applications/7"):
					if discoveryDenied {
						return jsonResponse(403, `{}`), nil
					}
					return jsonResponse(200, `[{"id":7,"name":"Approved Services"}]`), nil
				case strings.Contains(r.URL.Path, "metric"):
					return jsonResponse(403, `{}`), nil
				default:
					diagnostics.Add(1)
					return jsonResponse(200, `[]`), nil
				}
			})
			caps, err := c.TestApplications(context.Background(), []string{"7", "8"})
			if caps.Metrics || !caps.Events || !caps.Violations || !caps.Snapshots || caps.Status["metrics"] != "forbidden" || diagnostics.Load() != 3 {
				t.Fatal("full capability set was not independently tested", caps, err, diagnostics.Load())
			}
			if discoveryDenied {
				if err == nil || caps.Analytics || analytics.Load() != 0 {
					t.Fatal("unverified Analytics application scope was queried", caps, err, analytics.Load())
				}
			} else if err != nil || !caps.Analytics || caps.Status["analytics"] != "available_no_data" || analytics.Load() != 1 {
				t.Fatal(caps, err, analytics.Load())
			}
		})
	}
	var queries atomic.Int32
	c := fixtureClient(t, AuthBasic, func(*http.Request) (*http.Response, error) {
		queries.Add(1)
		return jsonResponse(200, `[]`), nil
	})
	for _, applications := range [][]string{nil, {"invalid"}, {"1", "1"}, make([]string, 33)} {
		if _, err := c.TestApplications(context.Background(), applications); err == nil {
			t.Fatal("invalid application approval accepted", applications)
		}
	}
	if queries.Load() != 0 {
		t.Fatal("invalid application approval made an upstream request")
	}
}

func TestAppDynamicsUnsupportedMetricFallbackOnly(t *testing.T) {
	for _, status := range []int{404, 405, 501, 401, 403, 429} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var legacy atomic.Int32
			c := fixtureClient(t, AuthBasic, func(r *http.Request) (*http.Response, error) {
				if strings.HasSuffix(r.URL.Path, "/metric-data") {
					legacy.Add(1)
					return jsonResponse(200, `[]`), nil
				}
				return jsonResponse(status, `{}`), nil
			})
			_, _, err := c.metrics(context.Background(), MetricSelection{ApplicationID: "1", Path: "calls"}, time.Now().Add(-time.Minute), time.Now(), 0)
			unsupported := status == 404 || status == 405 || status == 501
			if unsupported != (err == nil) || legacy.Load() != map[bool]int32{true: 1, false: 0}[unsupported] {
				t.Fatal(err, legacy.Load())
			}
		})
	}
}

func TestAppDynamicsCapabilityFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		status     int
		body, want string
	}{
		{403, `{"message":"Transaction Analytics license required"}`, "unlicensed"},
		{403, `{"message":"Permission denied"}`, "forbidden"},
		{402, `{}`, "unlicensed"},
	} {
		c := fixtureClient(t, AuthBasic, func(*http.Request) (*http.Response, error) { return jsonResponse(tc.status, tc.body), nil })
		var result []analyticsResponse
		err := c.query(context.Background(), "POST", "/events/query", nil, []byte("SELECT count(*) AS value FROM transactions"), true, &result)
		if capabilityStatus(err) != tc.want {
			t.Fatal(err, capabilityStatus(err))
		}
	}
	c := fixtureClient(t, AuthBasic, func(*http.Request) (*http.Response, error) { return nil, errors.New("private internal network detail") })
	_, err := c.Discover(context.Background(), "", "applications", "")
	if capabilityStatus(err) != "unreachable" || strings.Contains(err.Error(), "private") {
		t.Fatal(err)
	}
}

func TestAppDynamicsSharedExecutorSerializesPhysicalRequests(t *testing.T) {
	cfg := Config{Endpoint: "https://concurrency-pool.example", AccountName: "tenant", AuthType: AuthBasic}
	credentials := Credentials{Username: "user", Password: "password"}
	a, err := NewClient(cfg, credentials)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewClient(cfg, credentials)
	if err != nil {
		t.Fatal(err)
	}
	if a.controllerRate != b.controllerRate {
		t.Fatal("physical request concurrency remains per-client")
	}
	var active, maximum atomic.Int32
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		count := active.Add(1)
		for old := maximum.Load(); count > old && !maximum.CompareAndSwap(old, count); old = maximum.Load() {
		}
		time.Sleep(5 * time.Millisecond)
		active.Add(-1)
		return jsonResponse(200, `[]`), nil
	})
	a.http.Transport, b.http.Transport = transport, transport
	a.cfg.RequestsPerMinute, b.cfg.RequestsPerMinute = 1000000, 1000000
	done := make(chan error, 6)
	for index := range 6 {
		client := a
		if index%2 == 0 {
			client = b
		}
		go func() { _, err := client.Discover(context.Background(), "", "applications", ""); done <- err }()
	}
	for range 6 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if maximum.Load() != 1 {
		t.Fatal("executor exceeded its physical-request ceiling", maximum.Load())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.acquireRequest(ctx, false); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled request could enter the network", err)
	}
}
