package broker

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner"
)

func managedBrokerClient(t *testing.T, f *brokerServiceFixture) *BrokerClient {
	t.Helper()
	cfg := f.client.Transport.(*http.Transport).TLSClientConfig
	c, e := NewBrokerClient(BrokerClientConfig{Endpoint: f.url, ServerURI: "spiffe://apexload/java-broker", FacadeURI: "spiffe://apexload/java-facade/generator", InvocationTimeout: time.Second}, cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(c.Close)
	if e = c.Claim(t.Context()); e != nil {
		t.Fatal(e)
	}
	return c
}
func managedBrokerWave(t *testing.T, c *BrokerClient) *BrokerClientWave {
	t.Helper()
	w, e := c.Reserve(t.Context(), "run", "group", "approved", binding().ConfigurationSHA256, []javarunner.DedicatedHostContext{managedContext()})
	if e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(time.Second)
	for {
		ready, e := w.Ready(t.Context())
		if e != nil {
			t.Fatal(e)
		}
		if ready {
			return w
		}
		if time.Now().After(deadline) {
			t.Fatal("wave never ready")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestBrokerClientLifecycleRenewalAndPositiveCleanup(t *testing.T) {
	f := newBrokerServiceFixture(t, nil)
	c := managedBrokerClient(t, f)
	if c.LeaseInterval() != 2*time.Second {
		t.Fatal("wrong lease")
	}
	if e := c.Claim(t.Context()); e == nil {
		t.Fatal("owner replaced")
	}
	w := managedBrokerWave(t, c)
	hosts := w.Hosts()
	if len(hosts) != 1 || hosts[0].SessionID != "session" {
		t.Fatal("bad inventory")
	}
	id := hosts[0].HostID
	if _, e := w.Invoke(t.Context(), id, "init", nil); e == nil || f.calls.Load() != 0 {
		t.Fatal("precommit init")
	}
	if e := w.Commit(t.Context()); e != nil {
		t.Fatal(e)
	}
	if e := w.Commit(t.Context()); e == nil {
		t.Fatal("commit replay")
	}
	if _, e := w.Invoke(t.Context(), id, "init", map[string]string{"parameter": "value"}); e != nil {
		t.Fatal(e)
	}
	if e := c.Renew(t.Context()); e != nil {
		t.Fatal(e)
	}
	if e := w.RenewHost(t.Context(), id); e != nil {
		t.Fatal(e)
	}
	if _, e := w.Invoke(t.Context(), id, "action", nil); e != nil {
		t.Fatal(e)
	}
	if _, e := w.Invoke(t.Context(), id, "end", nil); e != nil {
		t.Fatal(e)
	}
	if _, e := w.Invoke(t.Context(), id, "action", nil); e == nil || f.calls.Load() != 3 {
		t.Fatal("closed session reused")
	}
	if e := c.Release(t.Context()); e != nil {
		t.Fatal(e)
	}
	if reaped, e := c.Cleanup(t.Context()); e != nil || reaped {
		t.Fatal("cleanup inferred from response", e)
	}
	r := record(t, f.store, f.epoch, id)
	if e := f.store.ConfirmTerminated(t.Context(), f.epoch, id, r.PodUID, r.ContainerID); e != nil {
		t.Fatal(e)
	}
	if reaped, e := c.Cleanup(t.Context()); e != nil || !reaped {
		t.Fatal("positive receipt not observed", e)
	}
}

func TestBrokerClientCapacityFailurePreservesExistingWave(t *testing.T) {
	f := newBrokerServiceFixture(t, nil)
	c := managedBrokerClient(t, f)
	w := managedBrokerWave(t, c)
	a, b := managedContext(), managedContext()
	a.SessionID = "increase-a"
	b.SessionID = "increase-b"
	_, e := c.Reserve(t.Context(), "run", "increase", "approved", binding().ConfigurationSHA256, []javarunner.DedicatedHostContext{a, b})
	var failure *BrokerFailure
	if !errors.As(e, &failure) || failure.Code != "java_broker_capacity_unavailable" {
		t.Fatal(e)
	}
	if e = c.Renew(t.Context()); e != nil {
		t.Fatal("rejection retired existing owner", e)
	}
	if e = w.Commit(t.Context()); e != nil {
		t.Fatal(e)
	}
	if _, e = w.Invoke(t.Context(), w.Hosts()[0].HostID, "init", nil); e != nil {
		t.Fatal(e)
	}
}

type brokerRoundTrip func(*http.Request) (*http.Response, error)

func (f brokerRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func corruptBrokerResponse(t *testing.T, c *BrokerClient, mutate func(*BrokerResponse)) {
	t.Helper()
	transport := c.http.Transport.(*http.Transport)
	t.Cleanup(transport.CloseIdleConnections)
	c.http.Transport = brokerRoundTrip(func(req *http.Request) (*http.Response, error) {
		response, e := transport.RoundTrip(req)
		if e != nil {
			return nil, e
		}
		var out BrokerResponse
		e = json.NewDecoder(response.Body).Decode(&out)
		response.Body.Close()
		if e != nil {
			return nil, e
		}
		mutate(&out)
		data, _ := json.Marshal(out)
		response.Body = io.NopCloser(bytes.NewReader(data))
		response.ContentLength = int64(len(data))
		return response, nil
	})
}

func TestBrokerClientForgedInvocationBecomesTerminalWithoutReplay(t *testing.T) {
	for _, field := range []string{"request", "epoch", "host", "configuration", "contract", "payload", "invocation"} {
		t.Run(field, func(t *testing.T) {
			f := newBrokerServiceFixture(t, nil)
			c := managedBrokerClient(t, f)
			w := managedBrokerWave(t, c)
			if e := w.Commit(t.Context()); e != nil {
				t.Fatal(e)
			}
			id := w.Hosts()[0].HostID
			corruptBrokerResponse(t, c, func(out *BrokerResponse) {
				switch field {
				case "request":
					out.RequestID = "forged"
				case "epoch":
					out.BrokerEpoch = "forged"
				case "host":
					out.Result.HostID = "other"
				case "configuration":
					out.Result.ConfigurationSHA256 = strings.Repeat("b", 64)
				case "contract":
					out.Result.Contract = "unknown"
				case "payload":
					out.Result.Payload = nil
				case "invocation":
					out.Result.InvocationID = "host-call-9"
				}
			})
			if _, e := w.Invoke(t.Context(), id, "init", nil); !errors.Is(e, ErrBrokerTransport) {
				t.Fatal("forged result accepted", e)
			}
			if _, e := w.Invoke(t.Context(), id, "init", nil); e == nil || f.calls.Load() != 1 {
				t.Fatal("ambiguous initialization replayed", e)
			}
		})
	}
}

func TestBrokerClientRejectsCredentialsAndInvalidLocalInputs(t *testing.T) {
	f := newBrokerServiceFixture(t, nil)
	tlsConfig := f.client.Transport.(*http.Transport).TLSClientConfig
	base := BrokerClientConfig{Endpoint: f.url, ServerURI: "spiffe://apexload/java-broker", FacadeURI: "spiffe://apexload/java-facade/generator", InvocationTimeout: time.Second}
	for _, mutate := range []func(*BrokerClientConfig){func(c *BrokerClientConfig) { c.Endpoint = "http://example.invalid" }, func(c *BrokerClientConfig) { c.ServerURI = "spiffe://apexload/java-host/host" }, func(c *BrokerClientConfig) { c.ServerURI = "spiffe://apexload/java-broker/../host" }, func(c *BrokerClientConfig) { c.FacadeURI = "spiffe://apexload/java-facade/other" }, func(c *BrokerClientConfig) { c.InvocationTimeout = 0 }} {
		cfg := base
		mutate(&cfg)
		if _, e := NewBrokerClient(cfg, tlsConfig); e == nil {
			t.Fatal("invalid client accepted")
		}
	}
	bad := tlsConfig.Clone()
	bad.RootCAs = nil
	if _, e := NewBrokerClient(base, bad); e == nil {
		t.Fatal("system trust accepted")
	}
	c := managedBrokerClient(t, f)
	w := managedBrokerWave(t, c)
	if e := w.Commit(t.Context()); e != nil {
		t.Fatal(e)
	}
	id := w.Hosts()[0].HostID
	if _, e := w.Invoke(t.Context(), id, "arbitrary", nil); !errors.Is(e, ErrState) {
		t.Fatal(e)
	}
	if _, e := w.Invoke(t.Context(), id, "init", map[string]string{"key": strings.Repeat("x", 65537)}); !errors.Is(e, ErrState) {
		t.Fatal(e)
	}
	if _, e := w.Invoke(t.Context(), id, "init", nil); e != nil || f.calls.Load() != 1 {
		t.Fatal("invalid local inputs consumed sequence", e)
	}
}

func TestBrokerClientIndependentHostLeasesDoNotPreserveAbandonedVuser(t *testing.T) {
	f := newBrokerServiceFixture(t, nil)
	c := managedBrokerClient(t, f)
	a, b := managedContext(), managedContext()
	a.SessionID = "healthy"
	b.SessionID = "abandoned"
	b.VUserID++
	w, e := c.Reserve(t.Context(), "run", "group", "approved", binding().ConfigurationSHA256, []javarunner.DedicatedHostContext{a, b})
	if e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(time.Second)
	for {
		ready, e := w.Ready(t.Context())
		if e != nil {
			t.Fatal(e)
		}
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("not ready")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if w.HostLeaseInterval() != time.Second {
		t.Fatal("wrong per-host renewal interval")
	}
	if e = w.Commit(t.Context()); e != nil {
		t.Fatal(e)
	}
	ids := map[string]string{}
	for _, host := range w.Hosts() {
		ids[host.SessionID] = host.HostID
		if _, e = w.Invoke(t.Context(), host.HostID, "init", nil); e != nil {
			t.Fatal(e)
		}
	}
	until := time.Now().Add(1300 * time.Millisecond)
	for time.Now().Before(until) {
		if e = c.Renew(t.Context()); e != nil {
			t.Fatal(e)
		}
		if e = w.RenewHost(t.Context(), ids["healthy"]); e != nil {
			t.Fatal(e)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if r := record(t, f.store, f.epoch, ids["abandoned"]); r.State != javarunner.HostQuarantined {
		t.Fatal("owner/peer heartbeat preserved abandoned Vuser", r.State)
	}
	if _, e = w.Invoke(t.Context(), ids["abandoned"], "action", nil); e == nil {
		t.Fatal("abandoned Vuser revived")
	}
	if e = c.Renew(t.Context()); e != nil {
		t.Fatal("single-host expiry fenced healthy owner", e)
	}
	if _, e = w.Invoke(t.Context(), ids["healthy"], "action", nil); e != nil {
		t.Fatal("single-host expiry killed peer", e)
	}
	if r := record(t, f.store, f.epoch, ids["healthy"]); r.State != javarunner.HostCommitted {
		t.Fatal("healthy peer retired", r.State)
	}
}

func TestBrokerClientCleanupRejectsMissingKnownHostInventory(t *testing.T) {
	f := newBrokerServiceFixture(t, nil)
	c := managedBrokerClient(t, f)
	_ = managedBrokerWave(t, c)
	if e := c.Release(t.Context()); e != nil {
		t.Fatal(e)
	}
	corruptBrokerResponse(t, c, func(out *BrokerResponse) { out.State = "reaped"; out.Hosts = nil })
	if reaped, e := c.Cleanup(t.Context()); reaped || !errors.Is(e, ErrBrokerTransport) {
		t.Fatal("partial cleanup inventory accepted", e)
	}
}

func TestBrokerClientRejectsProfileDriftBeforeReservation(t *testing.T) {
	f := newBrokerServiceFixture(t, nil)
	c := managedBrokerClient(t, f)
	_, e := c.Reserve(t.Context(), "run", "group", "approved", strings.Repeat("b", 64), []javarunner.DedicatedHostContext{managedContext()})
	if e == nil {
		t.Fatal("changed profile accepted")
	}
	records, _, e := f.store.Snapshot(t.Context(), f.epoch)
	if e != nil || len(records) != 0 || f.calls.Load() != 0 {
		t.Fatal("profile drift provisioned a host", e)
	}
	w := managedBrokerWave(t, c)
	if e := w.Commit(t.Context()); e != nil {
		t.Fatal(e)
	}
}

func TestBrokerClientCloseHostPreservesPeerAndReservation(t *testing.T) {
	f := newBrokerServiceFixture(t, nil)
	c := managedBrokerClient(t, f)
	a, b := managedContext(), managedContext()
	a.SessionID, b.SessionID = "closed", "peer"
	b.VUserID++
	w, e := c.Reserve(t.Context(), "run", "group", "approved", binding().ConfigurationSHA256, []javarunner.DedicatedHostContext{a, b})
	if e != nil {
		t.Fatal(e)
	}
	ids := map[string]string{}
	for _, h := range w.Hosts() {
		ids[h.SessionID] = h.HostID
	}
	if e := w.CloseHost(t.Context(), ids["closed"]); e == nil {
		t.Fatal("partial cold peak closed")
	}
	deadline := time.Now().Add(time.Second)
	for {
		ready, e := w.Ready(t.Context())
		if e != nil {
			t.Fatal(e)
		}
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("not ready")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if e := w.Commit(t.Context()); e != nil {
		t.Fatal(e)
	}
	if e := w.CloseHost(t.Context(), "unknown"); e == nil {
		t.Fatal("unknown host closed")
	}
	if e := w.CloseHost(t.Context(), ids["closed"]); e != nil {
		t.Fatal(e)
	}
	if r := record(t, f.store, f.epoch, ids["closed"]); r.State != javarunner.HostQuarantined || r.Failure != "dedicated_vuser_closed" {
		t.Fatal("reservation released without proof", r.State, r.Failure)
	}
	if e := w.RenewHost(t.Context(), ids["closed"]); e == nil {
		t.Fatal("closed host renewed")
	}
	if _, e := w.Invoke(t.Context(), ids["closed"], "init", nil); e == nil {
		t.Fatal("closed host invoked")
	}
	if e := w.RenewHost(t.Context(), ids["peer"]); e != nil {
		t.Fatal(e)
	}
	if _, e := w.Invoke(t.Context(), ids["peer"], "init", nil); e != nil || f.calls.Load() != 1 {
		t.Fatal("peer lost", e)
	}
}
