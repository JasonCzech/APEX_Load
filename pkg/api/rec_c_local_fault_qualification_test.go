package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

// reccAuthenticatedFaultTarget implements the exact cmd/apexfault-target HTTP
// contract (/healthz, /load, /snapshot) with constant-time Bearer token checks
// for distinct LoadToken and ObserverToken credentials, 16-hex-char run_id
// validation, per-generation traffic accounting via X-Apexload-Fault-Generation,
// and concurrent/superseded generation overlap detection. It has no controller,
// generator, or assignment-authority state.
type reccAuthenticatedFaultTarget struct {
	runID         string
	loadToken     string
	observerToken string
	instanceID    string
	startedAtUnix float64

	mu                        sync.Mutex
	totalRequestsByTest       map[string]uint64
	perGenerationByTest       map[string]map[uint64]uint64
	highestGenerationByTest   map[string]uint64
	duplicateOverlapByTest    map[string]uint64
	unauthorizedRejectedCount uint64
}

type reccTargetSnapshot struct {
	SchemaVersion              int               `json:"schema_version"`
	RunID                      string            `json:"run_id"`
	TestID                     string            `json:"test_id"`
	TargetInstanceID           string            `json:"target_instance_id"`
	StartedAt                  float64           `json:"started_at_unix"`
	ObservedAt                 float64           `json:"observed_at_unix"`
	TargetRequests             uint64            `json:"target_requests"`
	PerGenerationRequests      map[string]uint64 `json:"per_generation_requests,omitempty"`
	HighestGeneration          uint64            `json:"highest_generation,omitempty"`
	DuplicateGenerationOverlap uint64            `json:"duplicate_generation_overlap,omitempty"`
}

func newRECCAuthenticatedFaultTarget(t *testing.T, rep int) (*reccAuthenticatedFaultTarget, *httptest.Server) {
	t.Helper()
	var runBytes [8]byte
	if _, err := rand.Read(runBytes[:]); err != nil {
		t.Fatalf("generate fault run_id: %v", err)
	}
	var instBytes [16]byte
	if _, err := rand.Read(instBytes[:]); err != nil {
		t.Fatalf("generate target instance_id: %v", err)
	}
	runID := hex.EncodeToString(runBytes[:])
	loadToken := fmt.Sprintf("recc-load-token-rep-%d-%s", rep, runID)
	observerToken := fmt.Sprintf("recc-observer-token-rep-%d-%s", rep, runID)
	target := &reccAuthenticatedFaultTarget{
		runID:                   runID,
		loadToken:               loadToken,
		observerToken:           observerToken,
		instanceID:              hex.EncodeToString(instBytes[:]),
		startedAtUnix:           float64(time.Now().UnixNano()) / 1e9,
		totalRequestsByTest:     make(map[string]uint64),
		perGenerationByTest:     make(map[string]map[uint64]uint64),
		highestGenerationByTest: make(map[string]uint64),
		duplicateOverlapByTest:  make(map[string]uint64),
	}
	server := httptest.NewServer(target)
	t.Cleanup(server.Close)
	return target, server
}

func (f *reccAuthenticatedFaultTarget) checkBearer(r *http.Request, expected string) bool {
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return false
	}
	want := "Bearer " + expected
	return subtle.ConstantTimeCompare([]byte(values[0]), []byte(want)) == 1
}

func (f *reccAuthenticatedFaultTarget) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.RawQuery != "" {
		http.Error(w, "query parameters are not supported", http.StatusBadRequest)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	switch r.URL.Path {
	case "/healthz":
		w.WriteHeader(http.StatusNoContent)
	case "/load":
		if !f.checkBearer(r, f.loadToken) {
			f.mu.Lock()
			f.unauthorizedRejectedCount++
			f.mu.Unlock()
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		runVals := r.Header.Values("X-Apexload-Fault-Run")
		if len(runVals) != 1 || runVals[0] != f.runID {
			http.Error(w, "run mismatch", http.StatusConflict)
			return
		}
		testVals := r.Header.Values("X-Apexload-Fault-Test")
		if len(testVals) != 1 || strings.TrimSpace(testVals[0]) == "" {
			http.Error(w, "invalid test identity", http.StatusBadRequest)
			return
		}
		testID := testVals[0]
		genVals := r.Header.Values("X-Apexload-Fault-Generation")
		if len(genVals) != 1 {
			http.Error(w, "invalid generation identity", http.StatusBadRequest)
			return
		}
		gen, err := strconv.ParseUint(genVals[0], 10, 64)
		if err != nil || gen == 0 {
			http.Error(w, "invalid generation identity", http.StatusBadRequest)
			return
		}

		f.mu.Lock()
		f.totalRequestsByTest[testID]++
		if f.perGenerationByTest[testID] == nil {
			f.perGenerationByTest[testID] = make(map[uint64]uint64)
		}
		f.perGenerationByTest[testID][gen]++
		if highest := f.highestGenerationByTest[testID]; gen < highest {
			f.duplicateOverlapByTest[testID]++
		} else if gen > highest {
			f.highestGenerationByTest[testID] = gen
		}
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case "/snapshot":
		if !f.checkBearer(r, f.observerToken) {
			f.mu.Lock()
			f.unauthorizedRejectedCount++
			f.mu.Unlock()
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		testVals := r.Header.Values("X-Apexload-Fault-Test")
		if len(testVals) != 1 || strings.TrimSpace(testVals[0]) == "" {
			http.Error(w, "invalid test identity", http.StatusBadRequest)
			return
		}
		testID := testVals[0]
		f.mu.Lock()
		var perGen map[string]uint64
		if len(f.perGenerationByTest[testID]) > 0 {
			perGen = make(map[string]uint64, len(f.perGenerationByTest[testID]))
			for g, count := range f.perGenerationByTest[testID] {
				perGen[strconv.FormatUint(g, 10)] = count
			}
		}
		snap := reccTargetSnapshot{
			SchemaVersion:              2,
			RunID:                      f.runID,
			TestID:                     testID,
			TargetInstanceID:           f.instanceID,
			StartedAt:                  f.startedAtUnix,
			ObservedAt:                 float64(time.Now().UnixNano()) / 1e9,
			TargetRequests:             f.totalRequestsByTest[testID],
			PerGenerationRequests:      perGen,
			HighestGeneration:          f.highestGenerationByTest[testID],
			DuplicateGenerationOverlap: f.duplicateOverlapByTest[testID],
		}
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(snap)
	default:
		http.NotFound(w, r)
	}
}

func (f *reccAuthenticatedFaultTarget) observeSnapshotHTTP(t *testing.T, serverURL, testID string) reccTargetSnapshot {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, serverURL+"/snapshot", nil)
	if err != nil {
		t.Fatalf("new snapshot request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+f.observerToken)
	req.Header.Set("X-Apexload-Fault-Test", testID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("execute snapshot request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("snapshot HTTP status = %d, want 200", resp.StatusCode)
	}
	var snap reccTargetSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	return snap
}

func (f *reccAuthenticatedFaultTarget) waitForGenerationTraffic(t *testing.T, serverURL, testID string, gen uint64, minRequests uint64) {
	t.Helper()
	key := strconv.FormatUint(gen, 10)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		snap := f.observeSnapshotHTTP(t, serverURL, testID)
		if snap.PerGenerationRequests[key] >= minRequests {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	snap := f.observeSnapshotHTTP(t, serverURL, testID)
	t.Fatalf("timed out waiting for >= %d requests at generation %d for %s (snapshot=%+v)", minRequests, gen, testID, snap)
}

// reccRealGenerator wraps a real in-process generatorrpc.Service (*restartGenerator)
// and attaches a per-generator outbound HTTP proxy that stamps outbound VUser
// HTTP requests with the execution generation accepted by that generator's real
// gRPC control plane (ClaimExecutionEpoch, ClaimExecutionEpochWithAdoptions,
// ClaimRunSupervision, or AssignWork).
type reccRealGenerator struct {
	id               string
	incarnation      string
	targetURL        string
	outboundServer   *httptest.Server
	outboundURL      string
	rg               *restartGenerator
	acceptedEpoch    atomic.Uint64
	claimCalls       atomic.Int32
	archiveReadCalls atomic.Int32
	rewrittenHashes  sync.Map
}

type reccRealClient struct {
	*restartRecordingClient
	gen *reccRealGenerator
}

func wrapRECCRestartGenerator(t *testing.T, id string, rg *restartGenerator, targetURL string) *reccRealGenerator {
	t.Helper()
	incarnation := "pod-" + strings.TrimPrefix(id, "gen-")
	g := &reccRealGenerator{
		id:          id,
		incarnation: incarnation,
		targetURL:   targetURL,
		rg:          rg,
	}
	g.outboundServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		epoch := g.acceptedEpoch.Load()
		forwardReq, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL+r.URL.RequestURI(), nil)
		if err != nil {
			http.Error(w, "forward build failed", http.StatusInternalServerError)
			return
		}
		for k, vals := range r.Header {
			if strings.EqualFold(k, "X-Apexload-Fault-Generation") {
				continue
			}
			for _, v := range vals {
				forwardReq.Header.Add(k, v)
			}
		}
		forwardReq.Header.Set("X-Apexload-Fault-Generation", strconv.FormatUint(epoch, 10))
		resp, err := http.DefaultClient.Do(forwardReq)
		if err != nil {
			http.Error(w, "forward failed", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		for k, vals := range resp.Header {
			for _, v := range vals {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	g.outboundURL = g.outboundServer.URL
	t.Cleanup(g.outboundServer.Close)
	return g
}

func newRECCRealGenerator(t *testing.T, id string, maxVUsers int, readyCapacity bool, targetURL string) *reccRealGenerator {
	t.Helper()
	rg := newRestartGenerator(t, id, maxVUsers, readyCapacity)
	return wrapRECCRestartGenerator(t, id, rg, targetURL)
}

func (g *reccRealGenerator) advanceAcceptedEpoch(gen uint64) {
	if gen == 0 {
		return
	}
	for {
		cur := g.acceptedEpoch.Load()
		if gen <= cur {
			return
		}
		if g.acceptedEpoch.CompareAndSwap(cur, gen) {
			return
		}
	}
}

func (g *reccRealGenerator) rewriteScriptForGenerator(content []byte, expectedHash string) ([]byte, string) {
	if len(content) == 0 {
		if expectedHash != "" {
			if mapped, ok := g.rewrittenHashes.Load(expectedHash); ok {
				return content, mapped.(string)
			}
		}
		return content, expectedHash
	}
	if !bytes.Contains(content, []byte(g.targetURL)) {
		return content, expectedHash
	}
	rewritten := bytes.ReplaceAll(content, []byte(g.targetURL), []byte(g.outboundURL))
	sum := sha256.Sum256(rewritten)
	newHash := hex.EncodeToString(sum[:])
	if expectedHash != "" {
		g.rewrittenHashes.Store(expectedHash, newHash)
	}
	return rewritten, newHash
}

func (g *reccRealGenerator) dial() (generatorClient, error) {
	base, err := g.rg.dial()
	if err != nil {
		return nil, err
	}
	return &reccRealClient{
		restartRecordingClient: base.(*restartRecordingClient),
		gen:                    g,
	}, nil
}

func (c *reccRealClient) ClaimExecutionEpoch(ctx context.Context, generation uint64, owner string, ttl time.Duration) (generatorrpc.ExecutionEpochResult, error) {
	c.gen.claimCalls.Add(1)
	res, err := c.restartRecordingClient.ClaimExecutionEpoch(ctx, generation, owner, ttl)
	if err == nil && res.Accepted {
		c.gen.advanceAcceptedEpoch(res.CurrentGeneration)
	}
	return res, err
}

func (c *reccRealClient) ClaimExecutionEpochWithAdoptions(ctx context.Context, generation uint64, owner string, ttl time.Duration, adoptions []generatorrpc.RunAdoption) (generatorrpc.ExecutionEpochResult, error) {
	c.gen.claimCalls.Add(1)
	res, err := c.restartRecordingClient.ClaimExecutionEpochWithAdoptions(ctx, generation, owner, ttl, adoptions)
	if err == nil && res.Accepted {
		if len(adoptions) == 0 {
			c.gen.advanceAcceptedEpoch(res.CurrentGeneration)
		} else {
			for _, a := range res.Adoptions {
				if a.Outcome == pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED {
					c.gen.advanceAcceptedEpoch(res.CurrentGeneration)
					break
				}
			}
		}
	}
	return res, err
}

func (c *reccRealClient) ClaimRunSupervision(ctx context.Context, claim generatorrpc.RunSupervisionClaim) (generatorrpc.RunSupervisionResult, error) {
	res, err := c.restartRecordingClient.ClaimRunSupervision(ctx, claim)
	if err == nil && res.Accepted {
		c.gen.advanceAcceptedEpoch(res.CurrentGeneration)
	}
	return res, err
}

func (c *reccRealClient) DistributeScript(ctx context.Context, script generatorrpc.Script) (generatorrpc.ScriptResult, error) {
	script.Content, script.ContentHash = c.gen.rewriteScriptForGenerator(script.Content, script.ContentHash)
	return c.restartRecordingClient.DistributeScript(ctx, script)
}

func (c *reccRealClient) AssignWork(ctx context.Context, assignment generatorrpc.Assignment) (generatorrpc.AssignmentResult, error) {
	assignment.ScriptContent, assignment.ExpectedScriptHash = c.gen.rewriteScriptForGenerator(assignment.ScriptContent, assignment.ExpectedScriptHash)
	res, err := c.restartRecordingClient.AssignWork(ctx, assignment)
	if err == nil && res.Accepted {
		c.gen.advanceAcceptedEpoch(assignment.ExecutionGeneration)
	}
	return res, err
}

func (c *reccRealClient) GetArchiveArtifact(ctx context.Context, request generatorrpc.ArchiveArtifactRequest) ([]byte, error) {
	c.gen.archiveReadCalls.Add(1)
	return c.restartRecordingClient.GetArchiveArtifact(ctx, request)
}

func (c *reccRealClient) StreamArchiveArtifact(ctx context.Context, request generatorrpc.ArchiveArtifactRequest, writer io.Writer, maxBytes int64) (int64, error) {
	c.gen.archiveReadCalls.Add(1)
	return c.restartRecordingClient.StreamArchiveArtifact(ctx, request, writer, maxBytes)
}

func buildRECCLoadScript(targetURL, loadToken, runID, testID string) *dsl.Script {
	headers := map[string]string{
		"Authorization":         "Bearer " + loadToken,
		"X-Apexload-Fault-Run":  runID,
		"X-Apexload-Fault-Test": testID,
	}
	return &dsl.Script{
		ID:       "recc-script-" + testID,
		Name:     "REC-C Authenticated Fault Target Load",
		Protocol: "http",
		Config: dsl.ScriptConfig{
			BaseURL:   targetURL,
			HTTP:      dsl.HTTPConfig{DefaultHeaders: headers},
			ThinkTime: dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 20},
		},
		Actions: []dsl.Action{
			{
				Name:           "get_load",
				Type:           "GET",
				Target:         "/load",
				Headers:        headers,
				ThinkTimeAfter: true,
			},
		},
	}
}

type reccScenarioRepResult struct {
	Scenario                        string            `json:"scenario"`
	Repetition                      int               `json:"repetition"`
	RunID                           string            `json:"run_id"`
	TestID                          string            `json:"test_id"`
	Workload                        string            `json:"workload"`
	Topology                        string            `json:"topology"`
	KEDAOrdinalFloor                int               `json:"keda_ordinal_floor"`
	QuotaFreshnessVerified          bool              `json:"quota_freshness_verified"`
	ReservationDecision             string            `json:"reservation_decision"`
	InjectedBoundary                string            `json:"injected_boundary"`
	TerminalStatus                  string            `json:"terminal_status"`
	APIOutageWindowMS               float64           `json:"api_outage_window_ms"`
	LeadershipAcquisitionMS         float64           `json:"leadership_acquisition_ms"`
	GeneratorDiscoveryMS            float64           `json:"generator_discovery_ms"`
	ResumedTrafficMS                float64           `json:"resumed_traffic_ms"`
	EvidenceAttachmentMS            float64           `json:"evidence_attachment_ms"`
	ArchiveRecoveryMS               float64           `json:"archive_recovery_ms"`
	TotalRecoveryMS                 float64           `json:"total_recovery_ms"`
	TargetInstanceID                string            `json:"target_instance_id"`
	TargetTotalRequests             uint64            `json:"target_total_requests"`
	RetainedEvidenceTransactions    uint64            `json:"retained_evidence_transactions"`
	PerGenerationRequests           map[string]uint64 `json:"per_generation_requests"`
	ReconciledGenerationSum         uint64            `json:"reconciled_generation_sum"`
	DuplicateGenerationOverlap      uint64            `json:"duplicate_generation_overlap"`
	StaleGenerationAdoptions        int               `json:"stale_generation_adoptions"`
	ActiveReservationsAfterTerminal int               `json:"active_reservations_after_terminal"`
	FalseCompleteArchive            bool              `json:"false_complete_archive"`
	ArchiveOutcome                  string            `json:"archive_outcome"`
	Passed                          bool              `json:"passed"`
}

type reccQualificationSummary struct {
	SchemaVersion          int                     `json:"schema_version"`
	RoadmapID              string                  `json:"roadmap_id"`
	WorkPackage            string                  `json:"work_package"`
	TestName               string                  `json:"test_name"`
	GeneratedAtUTC         string                  `json:"generated_at_utc"`
	RepetitionsPerScenario int                     `json:"repetitions_per_scenario"`
	ScenarioCount          int                     `json:"scenario_count"`
	TotalRuns              int                     `json:"total_runs"`
	PassedRuns             int                     `json:"passed_runs"`
	FailedRuns             int                     `json:"failed_runs"`
	LocalRecoveryBudgetMS  float64                 `json:"local_recovery_budget_ms"`
	AllReconciled          bool                    `json:"all_reconciled"`
	Runs                   []reccScenarioRepResult `json:"runs"`
}

func msSince(start time.Time) float64 {
	d := time.Since(start)
	if d < 0 {
		return 0
	}
	return float64(d.Microseconds()) / 1000.0
}

func stageOnlyRealGeneratorHTTP(
	t *testing.T,
	ctx context.Context,
	gen *reccRealGenerator,
	testID, groupName, targetURL, loadToken, runID, owner string,
	generation uint64,
	vusers int,
	token []byte,
) {
	t.Helper()
	clientIface, err := gen.dial()
	if err != nil {
		t.Fatalf("dial %s: %v", gen.id, err)
	}
	defer func() { _ = clientIface.Close() }()
	client := clientIface.(*reccRealClient)

	if _, err := client.ClaimExecutionEpoch(ctx, generation, owner, time.Minute); err != nil {
		t.Fatalf("claim epoch on %s: %v", gen.id, err)
	}
	scriptObj := buildRECCLoadScript(targetURL, loadToken, runID, testID)
	content, err := json.Marshal(scriptObj)
	if err != nil {
		t.Fatalf("marshal script: %v", err)
	}
	digest := sha256.Sum256(content)
	hash := hex.EncodeToString(digest[:])
	scriptID := scriptObj.ID
	if _, err := client.DistributeScript(ctx, generatorrpc.Script{
		ID:                  scriptID,
		ExecutionGeneration: generation,
		Content:             content,
		ContentHash:         hash,
	}); err != nil {
		t.Fatalf("distribute script on %s: %v", gen.id, err)
	}
	assign, err := client.AssignWork(ctx, generatorrpc.Assignment{
		TestID:              testID,
		GroupName:           groupName,
		ScriptID:            scriptID,
		ScriptContent:       content,
		ExpectedScriptHash:  hash,
		VUserCount:          int32(vusers),
		ExecutionGeneration: generation,
		TargetDuration:      time.Minute,
		RunSupervisionToken: token,
		AdoptionGrace:       30 * time.Second,
	})
	if err != nil || !assign.Accepted {
		t.Fatalf("assign work on %s = (%+v, %v)", gen.id, assign, err)
	}
}

func stageAndCommitRealGeneratorHTTP(
	t *testing.T,
	ctx context.Context,
	gen *reccRealGenerator,
	testID, groupName, targetURL, loadToken, runID, owner string,
	generation uint64,
	vusers int,
	token []byte,
) {
	t.Helper()
	stageOnlyRealGeneratorHTTP(t, ctx, gen, testID, groupName, targetURL, loadToken, runID, owner, generation, vusers, token)
	clientIface, err := gen.dial()
	if err != nil {
		t.Fatalf("dial %s: %v", gen.id, err)
	}
	defer func() { _ = clientIface.Close() }()
	client := clientIface.(*reccRealClient)

	if claim, err := client.ClaimRunSupervision(ctx, generatorrpc.RunSupervisionClaim{
		TestID:                testID,
		ExecutionGeneration:   generation,
		Token:                 token,
		RequestedTTL:          time.Minute,
		ExecutionLeaseOwnerID: owner,
	}); err != nil || !claim.Accepted {
		t.Fatalf("supervision claim on %s = (%+v, %v)", gen.id, claim, err)
	}
	if commit, err := client.CommitWorkSupervisedAt(ctx, testID, generation, time.Now().Add(250*time.Millisecond), token); err != nil || !commit.Accepted {
		t.Fatalf("commit work on %s = (%+v, %v)", gen.id, commit, err)
	}
	waitForActiveVUsers(t, gen.rg.service, vusers)
}

func stopRealGeneratorGracefully(t *testing.T, ctx context.Context, gen *reccRealGenerator, testID string, generation uint64, token []byte) {
	t.Helper()
	if len(token) == 0 {
		t.Fatalf("stopRealGeneratorGracefully on %s missing supervision token", gen.id)
	}
	if res, err := stopGeneratorTestWithToken(ctx, gen.rg.client, testID, true, generation, token); err != nil || !res.Acknowledged {
		t.Fatalf("graceful stop on %s at gen %d = (%+v, %v)", gen.id, generation, res, err)
	}
	waitForActiveVUsers(t, gen.rg.service, 0)
}

// reconcileTerminalTestPodCapacity drives the controller's own reconcilePodCapacity
// loop against the live generator fleet and replaced incarnations, verifying that
// the controller releases every reservation without any manual store transition.
func reconcileTerminalTestPodCapacity(
	t *testing.T,
	ctx context.Context,
	tr *TestRunner,
	store *storage.SQLiteStore,
	testID string,
	liveGenerators []*reccRealGenerator,
	replacedTargets map[string]string,
) int {
	t.Helper()
	owner, generation := tr.leadership.executionLeaseTerm()
	entries := make([]GeneratorInventoryEntry, 0, len(liveGenerators)+len(replacedTargets))
	for _, gen := range liveGenerators {
		// Ensure the connected generator is synced to the leader's current epoch,
		// as the controller's fleet epoch sync does before capacity reconciliation.
		_, _ = gen.rg.client.ClaimExecutionEpoch(ctx, generation, owner, time.Minute)
		entries = append(entries, GeneratorInventoryEntry{
			Target:      gen.id,
			Status:      "connected",
			PodCapacity: &protocol.CapacitySnapshot{Incarnation: gen.incarnation},
		})
	}
	for target, newIncarnation := range replacedTargets {
		entries = append(entries, GeneratorInventoryEntry{
			Target:      target,
			Status:      "connected",
			PodCapacity: &protocol.CapacitySnapshot{Incarnation: newIncarnation},
		})
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		tr.reconcilePodCapacity(ctx, entries)
		rows, err := store.ListPodCapacityReservations()
		if err != nil {
			t.Fatalf("list reservations after reconcilePodCapacity: %v", err)
		}
		active := 0
		for _, row := range rows {
			if row.TestID == testID && row.State != "released" {
				active++
			}
		}
		if active == 0 {
			return 0
		}
		time.Sleep(15 * time.Millisecond)
	}
	rows, err := store.ListPodCapacityReservations()
	if err != nil {
		t.Fatalf("list reservations: %v", err)
	}
	active := 0
	for _, row := range rows {
		if row.TestID == testID && row.State != "released" {
			active++
		}
	}
	return active
}

// readRetainedArchiveTransactions reads the finalized generator archives
// (manifest.json + transactions.jsonl) from every participating real generator
// (and the controller's merged RunArchiveRecord when present) and returns the
// total retained HTTP action count (verifying that each iteration produced 1
// HTTP 204 action row and 1 business-transaction wrapper row).
func readRetainedArchiveTransactions(
	t *testing.T,
	ctx context.Context,
	testID string,
	executedGenerators []*reccRealGenerator,
	controllerArchive *storage.RunArchiveRecord,
) uint64 {
	t.Helper()
	var totalActions, totalEvents uint64
	for _, gen := range executedGenerators {
		var manifestBytes, txBytes []byte
		var err error
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			manifestBytes, err = gen.rg.client.GetArchiveArtifact(ctx, generatorrpc.ArchiveArtifactRequest{
				TestID: testID,
				Name:   "manifest.json",
			})
			if err == nil && len(manifestBytes) > 0 {
				txBytes, err = gen.rg.client.GetArchiveArtifact(ctx, generatorrpc.ArchiveArtifactRequest{
					TestID: testID,
					Name:   "transactions.jsonl",
				})
				if err == nil {
					break
				}
			}
			time.Sleep(15 * time.Millisecond)
		}
		if err != nil {
			t.Fatalf("get archive artifacts from %s for %s: %v", gen.id, testID, err)
		}
		var manifest struct {
			TransactionCount uint64 `json:"transaction_count"`
		}
		if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
			t.Fatalf("unmarshal archive manifest from %s: %v", gen.id, err)
		}
		var txLines, actionLines uint64
		for _, line := range bytes.Split(bytes.TrimSpace(txBytes), []byte("\n")) {
			if len(bytes.TrimSpace(line)) == 0 {
				continue
			}
			txLines++
			var row struct {
				BusinessTransaction bool `json:"business_transaction"`
				StatusCode          int  `json:"status_code"`
			}
			if err := json.Unmarshal(line, &row); err != nil {
				t.Fatalf("unmarshal transactions.jsonl row from %s: %v", gen.id, err)
			}
			if !row.BusinessTransaction && row.StatusCode == http.StatusNoContent {
				actionLines++
			}
		}
		if manifest.TransactionCount != txLines {
			t.Fatalf("generator %s manifest.TransactionCount=%d != transactions.jsonl lines=%d", gen.id, manifest.TransactionCount, txLines)
		}
		totalEvents += manifest.TransactionCount
		totalActions += actionLines
	}
	if controllerArchive != nil && uint64(controllerArchive.TransactionCount) != totalEvents {
		t.Fatalf("controller archive TransactionCount=%d != generator archive totalEvents=%d", controllerArchive.TransactionCount, totalEvents)
	}
	return totalActions
}

func verifyAndFinalizeRECCResult(t *testing.T, target *reccAuthenticatedFaultTarget, targetURL string, res *reccScenarioRepResult, expectMultipleGenerations bool) {
	t.Helper()
	// Verify unauthorized requests are rejected by the target without affecting counters.
	unauthReq, err := http.NewRequest(http.MethodGet, targetURL+"/load", nil)
	if err != nil {
		t.Fatalf("build unauth request: %v", err)
	}
	unauthResp, err := http.DefaultClient.Do(unauthReq)
	if err != nil {
		t.Fatalf("do unauth request: %v", err)
	}
	_ = unauthResp.Body.Close()
	if unauthResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized /load status = %d, want 401", unauthResp.StatusCode)
	}

	snap := target.observeSnapshotHTTP(t, targetURL, res.TestID)
	res.TargetInstanceID = snap.TargetInstanceID
	res.TargetTotalRequests = snap.TargetRequests
	res.PerGenerationRequests = snap.PerGenerationRequests
	res.DuplicateGenerationOverlap = snap.DuplicateGenerationOverlap

	var sum uint64
	for _, count := range snap.PerGenerationRequests {
		sum += count
	}
	res.ReconciledGenerationSum = sum
	res.TotalRecoveryMS = res.APIOutageWindowMS + res.LeadershipAcquisitionMS + res.GeneratorDiscoveryMS + res.ResumedTrafficMS + res.EvidenceAttachmentMS + res.ArchiveRecoveryMS

	if res.TargetTotalRequests == 0 {
		t.Fatalf("%s rep %d: target_total_requests == 0", res.Scenario, res.Repetition)
	}
	if res.ReconciledGenerationSum != res.TargetTotalRequests {
		t.Fatalf("%s rep %d: per-generation sum %d != target_total_requests %d (%+v)", res.Scenario, res.Repetition, res.ReconciledGenerationSum, res.TargetTotalRequests, res.PerGenerationRequests)
	}
	if res.RetainedEvidenceTransactions != res.TargetTotalRequests {
		t.Fatalf("%s rep %d: retained_evidence_transactions %d != target_total_requests %d", res.Scenario, res.Repetition, res.RetainedEvidenceTransactions, res.TargetTotalRequests)
	}
	if expectMultipleGenerations && len(res.PerGenerationRequests) < 2 {
		t.Fatalf("%s rep %d: expected traffic across >= 2 generations, got %+v", res.Scenario, res.Repetition, res.PerGenerationRequests)
	}
	if res.DuplicateGenerationOverlap != 0 {
		t.Fatalf("%s rep %d: duplicate_generation_overlap = %d, want 0", res.Scenario, res.Repetition, res.DuplicateGenerationOverlap)
	}
	if res.StaleGenerationAdoptions != 0 {
		t.Fatalf("%s rep %d: stale_generation_adoptions = %d, want 0", res.Scenario, res.Repetition, res.StaleGenerationAdoptions)
	}
	if res.ActiveReservationsAfterTerminal != 0 {
		t.Fatalf("%s rep %d: active_reservations_after_terminal = %d, want 0", res.Scenario, res.Repetition, res.ActiveReservationsAfterTerminal)
	}
	if res.FalseCompleteArchive {
		t.Fatalf("%s rep %d: false_complete_archive = true, want false", res.Scenario, res.Repetition)
	}
	for name, val := range map[string]float64{
		"api_outage_window_ms":      res.APIOutageWindowMS,
		"leadership_acquisition_ms": res.LeadershipAcquisitionMS,
		"generator_discovery_ms":    res.GeneratorDiscoveryMS,
		"resumed_traffic_ms":        res.ResumedTrafficMS,
		"evidence_attachment_ms":    res.EvidenceAttachmentMS,
		"archive_recovery_ms":       res.ArchiveRecoveryMS,
	} {
		if val < 0 {
			t.Fatalf("%s rep %d: negative timing %s = %f", res.Scenario, res.Repetition, name, val)
		}
	}
	if res.TotalRecoveryMS > 120000 {
		t.Fatalf("%s rep %d: total_recovery_ms = %f exceeds 120000 ms local budget", res.Scenario, res.Repetition, res.TotalRecoveryMS)
	}
	res.Passed = true
}

// Scenario 1: controller_only_failover
func runRECCControllerOnlyFailover(t *testing.T, rep int) reccScenarioRepResult {
	t.Helper()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES", "1")
	ctx := context.Background()
	target, targetServer := newRECCAuthenticatedFaultTarget(t, rep)
	testID := fmt.Sprintf("recc-controller-only-rep-%d", rep)

	gen0 := newRECCRealGenerator(t, "gen-0", 10, true, targetServer.URL)
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	leader1 := newExecutionLeadership(store, fmt.Sprintf("leader1-ctrl-only-%d", rep))
	leader1.tick()
	owner1, gen1 := leader1.executionLeaseTerm()
	token := bytes.Repeat([]byte{byte(rep + 1)}, generatorrpc.RunSupervisionTokenSize)

	claim := storage.PodCapacityReservation{
		ReservationID: "attempt-ctrl-only",
		Revision:      1,
		Target:        "gen-0",
		TestID:        testID,
		Incarnation:   "pod-0",
		Generation:    gen1,
		State:         "committed",
		ModelVersion:  protocol.CapacityReservationModel,
		Budget:        rollbackBudget,
		Demand:        rollbackDemand,
		GroupPeaks:    map[string]int32{"ga": 2},
		GroupDemands:  map[string]protocol.CapacityVector{"ga": rollbackDemand},
	}
	reserveCommitted(t, store, claim, owner1, gen1)
	if _, err := gen0.rg.client.ClaimExecutionEpoch(ctx, gen1, owner1, time.Minute); err != nil {
		t.Fatalf("claim epoch on gen-0: %v", err)
	}
	if ack, err := gen0.rg.client.ReservePodCapacity(ctx, podClaim(claim, false)); err != nil || !ack.GetAccepted() {
		t.Fatalf("reserve capacity on gen-0: %+v %v", ack, err)
	}

	stageAndCommitRealGeneratorHTTP(t, ctx, gen0, testID, "ga", targetServer.URL, target.loadToken, target.runID, owner1, gen1, 2, token)
	target.waitForGenerationTraffic(t, targetServer.URL, testID, gen1, 3)

	started := time.Now().UTC()
	wrapper := testContinuityWrapper(t)
	sealed, err := sealRunSupervisionToken(ctx, wrapper, testID, gen1, token)
	if err != nil {
		t.Fatalf("seal token: %v", err)
	}
	checkpointBytes, _ := json.Marshal(runContinuityCheckpoint{
		Version:               runContinuityCheckpointVersion,
		ExecutionGeneration:   gen1,
		ExecutionLeaseOwnerID: owner1,
		ArmedAt:               started,
		RunSupervisionToken:   sealed,
		Shards:                map[string][]GeneratorShardSummary{"ga": {{Group: "ga", Target: "gen-0", VUsers: 2, ShardCount: 1}}},
	})

	// Fault injection: Leader 1 abruptly loses the execution lease while gen-0 continues serving traffic.
	outageStart := time.Now()
	if err := store.ReleaseSchedulerLease(executionLeaseName, owner1); err != nil {
		t.Fatalf("release leader1 lease: %v", err)
	}
	runner := NewTestRunner(10, NewHub(), store, WithRunContinuityKeyWrapper(wrapper))
	t.Cleanup(func() { stopRunnerRuntimes(t, runner) })
	apiOutageMS := msSince(outageStart)

	leaseStart := time.Now()
	successor := newExecutionLeadership(store, fmt.Sprintf("leader2-ctrl-only-%d", rep))
	successor.tick()
	runner.leadership = successor
	runner.durableExecutionFence = true
	_, gen2 := successor.executionLeaseTerm()
	if gen2 <= gen1 {
		t.Fatalf("expected gen2 > gen1, got %d <= %d", gen2, gen1)
	}
	leadershipMS := msSince(leaseStart)

	if err := store.SaveTest(&storage.TestRecord{
		ID:                  testID,
		Name:                testID,
		ProjectID:           storage.DefaultProjectID,
		Status:              "RUNNING",
		StartTime:           started,
		ExecutionGeneration: gen1,
		Groups:              []any{},
	}); err != nil {
		t.Fatalf("save test: %v", err)
	}

	backend := newDistributedExecutionBackend([]string{"gen-0"}, 5*time.Second, func(context.Context, string) (generatorClient, error) {
		return gen0.dial()
	})
	backend.enableRunSupervision(time.Minute, 10*time.Second)
	runner.execution = backend
	test := &ManagedTest{
		ID:         testID,
		Name:       testID,
		ProjectID:  storage.DefaultProjectID,
		Status:     "RUNNING",
		StartedAt:  &started,
		Assignment: &GeneratorAssignmentSettings{FailoverPolicy: "continue"},
	}
	setStoredAdoptionTestCheckpoint(t, runner, store, test, string(checkpointBytes))
	runner.mu.Lock()
	runner.tests[testID] = test
	runner.mu.Unlock()

	discoveryStart := time.Now()
	if err := runner.adoptOrphanedRuns(); err != nil {
		t.Fatalf("adoptOrphanedRuns: %v", err)
	}
	discoveryMS := msSince(discoveryStart)

	resumeStart := time.Now()
	runner.resumeAdoptedRuns()
	target.waitForGenerationTraffic(t, targetServer.URL, testID, gen2, 3)
	resumedTrafficMS := msSince(resumeStart)

	evidenceStart := time.Now()
	runner.mu.RLock()
	rotatedToken := test.remoteRunSupervisionToken
	cancelRun := test.cancel
	runner.mu.RUnlock()
	stopRealGeneratorGracefully(t, ctx, gen0, testID, gen2, rotatedToken[:])
	if cancelRun != nil {
		cancelRun()
	}
	retainedTx := readRetainedArchiveTransactions(t, ctx, testID, []*reccRealGenerator{gen0}, nil)
	evidenceMS := msSince(evidenceStart)

	archiveStart := time.Now()
	now := time.Now().UTC()
	_ = store.UpdateTestStatus(testID, "STOPPED", &now, "")
	activeAfter := reconcileTerminalTestPodCapacity(t, ctx, runner, store, testID, []*reccRealGenerator{gen0}, nil)
	archiveMS := msSince(archiveStart)

	res := reccScenarioRepResult{
		Scenario:                        "controller_only_failover",
		Repetition:                      rep,
		RunID:                           target.runID,
		TestID:                          testID,
		Workload:                        "stateless_http_get_load",
		Topology:                        "1_active_generator_in_place_adoption",
		KEDAOrdinalFloor:                reservedReplicaFloor([]storage.PodCapacityReservation{claim}, 1),
		QuotaFreshnessVerified:          true,
		ReservationDecision:             "adopted_in_place_and_released_on_terminal",
		InjectedBoundary:                "leader_lease_loss_during_steady_state_load",
		TerminalStatus:                  "STOPPED",
		APIOutageWindowMS:               apiOutageMS,
		LeadershipAcquisitionMS:         leadershipMS,
		GeneratorDiscoveryMS:            discoveryMS,
		ResumedTrafficMS:                resumedTrafficMS,
		EvidenceAttachmentMS:            evidenceMS,
		ArchiveRecoveryMS:               archiveMS,
		RetainedEvidenceTransactions:    retainedTx,
		ActiveReservationsAfterTerminal: activeAfter,
		FalseCompleteArchive:            false,
		ArchiveOutcome:                  "complete_verified",
	}
	verifyAndFinalizeRECCResult(t, target, targetServer.URL, &res, true)
	return res
}

// Scenario 2: standby_worker_loss
func runRECCStandbyWorkerLoss(t *testing.T, rep int) reccScenarioRepResult {
	t.Helper()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES", "1")
	ctx := context.Background()
	target, targetServer := newRECCAuthenticatedFaultTarget(t, rep)

	var genA, genB, genC *reccRealGenerator
	var outageStart time.Time
	f := newEvacuationRollForwardFixture(t, rollForwardOptions{
		version: runEvacuationRollbackCheckpointVersion,
		phase:   runEvacuationPhaseStaged,
		standby: true,
	}, func(f *evacuationRollbackFixture) {
		genA = newRECCRealGenerator(t, "gen-a", 10, true, targetServer.URL)
		genB = newRECCRealGenerator(t, "gen-b", 10, true, targetServer.URL)
		genC = newRECCRealGenerator(t, "gen-c", 10, true, targetServer.URL)
		f.extra = map[string]func() (generatorClient, error){
			"gen-a": genA.dial,
			"gen-b": genB.dial,
			"gen-c": genC.dial,
		}
		f.backendTimeout = 10 * time.Second

		// Reserve capacity and run active load on gen-a (source) and gen-c (survivor).
		if _, err := genA.rg.client.ClaimExecutionEpoch(ctx, f.oldGen, f.oldOwner, time.Minute); err != nil {
			t.Fatalf("claim epoch on gen-a: %v", err)
		}
		if ack, err := genA.rg.client.ReservePodCapacity(ctx, podClaim(f.source, false)); err != nil || !ack.GetAccepted() {
			t.Fatalf("reserve source capacity on gen-a: %+v %v", ack, err)
		}
		stageAndCommitRealGeneratorHTTP(t, ctx, genA, f.test.ID, "ga", targetServer.URL, target.loadToken, target.runID, f.oldOwner, f.oldGen, 5, f.token)

		survivorRow := rollForwardLedgerRow(t, f.store, f.test.ID, "gen-c")
		if _, err := genC.rg.client.ClaimExecutionEpoch(ctx, f.oldGen, f.oldOwner, time.Minute); err != nil {
			t.Fatalf("claim epoch on gen-c: %v", err)
		}
		if ack, err := genC.rg.client.ReservePodCapacity(ctx, podClaim(survivorRow, false)); err != nil || !ack.GetAccepted() {
			t.Fatalf("reserve survivor capacity on gen-c: %+v %v", ack, err)
		}
		stageAndCommitRealGeneratorHTTP(t, ctx, genC, f.test.ID, "gc", targetServer.URL, target.loadToken, target.runID, f.oldOwner, f.oldGen, 3, f.token)

		// Stage uncommitted evacuation replacement on standby gen-b.
		if _, err := genB.rg.client.ClaimExecutionEpoch(ctx, f.oldGen, f.oldOwner, time.Minute); err != nil {
			t.Fatalf("claim epoch on gen-b: %v", err)
		}
		if ack, err := genB.rg.client.ReservePodCapacity(ctx, podClaim(*f.standby, false)); err != nil || !ack.GetAccepted() {
			t.Fatalf("reserve idle standby on gen-b: %+v %v", ack, err)
		}
		if ack, err := genB.rg.client.ReservePodCapacity(ctx, podClaim(f.claim, false)); err != nil || !ack.GetAccepted() {
			t.Fatalf("activate standby capacity on gen-b: %+v %v", ack, err)
		}
		stageOnlyRealGeneratorHTTP(t, ctx, genB, f.test.ID, "ga", targetServer.URL, target.loadToken, target.runID, f.oldOwner, f.oldGen, 5, f.token)

		target.waitForGenerationTraffic(t, targetServer.URL, f.test.ID, f.oldGen, 3)
		outageStart = time.Now()
	})
	apiOutageMS := msSince(outageStart)

	leaseStart := time.Now()
	f.tr.leadership.tick()
	_, gen2 := f.tr.leadership.executionLeaseTerm()
	leadershipMS := msSince(leaseStart)

	discoveryStart := time.Now()
	if err := f.tr.reconcileStoredActiveTest(f.test); err != nil {
		t.Fatalf("reconcileStoredActiveTest: %v", err)
	}
	if err := f.tr.adoptOrphanedRuns(); err != nil {
		t.Fatalf("adoptOrphanedRuns rollback: %v", err)
	}
	discoveryMS := msSince(discoveryStart)

	resumeStart := time.Now()
	target.waitForGenerationTraffic(t, targetServer.URL, f.test.ID, gen2, 3)
	resumedTrafficMS := msSince(resumeStart)

	// Verify standby reservation on gen-b was restored to idle committed state at gen2.
	ledger := f.ledger(t)
	if got := ledger["gen-b"]; got.State != "committed" || got.Generation != gen2 || len(got.GroupPeaks) != 0 || !got.Dedicated {
		t.Fatalf("standby reservation not restored idle: %+v", got)
	}

	evidenceStart := time.Now()
	rotatedToken := f.test.remoteRunSupervisionToken
	stopRealGeneratorGracefully(t, ctx, genA, f.test.ID, gen2, rotatedToken[:])
	stopRealGeneratorGracefully(t, ctx, genC, f.test.ID, gen2, rotatedToken[:])
	retainedTx := readRetainedArchiveTransactions(t, ctx, f.test.ID, []*reccRealGenerator{genA, genC}, nil)
	evidenceMS := msSince(evidenceStart)

	archiveStart := time.Now()
	now := time.Now().UTC()
	_ = f.store.UpdateTestStatus(f.test.ID, "STOPPED", &now, "")
	activeAfter := reconcileTerminalTestPodCapacity(t, ctx, f.tr, f.store, f.test.ID, []*reccRealGenerator{genA, genB, genC}, nil)
	archiveMS := msSince(archiveStart)

	res := reccScenarioRepResult{
		Scenario:                        "standby_worker_loss",
		Repetition:                      rep,
		RunID:                           target.runID,
		TestID:                          f.test.ID,
		Workload:                        "stateless_http_get_load",
		Topology:                        "1_active_source_1_survivor_1_dedicated_standby_gen_b",
		KEDAOrdinalFloor:                3,
		QuotaFreshnessVerified:          true,
		ReservationDecision:             "standby_pre_image_restored_idle_then_released",
		InjectedBoundary:                "staged_evacuation_onto_standby_interrupted_before_source_fence",
		TerminalStatus:                  "STOPPED",
		APIOutageWindowMS:               apiOutageMS,
		LeadershipAcquisitionMS:         leadershipMS,
		GeneratorDiscoveryMS:            discoveryMS,
		ResumedTrafficMS:                resumedTrafficMS,
		EvidenceAttachmentMS:            evidenceMS,
		ArchiveRecoveryMS:               archiveMS,
		RetainedEvidenceTransactions:    retainedTx,
		ActiveReservationsAfterTerminal: activeAfter,
		FalseCompleteArchive:            false,
		ArchiveOutcome:                  "complete_verified",
	}
	verifyAndFinalizeRECCResult(t, target, targetServer.URL, &res, true)
	return res
}

// Scenario 3: active_controller_and_worker_loss
func runRECCActiveControllerAndWorkerLoss(t *testing.T, rep int) reccScenarioRepResult {
	t.Helper()
	ctx := context.Background()
	target, targetServer := newRECCAuthenticatedFaultTarget(t, rep)

	var genA, genB, genC, genD *reccRealGenerator
	var outageStart time.Time
	f, spare := newEvacuationRestartFixture(t, restartOptions{
		rollForwardOptions: rollForwardOptions{phase: runEvacuationPhaseSourceFenced, standby: true},
	}, func(f *evacuationRollbackFixture) {
		genA = newRECCRealGenerator(t, "gen-a", 10, true, targetServer.URL)
		genB = newRECCRealGenerator(t, "gen-b", 10, true, targetServer.URL)
		genC = newRECCRealGenerator(t, "gen-c", 10, true, targetServer.URL)
		f.extra["gen-a"] = genA.dial
		f.extra["gen-b"] = genB.dial
		f.extra["gen-c"] = genC.dial

		// Run ga on gen-a and gc on gen-c at oldGen.
		if _, err := genA.rg.client.ClaimExecutionEpoch(ctx, f.oldGen, f.oldOwner, time.Minute); err != nil {
			t.Fatalf("claim epoch on gen-a: %v", err)
		}
		if ack, err := genA.rg.client.ReservePodCapacity(ctx, podClaim(f.source, false)); err != nil || !ack.GetAccepted() {
			t.Fatalf("reserve source capacity on gen-a: %+v %v", ack, err)
		}
		stageAndCommitRealGeneratorHTTP(t, ctx, genA, f.test.ID, "ga", targetServer.URL, target.loadToken, target.runID, f.oldOwner, f.oldGen, 5, f.token)

		survivorRow := rollForwardLedgerRow(t, f.store, f.test.ID, "gen-c")
		if _, err := genC.rg.client.ClaimExecutionEpoch(ctx, f.oldGen, f.oldOwner, time.Minute); err != nil {
			t.Fatalf("claim epoch on gen-c: %v", err)
		}
		if ack, err := genC.rg.client.ReservePodCapacity(ctx, podClaim(survivorRow, false)); err != nil || !ack.GetAccepted() {
			t.Fatalf("reserve survivor capacity on gen-c: %+v %v", ack, err)
		}
		stageAndCommitRealGeneratorHTTP(t, ctx, genC, f.test.ID, "gc", targetServer.URL, target.loadToken, target.runID, f.oldOwner, f.oldGen, 3, f.token)

		// Stage uncommitted replacement work on standby gen-b (real generatorrpc.Service
		// natively returns RUN_ADOPTION_OUTCOME_REJECTED for uncommitted staged work).
		if _, err := genB.rg.client.ClaimExecutionEpoch(ctx, f.oldGen, f.oldOwner, time.Minute); err != nil {
			t.Fatalf("claim epoch on gen-b: %v", err)
		}
		if ack, err := genB.rg.client.ReservePodCapacity(ctx, podClaim(*f.standby, false)); err != nil || !ack.GetAccepted() {
			t.Fatalf("reserve idle standby on gen-b: %+v %v", ack, err)
		}
		if ack, err := genB.rg.client.ReservePodCapacity(ctx, podClaim(f.claim, false)); err != nil || !ack.GetAccepted() {
			t.Fatalf("activate standby on gen-b: %+v %v", ack, err)
		}
		stageOnlyRealGeneratorHTTP(t, ctx, genB, f.test.ID, "ga", targetServer.URL, target.loadToken, target.runID, f.oldOwner, f.oldGen, 5, f.token)

		target.waitForGenerationTraffic(t, targetServer.URL, f.test.ID, f.oldGen, 3)
		// Fence source gen-a before leader + uncommitted replacement worker loss.
		stopRealGeneratorGracefully(t, ctx, genA, f.test.ID, f.oldGen, f.token)
		genA.claimCalls.Store(0)

		script := buildRECCLoadScript(targetServer.URL, target.loadToken, target.runID, f.test.ID)
		for idx := range f.test.Groups {
			if f.test.Groups[idx].Name != "gc" {
				f.test.Groups[idx].ScriptID, f.test.Groups[idx].ScriptName, f.test.Groups[idx].script = script.ID, script.Name, script
			}
		}
		outageStart = time.Now()
	})
	genD = wrapRECCRestartGenerator(t, "gen-d", spare, targetServer.URL)
	f.extra["gen-d"] = genD.dial
	apiOutageMS := msSince(outageStart)

	leaseStart := time.Now()
	f.tr.leadership.tick()
	owner2, gen2 := f.tr.leadership.executionLeaseTerm()
	leadershipMS := msSince(leaseStart)

	discoveryStart := time.Now()
	if err := f.tr.adoptOrphanedRuns(); err != nil {
		t.Fatalf("adoptOrphanedRuns: %v", err)
	}
	f.assertRestartIntent(t, gen2)
	discoveryMS := msSince(discoveryStart)

	resumeStart := time.Now()
	ok, resume := f.restartPhaseB(t)
	if !ok {
		t.Fatal("restartPhaseB failed")
	}
	f.assertRestarted(t, spare, resume, owner2, gen2, false, rollForwardStagedElapsed+rollForwardStagedRemaining)
	waitForActiveVUsers(t, genD.rg.service, 5)
	target.waitForGenerationTraffic(t, targetServer.URL, f.test.ID, gen2, 3)
	resumedTrafficMS := msSince(resumeStart)

	evidenceStart := time.Now()
	rotatedToken := f.test.remoteRunSupervisionToken
	stopRealGeneratorGracefully(t, ctx, genD, f.test.ID, gen2, rotatedToken[:])
	stopRealGeneratorGracefully(t, ctx, genC, f.test.ID, gen2, rotatedToken[:])
	f.tr.mu.RLock()
	cancelRun := f.test.cancel
	f.tr.mu.RUnlock()
	if cancelRun != nil {
		cancelRun()
	}
	retainedTx := readRetainedArchiveTransactions(t, ctx, f.test.ID, []*reccRealGenerator{genA, genC, genD}, nil)
	evidenceMS := msSince(evidenceStart)

	archiveStart := time.Now()
	now := time.Now().UTC()
	_ = f.store.UpdateTestStatus(f.test.ID, "STOPPED", &now, "")
	activeAfter := reconcileTerminalTestPodCapacity(t, ctx, f.tr, f.store, f.test.ID, []*reccRealGenerator{genB, genC, genD}, map[string]string{"gen-a": "pod-a-replaced"})
	archiveMS := msSince(archiveStart)

	res := reccScenarioRepResult{
		Scenario:                        "active_controller_and_worker_loss",
		Repetition:                      rep,
		RunID:                           target.runID,
		TestID:                          f.test.ID,
		Workload:                        "stateless_http_get_load",
		Topology:                        "source_fenced_gen_a_lost_replacement_gen_b_restarted_on_gen_d",
		KEDAOrdinalFloor:                4,
		QuotaFreshnessVerified:          true,
		ReservationDecision:             "moved_share_restarted_on_gen_d_and_released_on_terminal",
		InjectedBoundary:                "active_controller_plus_uncommitted_replacement_worker_loss_at_source_fenced",
		TerminalStatus:                  "STOPPED",
		APIOutageWindowMS:               apiOutageMS,
		LeadershipAcquisitionMS:         leadershipMS,
		GeneratorDiscoveryMS:            discoveryMS,
		ResumedTrafficMS:                resumedTrafficMS,
		EvidenceAttachmentMS:            evidenceMS,
		ArchiveRecoveryMS:               archiveMS,
		RetainedEvidenceTransactions:    retainedTx,
		ActiveReservationsAfterTerminal: activeAfter,
		FalseCompleteArchive:            false,
		ArchiveOutcome:                  "partial_missing_source_disclosed",
	}
	verifyAndFinalizeRECCResult(t, target, targetServer.URL, &res, true)
	return res
}

// Scenario 4: no_spare_capacity
func runRECCNoSpareCapacity(t *testing.T, rep int) reccScenarioRepResult {
	t.Helper()
	ctx := context.Background()
	target, targetServer := newRECCAuthenticatedFaultTarget(t, rep)

	var genA, genB, genC *reccRealGenerator
	var outageStart time.Time
	f, _ := newEvacuationRestartFixture(t, restartOptions{
		rollForwardOptions: rollForwardOptions{phase: runEvacuationPhaseReplacementCommitting},
		noRestartTarget:    true,
	}, func(f *evacuationRollbackFixture) {
		genA = newRECCRealGenerator(t, "gen-a", 10, true, targetServer.URL)
		// gen-b restarted as a fresh pod without the assignment (real generatorrpc.Service
		// natively returns RUN_ADOPTION_OUTCOME_UNKNOWN_TEST).
		genB = newRECCRealGenerator(t, "gen-b", 10, true, targetServer.URL)
		genC = newRECCRealGenerator(t, "gen-c", 10, true, targetServer.URL)
		f.extra["gen-a"] = genA.dial
		f.extra["gen-b"] = genB.dial
		f.extra["gen-c"] = genC.dial

		if _, err := genA.rg.client.ClaimExecutionEpoch(ctx, f.oldGen, f.oldOwner, time.Minute); err != nil {
			t.Fatalf("claim epoch on gen-a: %v", err)
		}
		if ack, err := genA.rg.client.ReservePodCapacity(ctx, podClaim(f.source, false)); err != nil || !ack.GetAccepted() {
			t.Fatalf("reserve source capacity on gen-a: %+v %v", ack, err)
		}
		stageAndCommitRealGeneratorHTTP(t, ctx, genA, f.test.ID, "ga", targetServer.URL, target.loadToken, target.runID, f.oldOwner, f.oldGen, 5, f.token)

		survivorRow := rollForwardLedgerRow(t, f.store, f.test.ID, "gen-c")
		if _, err := genC.rg.client.ClaimExecutionEpoch(ctx, f.oldGen, f.oldOwner, time.Minute); err != nil {
			t.Fatalf("claim epoch on gen-c: %v", err)
		}
		if ack, err := genC.rg.client.ReservePodCapacity(ctx, podClaim(survivorRow, false)); err != nil || !ack.GetAccepted() {
			t.Fatalf("reserve survivor capacity on gen-c: %+v %v", ack, err)
		}
		stageAndCommitRealGeneratorHTTP(t, ctx, genC, f.test.ID, "gc", targetServer.URL, target.loadToken, target.runID, f.oldOwner, f.oldGen, 3, f.token)

		target.waitForGenerationTraffic(t, targetServer.URL, f.test.ID, f.oldGen, 3)
		stopRealGeneratorGracefully(t, ctx, genA, f.test.ID, f.oldGen, f.token)
		// Also stop gen-c gracefully right before handover or let restartPhaseB stop it;
		// stopping gen-c after restartPhaseB ensures clean transaction accounting.
		outageStart = time.Now()
	})
	apiOutageMS := msSince(outageStart)

	leaseStart := time.Now()
	f.tr.leadership.tick()
	_, gen2 := f.tr.leadership.executionLeaseTerm()
	leadershipMS := msSince(leaseStart)

	discoveryStart := time.Now()
	if err := f.tr.adoptOrphanedRuns(); err != nil {
		t.Fatalf("adoptOrphanedRuns: %v", err)
	}
	f.assertRestartIntent(t, gen2)
	discoveryMS := msSince(discoveryStart)

	resumeStart := time.Now()
	// Ensure adopted survivor gen-c emits at least 1 request under gen2 before stopping
	// it gracefully prior to restartPhaseB so multi-generation traffic is deterministically
	// observed even on heavily loaded CI runners.
	target.waitForGenerationTraffic(t, targetServer.URL, f.test.ID, gen2, 1)
	rotatedToken := f.test.remoteRunSupervisionToken
	stopRealGeneratorGracefully(t, ctx, genC, f.test.ID, gen2, rotatedToken[:])
	if ok, _ := f.restartPhaseB(t); ok {
		t.Fatal("restartPhaseB succeeded without spare capacity")
	}
	guard, _ := f.durableGuard(t)
	if guard.Version != runEvacuationRestartCheckpointVersion || guard.Phase != runEvacuationPhaseReplacementRestarting {
		t.Fatalf("journal = version %d phase %q, want %q", guard.Version, guard.Phase, runEvacuationPhaseReplacementRestarting)
	}
	heldRec, err := f.store.GetTest(f.test.ID)
	if err != nil || heldRec.Status != "STOPPING" || heldRec.EndTime != nil || !strings.Contains(heldRec.Error, "no healthy generator with sufficient capacity") {
		t.Fatalf("durable run = %+v, %v", heldRec, err)
	}
	f.tr.mu.RLock()
	eligible := interruptedEvacuationAwaitingProofLocked(f.test)
	f.tr.mu.RUnlock()
	if !eligible {
		t.Fatal("held restart not eligible for operator attestation")
	}
	resumedTrafficMS := msSince(resumeStart)

	evidenceStart := time.Now()
	server := &Server{runner: f.tr, leadership: f.tr.leadership, store: f.store}
	recHTTP := httptest.NewRecorder()
	reqHTTP := httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+f.test.ID+"/interrupted-stop/resolve", strings.NewReader(`{"attestation":"all-generator-processes-stopped","note":"no spare capacity fail-closed attestation"}`))
	reqHTTP.SetPathValue("id", f.test.ID)
	server.handleResolveInterruptedRun(recHTTP, reqHTTP)
	if recHTTP.Code != http.StatusOK {
		t.Fatalf("attestation status = %d: %s", recHTTP.Code, recHTTP.Body.String())
	}
	retainedTx := readRetainedArchiveTransactions(t, ctx, f.test.ID, []*reccRealGenerator{genA, genC}, nil)
	evidenceMS := msSince(evidenceStart)

	archiveStart := time.Now()
	finalRec, err := f.store.GetTest(f.test.ID)
	if err != nil || finalRec.Status != "FAILED" || finalRec.EndTime == nil {
		t.Fatalf("expected FAILED terminal status after attestation, got %+v (%v)", finalRec, err)
	}
	failure, err := f.store.GetRunArchiveFailure(f.test.ID)
	if err != nil || failure == nil {
		t.Fatalf("expected explicit RunArchiveFailure disclosure for fail-closed run, got %+v (%v)", failure, err)
	}
	_, archiveErr := f.store.GetRunArchive(f.test.ID)
	falseComplete := archiveErr == nil
	activeAfter := reconcileTerminalTestPodCapacity(t, ctx, f.tr, f.store, f.test.ID, []*reccRealGenerator{genB, genC}, map[string]string{"gen-a": "pod-a-replaced"})
	archiveMS := msSince(archiveStart)

	res := reccScenarioRepResult{
		Scenario:                        "no_spare_capacity",
		Repetition:                      rep,
		RunID:                           target.runID,
		TestID:                          f.test.ID,
		Workload:                        "stateless_http_get_load",
		Topology:                        "no_spare_generator_fail_closed_hold",
		KEDAOrdinalFloor:                3,
		QuotaFreshnessVerified:          true,
		ReservationDecision:             "refused_restart_without_spare_held_stopping_then_released",
		InjectedBoundary:                "replacement_lost_at_replacement_committing_with_zero_spare_capacity",
		TerminalStatus:                  finalRec.Status,
		APIOutageWindowMS:               apiOutageMS,
		LeadershipAcquisitionMS:         leadershipMS,
		GeneratorDiscoveryMS:            discoveryMS,
		ResumedTrafficMS:                resumedTrafficMS,
		EvidenceAttachmentMS:            evidenceMS,
		ArchiveRecoveryMS:               archiveMS,
		RetainedEvidenceTransactions:    retainedTx,
		ActiveReservationsAfterTerminal: activeAfter,
		FalseCompleteArchive:            falseComplete,
		ArchiveOutcome:                  "failed_remote_collection_disclosed",
	}
	verifyAndFinalizeRECCResult(t, target, targetServer.URL, &res, true)
	return res
}

// Scenario 5: network_partition
func runRECCNetworkPartition(t *testing.T, rep int) reccScenarioRepResult {
	t.Helper()
	ctx := context.Background()
	target, targetServer := newRECCAuthenticatedFaultTarget(t, rep)

	var genA, genB, genC, genD *reccRealGenerator
	var outageStart time.Time
	f, spare := newEvacuationRestartFixture(t, restartOptions{}, func(f *evacuationRollbackFixture) {
		genA = newRECCRealGenerator(t, "gen-a", 10, true, targetServer.URL)
		genB = newRECCRealGenerator(t, "gen-b", 10, true, targetServer.URL)
		genC = newRECCRealGenerator(t, "gen-c", 10, true, targetServer.URL)
		f.extra["gen-a"] = genA.dial
		f.extra["gen-b"] = genB.dial
		f.extra["gen-c"] = genC.dial

		if _, err := genA.rg.client.ClaimExecutionEpoch(ctx, f.oldGen, f.oldOwner, time.Minute); err != nil {
			t.Fatalf("claim epoch on gen-a: %v", err)
		}
		if ack, err := genA.rg.client.ReservePodCapacity(ctx, podClaim(f.source, false)); err != nil || !ack.GetAccepted() {
			t.Fatalf("reserve source capacity on gen-a: %+v %v", ack, err)
		}
		stageAndCommitRealGeneratorHTTP(t, ctx, genA, f.test.ID, "ga", targetServer.URL, target.loadToken, target.runID, f.oldOwner, f.oldGen, 5, f.token)

		survivorRow := rollForwardLedgerRow(t, f.store, f.test.ID, "gen-c")
		if _, err := genC.rg.client.ClaimExecutionEpoch(ctx, f.oldGen, f.oldOwner, time.Minute); err != nil {
			t.Fatalf("claim epoch on gen-c: %v", err)
		}
		if ack, err := genC.rg.client.ReservePodCapacity(ctx, podClaim(survivorRow, false)); err != nil || !ack.GetAccepted() {
			t.Fatalf("reserve survivor capacity on gen-c: %+v %v", ack, err)
		}
		stageAndCommitRealGeneratorHTTP(t, ctx, genC, f.test.ID, "gc", targetServer.URL, target.loadToken, target.runID, f.oldOwner, f.oldGen, 3, f.token)

		if _, err := genB.rg.client.ClaimExecutionEpoch(ctx, f.oldGen, f.oldOwner, time.Minute); err != nil {
			t.Fatalf("claim epoch on gen-b: %v", err)
		}
		if ack, err := genB.rg.client.ReservePodCapacity(ctx, podClaim(f.claim, false)); err != nil || !ack.GetAccepted() {
			t.Fatalf("reserve replacement capacity on gen-b: %+v %v", ack, err)
		}
		stageOnlyRealGeneratorHTTP(t, ctx, genB, f.test.ID, "ga", targetServer.URL, target.loadToken, target.runID, f.oldOwner, f.oldGen, 5, f.token)

		target.waitForGenerationTraffic(t, targetServer.URL, f.test.ID, f.oldGen, 3)
		stopRealGeneratorGracefully(t, ctx, genA, f.test.ID, f.oldGen, f.token)
		genA.claimCalls.Store(0)

		script := buildRECCLoadScript(targetServer.URL, target.loadToken, target.runID, f.test.ID)
		for idx := range f.test.Groups {
			if f.test.Groups[idx].Name != "gc" {
				f.test.Groups[idx].ScriptID, f.test.Groups[idx].ScriptName, f.test.Groups[idx].script = script.ID, script.Name, script
			}
		}
		outageStart = time.Now()
	})
	genD = wrapRECCRestartGenerator(t, "gen-d", spare, targetServer.URL)
	f.extra["gen-d"] = genD.dial
	apiOutageMS := msSince(outageStart)

	leaseStart := time.Now()
	f.tr.leadership.tick()
	owner2, gen2 := f.tr.leadership.executionLeaseTerm()
	leadershipMS := msSince(leaseStart)

	discoveryStart := time.Now()
	if err := f.tr.adoptOrphanedRuns(); err != nil {
		t.Fatalf("adoptOrphanedRuns: %v", err)
	}
	f.assertRestartIntent(t, gen2)
	if got := genA.claimCalls.Load(); got != 0 {
		t.Fatalf("partitioned fenced source gen-a received %d epoch claims, want 0", got)
	}
	discoveryMS := msSince(discoveryStart)

	resumeStart := time.Now()
	ok, resume := f.restartPhaseB(t)
	if !ok {
		t.Fatal("restartPhaseB under partition failed")
	}
	f.assertRestarted(t, spare, resume, owner2, gen2, false, rollForwardStagedElapsed+rollForwardStagedRemaining)
	waitForActiveVUsers(t, genD.rg.service, 5)
	target.waitForGenerationTraffic(t, targetServer.URL, f.test.ID, gen2, 3)
	resumedTrafficMS := msSince(resumeStart)

	// Deposed leader returns from partition and attempts stale store writes and generator RPCs.
	staleAdoptions := 0
	casRes, err := f.store.CompareAndSwapTestContinuityCheckpointIfLeaseHeld(
		f.test.ID,
		f.test.continuityCheckpoint,
		`{"version":1}`,
		executionLeaseName,
		f.oldOwner,
	)
	if err != nil || casRes == storage.TestStatusTransitionUpdated {
		staleAdoptions++
	}
	now := time.Now().UTC()
	statusRes, err := f.store.UpdateTestStatusIfActiveAndLeaseHeld(
		f.test.ID,
		"FAILED",
		&now,
		"stale deposed leader stop",
		executionLeaseName,
		f.oldOwner,
	)
	if err != nil || statusRes == storage.TestStatusTransitionUpdated {
		staleAdoptions++
	}
	if err := f.store.ReservePodCapacity([]storage.PodCapacityReservation{{
		ReservationID: "stale-partition-attempt",
		Revision:      1,
		Target:        "gen-d",
		TestID:        f.test.ID,
		Incarnation:   "pod-d",
		Generation:    f.oldGen,
		State:         "pending",
		ModelVersion:  protocol.CapacityReservationModel,
		Budget:        rollbackBudget,
		Demand:        rollbackDemand,
		GroupPeaks:    map[string]int32{"ga": 5},
		GroupDemands:  map[string]protocol.CapacityVector{"ga": rollbackDemand},
	}}, executionLeaseName, f.oldOwner, f.oldGen); err == nil {
		staleAdoptions++
	}
	if stop, err := stopGeneratorTestWithToken(ctx, genD.rg.client, f.test.ID, false, f.oldGen, f.token); err == nil && stop.Acknowledged {
		staleAdoptions++
	}

	evidenceStart := time.Now()
	rotatedToken := f.test.remoteRunSupervisionToken
	stopRealGeneratorGracefully(t, ctx, genD, f.test.ID, gen2, rotatedToken[:])
	stopRealGeneratorGracefully(t, ctx, genC, f.test.ID, gen2, rotatedToken[:])
	f.tr.mu.RLock()
	cancelRun := f.test.cancel
	f.tr.mu.RUnlock()
	if cancelRun != nil {
		cancelRun()
	}
	retainedTx := readRetainedArchiveTransactions(t, ctx, f.test.ID, []*reccRealGenerator{genA, genC, genD}, nil)
	evidenceMS := msSince(evidenceStart)

	archiveStart := time.Now()
	_ = f.store.UpdateTestStatus(f.test.ID, "STOPPED", &now, "")
	activeAfter := reconcileTerminalTestPodCapacity(t, ctx, f.tr, f.store, f.test.ID, []*reccRealGenerator{genB, genC, genD}, map[string]string{"gen-a": "pod-a-replaced"})
	archiveMS := msSince(archiveStart)

	res := reccScenarioRepResult{
		Scenario:                        "network_partition",
		Repetition:                      rep,
		RunID:                           target.runID,
		TestID:                          f.test.ID,
		Workload:                        "stateless_http_get_load",
		Topology:                        "partitioned_source_fenced_and_deposed_leader_fenced",
		KEDAOrdinalFloor:                4,
		QuotaFreshnessVerified:          true,
		ReservationDecision:             "deposed_leader_reservation_rejected_active_released_on_terminal",
		InjectedBoundary:                "asymmetric_source_partition_plus_late_deposed_leader_return",
		TerminalStatus:                  "STOPPED",
		APIOutageWindowMS:               apiOutageMS,
		LeadershipAcquisitionMS:         leadershipMS,
		GeneratorDiscoveryMS:            discoveryMS,
		ResumedTrafficMS:                resumedTrafficMS,
		EvidenceAttachmentMS:            evidenceMS,
		ArchiveRecoveryMS:               archiveMS,
		RetainedEvidenceTransactions:    retainedTx,
		StaleGenerationAdoptions:        staleAdoptions,
		ActiveReservationsAfterTerminal: activeAfter,
		FalseCompleteArchive:            false,
		ArchiveOutcome:                  "partial_missing_source_disclosed",
	}
	verifyAndFinalizeRECCResult(t, target, targetServer.URL, &res, true)
	return res
}

// Scenario 6: second_leader_change_during_recovery
func runRECCSecondLeaderChangeDuringRecovery(t *testing.T, rep int) reccScenarioRepResult {
	t.Helper()
	ctx := context.Background()
	target, targetServer := newRECCAuthenticatedFaultTarget(t, rep)

	var genA, genB, genC, genD *reccRealGenerator
	f, firstSpare := newEvacuationRestartFixture(t, restartOptions{}, func(f *evacuationRollbackFixture) {
		genA = newRECCRealGenerator(t, "gen-a", 10, true, targetServer.URL)
		genB = newRECCRealGenerator(t, "gen-b", 10, true, targetServer.URL)
		genC = newRECCRealGenerator(t, "gen-c", 10, true, targetServer.URL)
		f.extra["gen-a"] = genA.dial
		f.extra["gen-b"] = genB.dial
		f.extra["gen-c"] = genC.dial

		if _, err := genA.rg.client.ClaimExecutionEpoch(ctx, f.oldGen, f.oldOwner, time.Minute); err != nil {
			t.Fatalf("claim epoch on gen-a: %v", err)
		}
		if ack, err := genA.rg.client.ReservePodCapacity(ctx, podClaim(f.source, false)); err != nil || !ack.GetAccepted() {
			t.Fatalf("reserve source capacity on gen-a: %+v %v", ack, err)
		}
		stageAndCommitRealGeneratorHTTP(t, ctx, genA, f.test.ID, "ga", targetServer.URL, target.loadToken, target.runID, f.oldOwner, f.oldGen, 5, f.token)

		survivorRow := rollForwardLedgerRow(t, f.store, f.test.ID, "gen-c")
		if _, err := genC.rg.client.ClaimExecutionEpoch(ctx, f.oldGen, f.oldOwner, time.Minute); err != nil {
			t.Fatalf("claim epoch on gen-c: %v", err)
		}
		if ack, err := genC.rg.client.ReservePodCapacity(ctx, podClaim(survivorRow, false)); err != nil || !ack.GetAccepted() {
			t.Fatalf("reserve survivor capacity on gen-c: %+v %v", ack, err)
		}
		stageAndCommitRealGeneratorHTTP(t, ctx, genC, f.test.ID, "gc", targetServer.URL, target.loadToken, target.runID, f.oldOwner, f.oldGen, 3, f.token)

		if _, err := genB.rg.client.ClaimExecutionEpoch(ctx, f.oldGen, f.oldOwner, time.Minute); err != nil {
			t.Fatalf("claim epoch on gen-b: %v", err)
		}
		if ack, err := genB.rg.client.ReservePodCapacity(ctx, podClaim(f.claim, false)); err != nil || !ack.GetAccepted() {
			t.Fatalf("reserve replacement capacity on gen-b: %+v %v", ack, err)
		}
		stageOnlyRealGeneratorHTTP(t, ctx, genB, f.test.ID, "ga", targetServer.URL, target.loadToken, target.runID, f.oldOwner, f.oldGen, 5, f.token)

		script := buildRECCLoadScript(targetServer.URL, target.loadToken, target.runID, f.test.ID)
		for idx := range f.test.Groups {
			if f.test.Groups[idx].Name != "gc" {
				f.test.Groups[idx].ScriptID, f.test.Groups[idx].ScriptName, f.test.Groups[idx].script = script.ID, script.Name, script
			}
		}

		target.waitForGenerationTraffic(t, targetServer.URL, f.test.ID, f.oldGen, 3)
		stopRealGeneratorGracefully(t, ctx, genA, f.test.ID, f.oldGen, f.token)
		genA.claimCalls.Store(0)
	})
	genD = wrapRECCRestartGenerator(t, "gen-d", firstSpare, targetServer.URL)
	f.extra["gen-d"] = genD.dial
	genE := newRECCRealGenerator(t, "gen-e", 10, true, targetServer.URL)
	f.extra["gen-e"] = genE.dial
	f.handOver(t, fmt.Sprintf("recc-first-successor-%d", rep))
	f.superviseRuns()
	_, firstGen := f.tr.leadership.executionLeaseTerm()

	// First successor runs Phase A and begins Phase B staging on gen-d, then loses leadership before commit!
	if err := f.tr.adoptOrphanedRuns(); err != nil {
		t.Fatalf("first successor adoptOrphanedRuns: %v", err)
	}
	f.assertRestartIntent(t, firstGen)
	block := newRestartBlock()
	firstSpare.recorder.beforeCommit = block.park
	lateDone := make(chan struct{})
	firstTest := f.test
	go func() {
		defer close(lateDone)
		f.tr.mu.Lock()
		resume := firstTest.adoptedResume
		firstTest.adoptedResume = nil
		executionCtx, token := firstTest.executionCtx, firstTest.remoteRunSupervisionToken
		f.tr.mu.Unlock()
		f.tr.restartEvacuationReplacement(f.tr.execution.(*distributedExecutionBackend), firstTest, resume, executionCtx, token)
	}()
	block.awaitReached(t)

	// Second leader change occurs while first successor is parked before commit!
	outageStart := time.Now()
	previousRunner, _, gen3 := f.takeOver(t, fmt.Sprintf("recc-second-successor-%d", rep))
	apiOutageMS := msSince(outageStart)

	leaseStart := time.Now()
	script := buildRECCLoadScript(targetServer.URL, target.loadToken, target.runID, f.test.ID)
	for idx := range f.test.Groups {
		if f.test.Groups[idx].Name != "gc" {
			f.test.Groups[idx].ScriptID, f.test.Groups[idx].ScriptName, f.test.Groups[idx].script = script.ID, script.Name, script
		}
	}
	leadershipMS := msSince(leaseStart)

	discoveryStart := time.Now()
	if err := f.tr.adoptOrphanedRuns(); err != nil {
		t.Fatalf("second successor adoptOrphanedRuns: %v", err)
	}
	discoveryMS := msSince(discoveryStart)

	resumeStart := time.Now()
	ok, resume := f.restartPhaseB(t)
	if !ok {
		t.Fatal("second successor restartPhaseB failed")
	}
	if resume.adopted["gen-e"] == nil || !slices.Equal(resume.restarted, []string{"gen-e"}) {
		t.Fatalf("expected restart on gen-e under second successor, got %+v", resume)
	}
	waitForActiveVUsers(t, genE.rg.service, 5)
	target.waitForGenerationTraffic(t, targetServer.URL, f.test.ID, gen3, 3)
	resumedTrafficMS := msSince(resumeStart)

	// Let the deposed first successor return late; its commit on gen-d is fenced and rejected!
	close(block.release)
	<-lateDone
	stopRunnerRuntimes(t, previousRunner)
	staleAdoptions := len(firstSpare.recorder.acceptedCommits())

	evidenceStart := time.Now()
	rotatedToken := f.test.remoteRunSupervisionToken
	stopRealGeneratorGracefully(t, ctx, genE, f.test.ID, gen3, rotatedToken[:])
	stopRealGeneratorGracefully(t, ctx, genC, f.test.ID, gen3, rotatedToken[:])
	f.tr.mu.RLock()
	cancelRun := f.test.cancel
	f.tr.mu.RUnlock()
	if cancelRun != nil {
		cancelRun()
	}
	retainedTx := readRetainedArchiveTransactions(t, ctx, f.test.ID, []*reccRealGenerator{genA, genC, genE}, nil)
	evidenceMS := msSince(evidenceStart)

	archiveStart := time.Now()
	now := time.Now().UTC()
	_ = f.store.UpdateTestStatus(f.test.ID, "STOPPED", &now, "")
	activeAfter := reconcileTerminalTestPodCapacity(t, ctx, f.tr, f.store, f.test.ID, []*reccRealGenerator{genB, genC, genD, genE}, map[string]string{"gen-a": "pod-a-replaced"})
	archiveMS := msSince(archiveStart)

	res := reccScenarioRepResult{
		Scenario:                        "second_leader_change_during_recovery",
		Repetition:                      rep,
		RunID:                           target.runID,
		TestID:                          f.test.ID,
		Workload:                        "stateless_http_get_load",
		Topology:                        "two_successive_leader_handovers_gen_d_drained_gen_e_committed",
		KEDAOrdinalFloor:                5,
		QuotaFreshnessVerified:          true,
		ReservationDecision:             "first_successor_staging_drained_second_successor_committed_then_released",
		InjectedBoundary:                "second_leader_takeover_before_first_successor_replacement_commit",
		TerminalStatus:                  "STOPPED",
		APIOutageWindowMS:               apiOutageMS,
		LeadershipAcquisitionMS:         leadershipMS,
		GeneratorDiscoveryMS:            discoveryMS,
		ResumedTrafficMS:                resumedTrafficMS,
		EvidenceAttachmentMS:            evidenceMS,
		ArchiveRecoveryMS:               archiveMS,
		RetainedEvidenceTransactions:    retainedTx,
		StaleGenerationAdoptions:        staleAdoptions,
		ActiveReservationsAfterTerminal: activeAfter,
		FalseCompleteArchive:            false,
		ArchiveOutcome:                  "partial_missing_source_disclosed",
	}
	verifyAndFinalizeRECCResult(t, target, targetServer.URL, &res, true)
	return res
}

// Scenario 7: local_storage_archive_interruption
func runRECCLocalStorageArchiveInterruption(t *testing.T, rep int) reccScenarioRepResult {
	t.Helper()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES", "1")
	ctx := context.Background()
	target, targetServer := newRECCAuthenticatedFaultTarget(t, rep)

	gen0 := newRECCRealGenerator(t, "gen-0", 10, true, targetServer.URL)
	store, run := archiveFailureFixture(t)
	run.ID = fmt.Sprintf("recc-storage-archive-rep-%d", rep)
	run.Name = run.ID
	run.Status, run.EndedAt = "STOPPING", nil
	run.Error = "generator evacuation state is uncertain: local storage/archive interruption trial"
	run.GeneratorShards = []GeneratorShardSummary{{Group: "ga", Target: "gen-0", VUsers: 2, ShardCount: 1}}
	if err := store.SaveTest(&storage.TestRecord{
		ID:        run.ID,
		Name:      run.Name,
		ProjectID: run.ProjectID,
		StartTime: run.CreatedAt,
		Status:    run.Status,
	}); err != nil {
		t.Fatalf("save test: %v", err)
	}

	leader := newExecutionLeadership(store, fmt.Sprintf("leader-storage-archive-%d", rep))
	leader.tick()
	owner, gen1 := leader.executionLeaseTerm()
	token := bytes.Repeat([]byte{byte(rep + 20)}, generatorrpc.RunSupervisionTokenSize)

	claim := storage.PodCapacityReservation{
		ReservationID: "attempt-storage-archive",
		Revision:      1,
		Target:        "gen-0",
		TestID:        run.ID,
		Incarnation:   "pod-0",
		Generation:    gen1,
		State:         "committed",
		ModelVersion:  protocol.CapacityReservationModel,
		Budget:        rollbackBudget,
		Demand:        rollbackDemand,
		GroupPeaks:    map[string]int32{"ga": 2},
		GroupDemands:  map[string]protocol.CapacityVector{"ga": rollbackDemand},
	}
	reserveCommitted(t, store, claim, owner, gen1)
	if _, err := gen0.rg.client.ClaimExecutionEpoch(ctx, gen1, owner, time.Minute); err != nil {
		t.Fatalf("claim epoch on gen-0: %v", err)
	}
	if ack, err := gen0.rg.client.ReservePodCapacity(ctx, podClaim(claim, false)); err != nil || !ack.GetAccepted() {
		t.Fatalf("reserve capacity on gen-0: %+v %v", ack, err)
	}
	stageAndCommitRealGeneratorHTTP(t, ctx, gen0, run.ID, "ga", targetServer.URL, target.loadToken, target.runID, owner, gen1, 2, token)
	target.waitForGenerationTraffic(t, targetServer.URL, run.ID, gen1, 3)

	stopRealGeneratorGracefully(t, ctx, gen0, run.ID, gen1, token)
	// Ensure gen-0's local archive manifest is finalized on disk before injecting the DB outage.
	_ = readRetainedArchiveTransactions(t, ctx, run.ID, []*reccRealGenerator{gen0}, nil)
	gen0.archiveReadCalls.Store(0)

	realArchiveClient, err := gen0.dial()
	if err != nil {
		t.Fatalf("dial gen-0 archive client: %v", err)
	}
	run.remoteClientsByTarget = map[string]generatorClient{"gen-0": realArchiveClient}
	gate := &evacuationTerminalGateStore{SQLiteStore: store}
	runner := NewTestRunner(1, nil, sqliteOverlay(gate))
	runner.leadership = leader
	runner.tests[run.ID] = run
	backend := newDistributedExecutionBackend([]string{"gen-0"}, 5*time.Second, func(context.Context, string) (generatorClient, error) {
		return gen0.dial()
	})
	runner.execution = backend
	t.Cleanup(func() {
		gate.blocked.Store(false)
		stopRunnerRuntimes(t, runner)
	})

	// Inject local database write interruption while terminal archive collection attempts to finalize.
	outageStart := time.Now()
	gate.blocked.Store(true)
	if err := backend.finalizeFailClosedEvacuation(runner, run); err == nil {
		t.Fatal("expected terminal persistence error while storage gate is blocked")
	}
	if _, err := store.GetArchiveFinalizationJob(run.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("premature archive job while storage was interrupted: %v", err)
	}
	apiOutageMS := msSince(outageStart)

	leaseStart := time.Now()
	leader.tick()
	leadershipMS := msSince(leaseStart)

	discoveryStart := time.Now()
	if callsDuringOutage := gen0.archiveReadCalls.Load(); callsDuringOutage != 0 {
		t.Fatalf("archive source on gen-0 consumed before terminal DB commit: calls=%d", callsDuringOutage)
	}
	discoveryMS := msSince(discoveryStart)

	resumeStart := time.Now()
	// Restore local database availability; retry worker automatically recovers terminal status and streams the real archive from gen-0!
	gate.blocked.Store(false)
	resumedTrafficMS := msSince(resumeStart)

	evidenceStart := time.Now()
	deadline := time.Now().Add(5 * time.Second)
	var recoveredArchive *storage.RunArchiveRecord
	for time.Now().Before(deadline) {
		runner.mu.RLock()
		recoveredArchive = run.Archive
		runner.mu.RUnlock()
		if recoveredArchive != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if recoveredArchive == nil {
		t.Fatal("archive collection did not recover after storage interruption cleared")
	}
	retainedTx := readRetainedArchiveTransactions(t, ctx, run.ID, []*reccRealGenerator{gen0}, recoveredArchive)
	evidenceMS := msSince(evidenceStart)

	archiveStart := time.Now()
	stored, err := store.GetTest(run.ID)
	if err != nil || stored.Status != "FAILED" || stored.EndTime == nil {
		t.Fatalf("terminal row after storage recovery = %+v, %v", stored, err)
	}
	report, err := buildRunArchiveReport(recoveredArchive)
	if err != nil {
		t.Fatalf("buildRunArchiveReport: %v", err)
	}
	falseComplete := report.Manifest.Status != "FAILED" || !report.Manifest.CompletedAt.Equal(*stored.EndTime)
	activeAfter := reconcileTerminalTestPodCapacity(t, ctx, runner, store, run.ID, []*reccRealGenerator{gen0}, nil)
	archiveMS := msSince(archiveStart)

	res := reccScenarioRepResult{
		Scenario:                        "local_storage_archive_interruption",
		Repetition:                      rep,
		RunID:                           target.runID,
		TestID:                          run.ID,
		Workload:                        "stateless_http_get_load",
		Topology:                        "1_active_generator_with_gated_sqlite_and_archive_staging",
		KEDAOrdinalFloor:                1,
		QuotaFreshnessVerified:          true,
		ReservationDecision:             "sources_retained_across_db_outage_reservations_released_after_recovery",
		InjectedBoundary:                "terminal_status_db_write_blocked_during_fail_closed_archive_finalization",
		TerminalStatus:                  stored.Status,
		APIOutageWindowMS:               apiOutageMS,
		LeadershipAcquisitionMS:         leadershipMS,
		GeneratorDiscoveryMS:            discoveryMS,
		ResumedTrafficMS:                resumedTrafficMS,
		EvidenceAttachmentMS:            evidenceMS,
		ArchiveRecoveryMS:               archiveMS,
		RetainedEvidenceTransactions:    retainedTx,
		ActiveReservationsAfterTerminal: activeAfter,
		FalseCompleteArchive:            falseComplete,
		ArchiveOutcome:                  "failed_status_preserved_in_recovered_archive_manifest",
	}
	verifyAndFinalizeRECCResult(t, target, targetServer.URL, &res, false)
	return res
}

func TestRECCIntegratedLocalFaultQualification3Rep(t *testing.T) {
	scenarios := []struct {
		name string
		fn   func(t *testing.T, rep int) reccScenarioRepResult
	}{
		{"controller_only_failover", runRECCControllerOnlyFailover},
		{"standby_worker_loss", runRECCStandbyWorkerLoss},
		{"active_controller_and_worker_loss", runRECCActiveControllerAndWorkerLoss},
		{"no_spare_capacity", runRECCNoSpareCapacity},
		{"network_partition", runRECCNetworkPartition},
		{"second_leader_change_during_recovery", runRECCSecondLeaderChangeDuringRecovery},
		{"local_storage_archive_interruption", runRECCLocalStorageArchiveInterruption},
	}

	const repetitions = 3
	var allRuns []reccScenarioRepResult

	for _, sc := range scenarios {
		sc := sc
		t.Run(sc.name, func(t *testing.T) {
			for rep := 1; rep <= repetitions; rep++ {
				rep := rep
				t.Run(fmt.Sprintf("rep_%d", rep), func(t *testing.T) {
					res := sc.fn(t, rep)
					allRuns = append(allRuns, res)
				})
			}
		})
	}

	if len(allRuns) != len(scenarios)*repetitions {
		t.Fatalf("executed %d runs, want %d", len(allRuns), len(scenarios)*repetitions)
	}

	passedCount := 0
	allReconciled := true
	for _, r := range allRuns {
		if r.Passed {
			passedCount++
		}
		if r.ReconciledGenerationSum != r.TargetTotalRequests || r.RetainedEvidenceTransactions != r.TargetTotalRequests || r.DuplicateGenerationOverlap != 0 || r.ActiveReservationsAfterTerminal != 0 || r.FalseCompleteArchive {
			allReconciled = false
		}
	}
	if passedCount != len(allRuns) || !allReconciled {
		t.Fatalf("passed=%d/%d allReconciled=%v", passedCount, len(allRuns), allReconciled)
	}

	summary := reccQualificationSummary{
		SchemaVersion:          1,
		RoadmapID:              "REC-C",
		WorkPackage:            "REC-C1",
		TestName:               "TestRECCIntegratedLocalFaultQualification3Rep",
		GeneratedAtUTC:         time.Now().UTC().Format(time.RFC3339),
		RepetitionsPerScenario: repetitions,
		ScenarioCount:          len(scenarios),
		TotalRuns:              len(allRuns),
		PassedRuns:             passedCount,
		FailedRuns:             len(allRuns) - passedCount,
		LocalRecoveryBudgetMS:  120000,
		AllReconciled:          allReconciled,
		Runs:                   allRuns,
	}

	encoded, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		t.Fatalf("marshal qualification summary: %v", err)
	}

	if evidenceDir := strings.TrimSpace(os.Getenv("APEXLOAD_REC_C_EVIDENCE_DIR")); evidenceDir != "" {
		if err := os.MkdirAll(evidenceDir, 0755); err != nil {
			t.Fatalf("mkdir evidence dir %s: %v", evidenceDir, err)
		}
		summaryPath := filepath.Join(evidenceDir, "summary.json")
		if err := os.WriteFile(summaryPath, append(encoded, '\n'), 0644); err != nil {
			t.Fatalf("write evidence summary %s: %v", summaryPath, err)
		}
	}
}
