package javasidecar

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/javarunner"
)

// Inspection fixtures only: no executable JVM or user class is supplied. The
// network control surface must never attempt to prepare or invoke these assets.
func controlProfileFixture(t *testing.T) PooledControlProfile {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(dir, "jdk")
	if err := os.MkdirAll(filepath.Join(home, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"bin/java", "release"} {
		if err := os.WriteFile(filepath.Join(home, name), []byte("fixture-only"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"host.jar", "shim.jar", "script.jar"} {
		var data bytes.Buffer
		writer := zip.NewWriter(&data)
		entry, err := writer.Create("fixture.txt")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = entry.Write([]byte(name)); err != nil {
			t.Fatal(err)
		}
		if err = writer.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
	}
	source := filepath.Join(dir, "source.zip")
	if err := os.WriteFile(source, []byte("source"), 0600); err != nil {
		t.Fatal(err)
	}
	q := javarunner.PooledQualificationRequest{Launch: javarunner.PooledLaunchSpec{ContainmentBackend: "delegated_linux", JDK: javarunner.ResolvedJDK{Home: home, JavaBin: filepath.Join(home, "bin", "java"), Version: "17-fixture", Major: 17, VMFamily: javarunner.JavaVMHotSpot}, HarnessJar: filepath.Join(dir, "host.jar"), ShimJar: filepath.Join(dir, "shim.jar"), TargetClass: "Actions", WorkDir: dir, ScriptClasspath: []string{filepath.Join(dir, "script.jar")}, VMParameters: []string{"-Xmx128m"}, QualifiedCleanup: true}, SourceArchive: source, SharingProfile: "sharing", Resources: javarunner.PooledResourceProfile{ID: "resource", SlotsPerHost: 2, HeapBytes: 128 << 20, NativeBytes: 16 << 20, StackBytes: 8 << 20, MetaspaceBytes: 16 << 20, CodeCacheBytes: 16 << 20, CaptureBytes: 1 << 20}}
	digest, err := javarunner.InspectPooledQualification(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	e := strings.Repeat("e", 64)
	registry, err := javarunner.NewPooledQualificationRegistry([]javarunner.PooledQualificationProfile{{Schema: javarunner.PooledQualificationSchema, ID: "qualified", ConfigurationSHA256: digest, Evidence: javarunner.PooledQualificationEvidence{ApprovedBy: "test-operator", InspectionSHA256: e, RepresentativeRunSHA256: e, LoaderChecksSHA256: e, CleanupChecksSHA256: e, NativeAssessment: "no_application_native_observed"}}})
	if err != nil {
		t.Fatal(err)
	}
	selection, err := registry.Select(context.Background(), "qualified", 2, q)
	if err != nil {
		t.Fatal(err)
	}
	return PooledControlProfile{Selection: selection, Budget: javarunner.HostBudget{MaxHosts: 2, MaxVUsers: 4, MaxMemoryBytes: selection.Decision().HostMemoryBytes * 2}}
}

func controlServerFixture(t *testing.T, allowPreparation ...bool) (*Server, *Client, *javarunner.HostCapacity) {
	t.Helper()
	p := controlProfileFixture(t)
	p.AllowPreparation = len(allowPreparation) > 0 && allowPreparation[0]
	p.AllowCommit = len(allowPreparation) > 1 && allowPreparation[1]
	p.AllowInvocation = len(allowPreparation) > 2 && allowPreparation[2]
	profiles, err := NewPooledControlProfiles(map[string]PooledControlProfile{"pilot": p})
	if err != nil {
		t.Fatal(err)
	}
	c, err := javarunner.NewHostCapacity(p.Budget)
	if err != nil {
		t.Fatal(err)
	}
	a, err := javarunner.NewDedicatedHostAdmission(c, p.Selection.Decision().HostMemoryBytes)
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(ServerOptions{Operator: config.JavaVuserOperatorConfig{Enabled: true, AuthToken: testSidecarAuthToken, MaxProcesses: 2}, HostAdmission: a, PooledControlProfiles: profiles})
	httpServer := httptest.NewServer(s)
	client, err := NewClientWithToken(httpServer.URL, testSidecarAuthToken, httpServer.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ClaimOwner(context.Background(), "owner"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
		httpServer.Close()
	})
	return s, client, c
}

func claimControl(t *testing.T, c *Client) PooledControlResponse {
	t.Helper()
	r, err := c.PooledControl(context.Background(), PooledControlRequest{Operation: "claim", RequestID: "claim-1", ProfileID: "pilot", ExecutionID: "execution", GroupID: "group"})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func controlRequest(r PooledControlResponse, operation, id string) PooledControlRequest {
	return PooledControlRequest{Operation: operation, RequestID: id, RunID: r.RunID, RunToken: r.RunToken}
}

func requireControlError(t *testing.T, err error, code string) {
	t.Helper()
	var remote *RemoteError
	if !errors.As(err, &remote) || remote.Code != code {
		t.Fatalf("error=%v, want %s", err, code)
	}
}

func TestPooledControlReservationLifecycle(t *testing.T) {
	s, c, capacity := controlServerFixture(t)
	claim := claimControl(t, c)
	q := controlRequest(claim, "reserve", "reserve-1")
	q.VUsers = 3
	q.PrepareTimeoutMS = 1000
	r, err := c.PooledControl(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := c.PooledControl(context.Background(), q)
	if err != nil || !reflect.DeepEqual(r, replay) {
		t.Fatalf("replay changed: %v", err)
	}
	if u := capacity.Snapshot(); u.Hosts != 2 || u.VUsers != 3 {
		t.Fatal(u)
	}
	q.VUsers = 2
	_, err = c.PooledControl(context.Background(), q)
	requireControlError(t, err, "pooled_request_conflict")
	if _, err := c.PooledControl(context.Background(), controlRequest(claim, "renew", "renew-1")); err != nil {
		t.Fatal(err)
	}
	abort := controlRequest(claim, "abort", "abort-1")
	abort.ReservationID = r.ReservationID
	if _, err := c.PooledControl(context.Background(), abort); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PooledControl(context.Background(), abort); err != nil {
		t.Fatal(err)
	}
	q.VUsers = 3
	_, err = c.PooledControl(context.Background(), q)
	requireControlError(t, err, "pooled_host_state_invalid")
	if capacity.Snapshot() != (javarunner.HostUsage{}) {
		t.Fatal("abort retained unstarted allocation")
	}
	closed, err := c.PooledControl(context.Background(), controlRequest(claim, "close", "close-1"))
	if err != nil || !closed.Reaped {
		t.Fatal("close unproven", err)
	}
	if _, err := c.PooledControl(context.Background(), controlRequest(claim, "close", "close-2")); err != nil {
		t.Fatal("close retry failed", err)
	}
	if len(s.running) != 0 || len(s.allSessions) != 0 {
		t.Fatal("control operations started dedicated Java")
	}
}

func TestPooledControlFencesAndConcurrentGroupLimit(t *testing.T) {
	_, c, _ := controlServerFixture(t)
	claim := claimControl(t, c)
	bad := controlRequest(claim, "renew", "renew-1")
	bad.RunToken = strings.Repeat("f", 64)
	_, err := c.PooledControl(context.Background(), bad)
	requireControlError(t, err, "pooled_run_fenced")
	// Independent groups now run concurrently; each still owns its own token.
	second, err := c.PooledControl(context.Background(), PooledControlRequest{Operation: "claim", RequestID: "claim-2", ProfileID: "pilot", ExecutionID: "other-execution", GroupID: "other-group"})
	if err != nil || second.RunID == claim.RunID || second.RunToken == claim.RunToken {
		t.Fatalf("second group claim: %+v %v", second, err)
	}
	crossed := controlRequest(claim, "renew", "renew-cross")
	crossed.RunToken = second.RunToken
	_, err = c.PooledControl(context.Background(), crossed)
	requireControlError(t, err, "pooled_run_fenced")
	// The same execution/group can never be reopened in one epoch.
	_, err = c.PooledControl(context.Background(), PooledControlRequest{Operation: "claim", RequestID: "claim-dup", ProfileID: "pilot", ExecutionID: "execution", GroupID: "group"})
	requireControlError(t, err, "pooled_host_identity_invalid")
	for i := 2; i < maxConcurrentPooledControlRuns; i++ {
		if _, err := c.PooledControl(context.Background(), PooledControlRequest{Operation: "claim", RequestID: fmt.Sprintf("claim-%d", i+1), ProfileID: "pilot", ExecutionID: "execution", GroupID: fmt.Sprintf("group-%d", i)}); err != nil {
			t.Fatalf("claim %d: %v", i, err)
		}
	}
	_, err = c.PooledControl(context.Background(), PooledControlRequest{Operation: "claim", RequestID: "claim-over", ProfileID: "pilot", ExecutionID: "execution", GroupID: "group-over"})
	requireControlError(t, err, "pooled_group_limit")
	// Closing one run frees exactly one concurrent-run admission.
	if closed, err := c.PooledControl(context.Background(), controlRequest(second, "close", "close-second")); err != nil || !closed.Reaped {
		t.Fatalf("close: %+v %v", closed, err)
	}
	if _, err := c.PooledControl(context.Background(), PooledControlRequest{Operation: "claim", RequestID: "claim-after-close", ProfileID: "pilot", ExecutionID: "execution", GroupID: "group-after-close"}); err != nil {
		t.Fatalf("claim after close: %v", err)
	}
	if _, err := c.ClaimOwner(context.Background(), "replacement"); err != nil {
		t.Fatal(err)
	}
	_, err = c.PooledControl(context.Background(), controlRequest(claim, "renew", "renew-2"))
	requireControlError(t, err, "pooled_run_fenced")
}

func TestPooledControlConcurrentReplayChargesOnce(t *testing.T) {
	_, c, capacity := controlServerFixture(t)
	claim := claimControl(t, c)
	q := controlRequest(claim, "reserve", "reserve-1")
	q.VUsers = 2
	q.PrepareTimeoutMS = 1000
	var wg sync.WaitGroup
	responses := make(chan PooledControlResponse, 10)
	for range 10 {
		wg.Go(func() {
			r, err := c.PooledControl(context.Background(), q)
			if err != nil {
				t.Error(err)
				return
			}
			responses <- r
		})
	}
	wg.Wait()
	close(responses)
	id := ""
	for r := range responses {
		if id != "" && id != r.ReservationID {
			t.Fatal("retry created new reservation")
		}
		id = r.ReservationID
	}
	if u := capacity.Snapshot(); u.Hosts != 1 || u.VUsers != 2 {
		t.Fatal(u)
	}
}

func TestPooledControlExpiredReservationCannotReplay(t *testing.T) {
	s, c, capacity := controlServerFixture(t)
	claim := claimControl(t, c)
	q := controlRequest(claim, "reserve", "reserve-1")
	q.VUsers = 2
	q.PrepareTimeoutMS = 20
	r, err := c.PooledControl(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if r.PrepareDeadlineUnixMS <= 0 {
		t.Fatal("missing original deadline")
	}
	deadline := time.Now().Add(time.Second)
	for capacity.Snapshot().Hosts != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if capacity.Snapshot().Hosts != 0 {
		t.Fatal("preparation did not expire")
	}
	_, err = c.PooledControl(context.Background(), q)
	requireControlError(t, err, "pooled_host_state_invalid")
	run := s.selectedPooledRuns(nil)[0]
	run.mu.Lock()
	run.deadline = time.Now().Add(-time.Second)
	run.armLocked()
	run.mu.Unlock()
	select {
	case <-run.closed:
	case <-time.After(time.Second):
		t.Fatal("run did not expire")
	}
	_, err = c.PooledControl(context.Background(), controlRequest(claim, "renew", "renew-1"))
	requireControlError(t, err, "pooled_owner_fenced")
}

func TestPooledControlRejectsUnnegotiatedAndUnsafeRequests(t *testing.T) {
	s, c, _ := controlServerFixture(t)
	owner, epoch, _, _ := c.ownerAndRuntimeLease()
	base := PooledControlRequest{Contract: ContractVersion, PooledContract: PooledControlContract, OwnerID: owner, OwnerEpoch: epoch, Operation: "claim", RequestID: "claim", ProfileID: "pilot", ExecutionID: "execution", GroupID: "group"}
	for _, tc := range []struct {
		name   string
		mutate func(*PooledControlRequest)
		extra  string
		token  string
		code   int
	}{
		{"wrong contract", func(q *PooledControlRequest) { q.PooledContract = "old" }, "", testSidecarAuthToken, http.StatusPreconditionFailed},
		{"unknown operation", func(q *PooledControlRequest) { q.Operation = "prepare" }, "", testSidecarAuthToken, http.StatusBadRequest},
		{"operator arguments", func(*PooledControlRequest) {}, `,"vm_parameters":["-Xmx64m"]`, testSidecarAuthToken, http.StatusBadRequest},
		{"unauthenticated", func(*PooledControlRequest) {}, "", "wrong", http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := base
			tc.mutate(&q)
			body, _ := json.Marshal(q)
			if tc.extra != "" {
				body = append(body[:len(body)-1], []byte(tc.extra+"}")...)
			}
			r := httptest.NewRequest(http.MethodPost, "/v1/pooled-control", bytes.NewReader(body))
			r.Header.Set("X-Apexload-Java-Runner-Token", tc.token)
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			if w.Code != tc.code {
				t.Fatalf("%d: %s", w.Code, w.Body.String())
			}
		})
	}
	if len(s.selectedPooledRuns(nil)) != 0 {
		t.Fatal("invalid request created a run")
	}
	disabled := NewServer(ServerOptions{Operator: config.JavaVuserOperatorConfig{Enabled: true, AuthToken: testSidecarAuthToken}})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/pooled-control", strings.NewReader("{}"))
	r.Header.Set("X-Apexload-Java-Runner-Token", testSidecarAuthToken)
	disabled.ServeHTTP(w, r)
	if w.Code != http.StatusPreconditionFailed || !strings.Contains(w.Body.String(), "pooled_control_disabled") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestPooledControlProfilesFreezeAndValidate(t *testing.T) {
	p := controlProfileFixture(t)
	source := map[string]PooledControlProfile{"pilot": p}
	profiles, err := NewPooledControlProfiles(source)
	if err != nil {
		t.Fatal(err)
	}
	delete(source, "pilot")
	if len(profiles.profiles) != 1 {
		t.Fatal("registry aliased caller map")
	}
	for _, bad := range []map[string]PooledControlProfile{nil, {"bad/path": p}, {"unqualified": {}}, {"oversized": {Selection: p.Selection, Budget: javarunner.HostBudget{MaxHosts: maxPooledControlHosts + 1, MaxVUsers: 4, MaxMemoryBytes: p.Budget.MaxMemoryBytes}}}, {"too-many-vusers": {Selection: p.Selection, Budget: javarunner.HostBudget{MaxHosts: 1, MaxVUsers: maxPooledControlVUsers + 1, MaxMemoryBytes: p.Budget.MaxMemoryBytes}}}} {
		if _, err := NewPooledControlProfiles(bad); err == nil {
			t.Fatal("invalid profiles accepted")
		}
	}
}

func TestPooledControlReplayLimitPreservesRenewAndClose(t *testing.T) {
	_, c, _ := controlServerFixture(t)
	claim := claimControl(t, c)
	for i := 0; i < maxPooledControlReplies; i++ {
		q := controlRequest(claim, "abort", fmt.Sprintf("abort-%d", i))
		q.ReservationID = "unknown-reservation"
		_, err := c.PooledControl(context.Background(), q)
		requireControlError(t, err, "pooled_host_identity_invalid")
	}
	q := controlRequest(claim, "reserve", "overflow")
	q.VUsers = 2
	q.PrepareTimeoutMS = 1000
	_, err := c.PooledControl(context.Background(), q)
	requireControlError(t, err, "pooled_control_limit")
	if _, err := c.PooledControl(context.Background(), controlRequest(claim, "renew", "renew")); err != nil {
		t.Fatal("renew queued behind replay limit", err)
	}
	if response, err := c.PooledControl(context.Background(), controlRequest(claim, "close", "close")); err != nil || !response.Reaped {
		t.Fatal("close blocked by replay limit", err)
	}
}
