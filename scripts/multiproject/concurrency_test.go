//go:build multiproject_integration

package multiproject

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

const targetSecretName = "mp_target_load"

// tenantSpec fixes each tenant's project-level concurrency limit. alpha can run
// one test at a time, so its second test must queue while the others keep
// running; bravo and charlie only meet the global limit.
var tenantSpecs = []struct {
	Key           string
	MaxConcurrent int
}{
	{Key: "alpha", MaxConcurrent: 1},
	{Key: "bravo", MaxConcurrent: 3},
	{Key: "charlie", MaxConcurrent: 3},
}

type tenant struct {
	Key              string
	Token            string // unique marker in this tenant's request names
	ProjectName      string
	ProjectID        string
	ScriptID         string
	ActionName       string
	MaxConcurrent    int
	ServiceAccountID string
	client           *apiClient
}

type trackedRun struct {
	ID     string
	Name   string
	Phase  string
	Tenant *tenant
}

type suite struct {
	cfg      suiteConfig
	ctx      context.Context
	admin    *apiClient
	replicas []*apiClient
	observer *targetObserver
	sampler  *admissionSampler
	ev       *evidence

	tenants []*tenant
	byKey   map[string]*tenant

	mu   sync.Mutex
	runs []*trackedRun

	originalGlobal int // the deployment's limit before the suite changed it; 0 when untouched
	globalLimit    int // the limit currently in force
}

// TestMultiProjectConcurrency runs tests from several projects at the same
// time against one deployment and checks that admission, queueing, isolation
// and results stay correct per project. Phases run in order and leave no
// active run behind them.
func TestMultiProjectConcurrency(t *testing.T) {
	cfg := loadSuiteConfig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := newSuite(t, ctx, cfg)

	if !s.phase(t, "preflight", s.preflight) || !s.phase(t, "provision tenants", s.provision) {
		t.FailNow()
	}
	samplerCtx, stopSampler := context.WithCancel(ctx)
	samplerDone := make(chan struct{})
	go func() { defer close(samplerDone); s.sampler.run(samplerCtx) }()
	defer func() { stopSampler(); <-samplerDone }()

	s.phase(t, "projects launch at the same moment and run together", s.concurrentLaunch)
	s.phase(t, "a full project queues only its own work", s.projectQuotaIsolation)
	s.phase(t, "the global queue promotes the least-loaded project first", s.globalFairShare)
	s.phase(t, "two projects race for the last global slot", s.lastSlotRace)
	s.phase(t, "every run's results belong to that run alone", s.resultIsolation)
	s.phase(t, "every controller replica reports the same outcomes", s.replicaConsistency)

	stopSampler()
	<-samplerDone
	s.phase(t, "admission limits held in every sample", s.admissionInvariants)
}

func newSuite(t *testing.T, ctx context.Context, cfg suiteConfig) *suite {
	adminID := apiKeyIdentity("admin", cfg.AdminAPIKey)
	if cfg.AdminAPIKey == "" {
		adminID = trustedHeaderIdentity("admin", cfg.AdminSubject, cfg.ProxySecret)
	}
	s := &suite{
		cfg:      cfg,
		ctx:      ctx,
		admin:    newAPIClient(cfg.BaseURL, adminID),
		observer: &targetObserver{base: cfg.ObserverURL, token: cfg.ObserverToken, runID: cfg.TargetRunID, http: &http.Client{Timeout: 10 * time.Second}},
		byKey:    map[string]*tenant{},
		ev: &evidence{
			StartedAt: time.Now().UTC(),
			Profile:   cfg.Profile,
			Prefix:    cfg.Prefix,
			Limits:    map[string]any{},
			Runs:      map[string]*runEvidence{},
		},
	}
	for _, base := range cfg.ControllerURLs {
		s.replicas = append(s.replicas, newAPIClient(base, adminID))
	}
	s.sampler = newAdmissionSampler(s.admin)
	// Registered first so it runs last: every phase's own cleanup has already
	// happened, and the evidence includes the cleanup outcome.
	t.Cleanup(func() {
		s.teardown(t)
		summary := s.sampler.summary()
		s.ev.mu.Lock()
		s.ev.FinishedAt = time.Now().UTC()
		s.ev.Sampler = summary
		s.ev.mu.Unlock()
		if err := s.ev.write(cfg.EvidenceDir); err != nil {
			t.Errorf("write evidence: %v", err)
		} else if cfg.EvidenceDir != "" {
			t.Logf("evidence written to %s", cfg.EvidenceDir)
		}
	})
	return s
}

// phase runs one subtest, records its outcome, and always leaves the
// deployment with none of the suite's runs active.
func (s *suite) phase(t *testing.T, name string, fn func(*testing.T)) bool {
	started := time.Now().UTC()
	skipped := false
	ok := t.Run(name, func(t *testing.T) {
		// Deferred so it also runs when fn skips (Skip exits the goroutine).
		defer func() { skipped = t.Skipped() }()
		fn(t)
	})
	s.quiesce(t)
	outcome := "passed"
	switch {
	case !ok:
		outcome = "failed"
	case skipped:
		outcome = "skipped"
	}
	s.ev.mu.Lock()
	s.ev.Phases = append(s.ev.Phases, phaseEvidence{Name: name, Outcome: outcome, StartedAt: started, FinishedAt: time.Now().UTC()})
	s.ev.mu.Unlock()
	return ok
}

func (s *suite) preflight(t *testing.T) {
	var health map[string]any
	if err := s.admin.get(s.ctx, "/api/v1/system/health", &health); err != nil {
		t.Fatalf("controller health: %v", err)
	}
	s.ev.mu.Lock()
	s.ev.Controller = map[string]any{}
	for _, key := range []string{"version", "commit", "environment", "build_time"} {
		s.ev.Controller[key] = health[key]
	}
	s.ev.mu.Unlock()

	// The router must reach an admitted execution leader before any mutation.
	var readiness map[string]any
	if err := s.admin.get(s.ctx, "/api/v1/system/execution-readiness", &readiness); err != nil {
		t.Fatalf("no admitted execution leader behind %s: %v", s.cfg.BaseURL, err)
	}
	s.ev.mu.Lock()
	s.ev.Controller["ha_enabled"] = readiness["ha_enabled"]
	s.ev.mu.Unlock()

	// An admin-only read proves the identity carries system_admin; on a fresh
	// production stack it is also the request that triggers the bootstrap grant.
	if err := s.admin.get(s.ctx, "/api/v1/auth/service-accounts", nil); err != nil {
		t.Fatalf("admin identity is not a system administrator: %v", err)
	}
	var me struct {
		Authenticated bool   `json:"authenticated"`
		Mode          string `json:"mode"`
	}
	if err := s.admin.get(s.ctx, "/api/v1/auth/me", &me); err != nil || !me.Authenticated {
		t.Fatalf("admin identity is not authenticated (mode %q): %v", me.Mode, err)
	}
	s.ev.mu.Lock()
	s.ev.Controller["auth_mode"] = me.Mode
	s.ev.mu.Unlock()

	var inventory map[string]any
	if err := s.admin.get(s.ctx, "/api/v1/system/inventory", &inventory); err != nil {
		s.ev.note("system inventory unavailable: %v", err)
	} else if platform, ok := inventory["data_platform"].(map[string]any); ok {
		s.ev.mu.Lock()
		s.ev.Controller["data_platform"] = platform
		s.ev.mu.Unlock()
		if s.cfg.Profile == "docker" {
			for key, want := range map[string]string{"metadata_store": "postgres", "cache_backend": "redis", "archive_backend": "s3"} {
				if got, _ := platform[key].(string); got != want {
					t.Errorf("data platform %s = %q, want %q for the Docker profile", key, got, want)
				}
			}
		}
	}

	// Fleet capacity counts discovered generators whatever the configured
	// capacity-availability source; health reports a live count only when that
	// source includes live inventory.
	live := 0
	err := eventually(s.ctx, 2*time.Minute, time.Second, func() (bool, error) {
		var capacity struct {
			TotalGenerators int `json:"total_generators"`
		}
		if err := s.admin.get(s.ctx, "/api/v1/fleet/capacity", &capacity); err != nil {
			return false, err
		}
		live = capacity.TotalGenerators
		return live >= s.cfg.MinGenerators, nil
	})
	if err != nil {
		t.Fatalf("live generators = %d, want at least %d: %v", live, s.cfg.MinGenerators, err)
	}
	s.ev.mu.Lock()
	s.ev.Controller["live_generators"] = live
	s.ev.mu.Unlock()

	if _, err := s.observer.count(s.ctx, "preflight-probe"); err != nil {
		t.Fatalf("counting target is not observable at %s: %v", s.cfg.ObserverURL, err)
	}

	limit, err := s.currentGlobalLimit()
	if err != nil {
		t.Fatalf("read global settings: %v", err)
	}
	s.globalLimit = limit
	// The controller applies one limit to every project's runs, so the
	// invariant holds even when other teams share the deployment.
	s.sampler.setGlobalLimit(limit)
	s.ev.mu.Lock()
	s.ev.Limits["global_max_concurrent_tests_before"] = limit
	s.ev.Limits["mutate_global_settings"] = s.cfg.MutateGlobal
	s.ev.mu.Unlock()
	if s.cfg.MutateGlobal {
		if active := s.reservingTestCount(t); active != 0 {
			t.Fatalf("%d tests already hold capacity; the global phases need an otherwise idle deployment (or unset APEXLOAD_MP_MUTATE_GLOBAL_SETTINGS)", active)
		}
		s.originalGlobal = limit
	}
}

func (s *suite) provision(t *testing.T) {
	for _, spec := range tenantSpecs {
		tn := &tenant{
			Key:           spec.Key,
			Token:         "mp" + spec.Key,
			ProjectName:   s.cfg.Prefix + "-" + spec.Key,
			MaxConcurrent: spec.MaxConcurrent,
		}
		tn.ActionName = tn.Token + "-load"

		var project struct {
			ID string `json:"id"`
		}
		if err := s.admin.post(s.ctx, "/api/v1/projects", map[string]any{
			"name":     tn.ProjectName,
			"settings": map[string]any{"max_concurrent_tests": spec.MaxConcurrent},
		}, &project); err != nil || project.ID == "" {
			t.Fatalf("create project %s: id=%q err=%v", tn.ProjectName, project.ID, err)
		}
		tn.ProjectID = project.ID
		s.sampler.setProjectLimit(tn.ProjectID, spec.MaxConcurrent)

		if err := s.admin.post(s.ctx, "/api/v1/projects/"+tn.ProjectID+"/secrets", map[string]any{
			"secrets": []any{map[string]any{"name": targetSecretName, "value": s.cfg.TargetLoadToken}},
		}, nil); err != nil {
			t.Fatalf("create %s target secret: %v", tn.Key, err)
		}

		var script struct {
			ID        string `json:"id"`
			ProjectID string `json:"project_id"`
		}
		if err := s.admin.post(s.ctx, "/api/v1/scripts", map[string]any{
			"project_id": tn.ProjectID,
			"script":     s.scriptDocument(tn),
		}, &script); err != nil || script.ID == "" {
			t.Fatalf("create %s script: id=%q err=%v", tn.Key, script.ID, err)
		}
		if script.ProjectID != tn.ProjectID {
			t.Fatalf("%s script landed in project %q, want %q", tn.Key, script.ProjectID, tn.ProjectID)
		}
		tn.ScriptID = script.ID

		// Each tenant acts only through a key scoped to its own project, the way
		// separate teams' CI pipelines would.
		var account struct {
			ServiceAccount struct {
				ID string `json:"id"`
			} `json:"service_account"`
			APIKey string `json:"api_key"`
		}
		if err := s.admin.post(s.ctx, "/api/v1/auth/service-accounts", map[string]any{
			"name":          tn.ProjectName + "-ci",
			"description":   "multi-project concurrency suite tenant (disposable)",
			"project_roles": map[string][]string{tn.ProjectID: {"editor"}},
			"scopes":        []string{"read", "tests:run", "results:read"},
			"expires_at":    time.Now().Add(12 * time.Hour).UTC().Format(time.RFC3339),
		}, &account); err != nil || account.APIKey == "" {
			t.Fatalf("create %s service account: err=%v", tn.Key, err)
		}
		tn.ServiceAccountID = account.ServiceAccount.ID
		tn.client = newAPIClient(s.cfg.BaseURL, apiKeyIdentity(tn.Key, account.APIKey))

		if err := tn.client.get(s.ctx, "/api/v1/tests?limit=1&project_id="+tn.ProjectID, nil); err != nil {
			t.Fatalf("%s key cannot read its own project: %v", tn.Key, err)
		}
		s.tenants = append(s.tenants, tn)
		s.byKey[tn.Key] = tn
		s.ev.mu.Lock()
		s.ev.Tenants = append(s.ev.Tenants, tenantEvidence{Key: tn.Key, ProjectID: tn.ProjectID, ProjectName: tn.ProjectName, MaxConcurrent: tn.MaxConcurrent})
		s.ev.mu.Unlock()
	}
}

// scriptDocument sends every request to the counting target. The run ID header
// keys the target's counter, so the target independently records which run
// sent each request; the action name carries the tenant's marker so results
// can be checked for another tenant's requests.
func (s *suite) scriptDocument(tn *tenant) map[string]any {
	return map[string]any{
		"name":     tn.ProjectName + " target traffic",
		"version":  "1.0.0",
		"protocol": "http",
		"config": map[string]any{
			"base_url": s.cfg.TargetURL,
			"pacing":   map[string]any{"type": "fixed_interval", "interval_ms": s.cfg.PacingMS},
			"http":     map[string]any{"connect_timeout_ms": 2000, "request_timeout_ms": 5000},
		},
		"actions": []any{map[string]any{
			"type":   "http",
			"name":   tn.ActionName,
			"method": "GET",
			"target": "/load",
			"headers": map[string]any{
				"Authorization":         "Bearer {{secret." + targetSecretName + "}}",
				"X-Apexload-Fault-Run":  s.cfg.TargetRunID,
				"X-Apexload-Fault-Test": "{{run.id}}",
			},
		}},
	}
}

// --- Phase 1 ---------------------------------------------------------------

func (s *suite) concurrentLaunch(t *testing.T) {
	s.requireGlobalHeadroom(t, 4, 8)
	alpha, bravo, charlie := s.byKey["alpha"], s.byKey["bravo"], s.byKey["charlie"]
	const phase = "concurrent-launch"
	a1 := s.createRun(t, alpha, phase, "alpha-1")
	b1 := s.createRun(t, bravo, phase, "bravo-1")
	c1 := s.createRun(t, charlie, phase, "charlie-1")

	s.startTogether(t, a1, b1, c1)
	for _, run := range []*trackedRun{a1, b1, c1} {
		s.waitRunning(t, run)
	}
	key := s.sampler.watchOverlap(a1.ID, b1.ID, c1.ID)
	if err := eventually(s.ctx, 30*time.Second, 200*time.Millisecond, func() (bool, error) {
		return !s.sampler.overlapAt(key).IsZero(), nil
	}); err != nil {
		t.Fatalf("never observed all three projects RUNNING in the same sample: %v", err)
	}

	// Traffic, not just status: every run's requests must reach the target
	// while the others are running too.
	s.requireTrafficGrowth(t, a1, b1, c1)
	s.recordGeneratorSpread(t, a1, b1, c1)

	t.Run("tenants cannot see or control each other's runs", func(t *testing.T) {
		// A project-scoped key may get a filtered list or a 403 for an
		// unscoped or foreign-project list; it must never see another
		// project's runs.
		for _, query := range []string{"", "&project_id=" + bravo.ProjectID, "&project_id=" + alpha.ProjectID} {
			var page struct {
				Tests []testRun `json:"tests"`
			}
			err := alpha.client.get(s.ctx, "/api/v1/tests?limit=2000"+query, &page)
			if status := statusOf(err); err != nil && !(status == http.StatusForbidden && query != "&project_id="+alpha.ProjectID) {
				t.Errorf("alpha lists tests%s: %v", query, err)
				continue
			}
			visible := map[string]bool{}
			for _, run := range page.Tests {
				visible[run.ID] = true
				if run.ProjectID != alpha.ProjectID {
					t.Errorf("alpha's list%s includes %s from project %s", query, run.ID, run.ProjectID)
				}
			}
			if query == "&project_id="+alpha.ProjectID && !visible[a1.ID] {
				t.Errorf("alpha's own-project list is missing its run %s", a1.ID)
			}
		}
		for _, probe := range []struct{ method, path string }{
			{http.MethodGet, "/api/v1/tests/" + b1.ID},
			{http.MethodGet, "/api/v1/tests/" + b1.ID + "/metrics"},
			{http.MethodPost, "/api/v1/tests/" + b1.ID + "/stop"},
			{http.MethodPost, "/api/v1/tests/" + c1.ID + "/abort"},
		} {
			var body any
			if probe.method == http.MethodPost {
				body = map[string]any{}
			}
			_, err := alpha.client.do(s.ctx, probe.method, probe.path, body, nil)
			if status := statusOf(err); status != http.StatusForbidden && status != http.StatusNotFound {
				t.Errorf("alpha %s %s: got %v, want 403 or 404", probe.method, probe.path, err)
			}
		}
		for _, run := range []*trackedRun{b1, c1} {
			current := s.getRun(t, run)
			if current.Status != "RUNNING" {
				t.Errorf("%s is %s after alpha's denied stop/abort, want RUNNING", run.Name, current.Status)
			}
		}
		s.requireTrafficGrowth(t, b1, c1)
	})
}

// --- Phase 2 ---------------------------------------------------------------

func (s *suite) projectQuotaIsolation(t *testing.T) {
	s.requireGlobalHeadroom(t, 4, 8)
	alpha, bravo, charlie := s.byKey["alpha"], s.byKey["bravo"], s.byKey["charlie"]
	const phase = "project-quota"
	a1 := s.createRun(t, alpha, phase, "alpha-quota-1")
	b1 := s.createRun(t, bravo, phase, "bravo-quota-1")
	c1 := s.createRun(t, charlie, phase, "charlie-quota-1")
	s.startTogether(t, a1, b1, c1)
	for _, run := range []*trackedRun{a1, b1, c1} {
		s.waitRunning(t, run)
	}

	// alpha is at its limit of one: its second run must wait...
	a2 := s.createRun(t, alpha, phase, "alpha-quota-2")
	s.start(t, a2)
	s.waitPending(t, a2, "project test slot")

	// ...while bravo, with spare project capacity, starts straight away.
	b2 := s.createRun(t, bravo, phase, "bravo-quota-2")
	s.start(t, b2)
	s.waitRunning(t, b2)
	if current := s.getRun(t, a2); current.Status != "PENDING" {
		t.Fatalf("alpha's queued run became %s while alpha was still at its limit", current.Status)
	}

	// Freeing alpha's slot promotes its queued run without another request.
	s.stopAndWait(t, a1)
	promoted := s.waitRunning(t, a2)
	ended := s.getRun(t, a1).EndedAt
	if ended != nil && promoted.StartedAt != nil && promoted.StartedAt.Before(*ended) {
		t.Errorf("alpha's queued run started at %s, before its predecessor ended at %s", promoted.StartedAt, ended)
	}
}

// --- Phase 3 ---------------------------------------------------------------

// globalFairShare fills the global limit, queues an older run from the busiest
// project and a newer one from a lighter project, then frees one slot from an
// uninvolved project. Fair share must pick the lighter project's newer run; a
// first-in-first-out queue would pick the older one.
func (s *suite) globalFairShare(t *testing.T) {
	s.requireGlobalMutation(t)
	s.setGlobalLimit(t, 4)
	alpha, bravo, charlie := s.byKey["alpha"], s.byKey["bravo"], s.byKey["charlie"]
	const phase = "global-fair-share"
	a1 := s.createRun(t, alpha, phase, "alpha-fair-1")
	b1 := s.createRun(t, bravo, phase, "bravo-fair-1")
	b2 := s.createRun(t, bravo, phase, "bravo-fair-2")
	c1 := s.createRun(t, charlie, phase, "charlie-fair-1")
	s.startTogether(t, a1, b1, b2, c1)
	for _, run := range []*trackedRun{a1, b1, b2, c1} {
		s.waitRunning(t, run)
	}

	older := s.createRun(t, bravo, phase, "bravo-fair-3")
	s.start(t, older)
	s.waitPending(t, older, "global test slot")
	// Separate the queue anchors so "older" is unambiguous.
	time.Sleep(1500 * time.Millisecond)
	newer := s.createRun(t, charlie, phase, "charlie-fair-2")
	s.start(t, newer)
	s.waitPending(t, newer, "global test slot")

	s.stopAndWait(t, a1)
	s.waitRunning(t, newer)
	if current := s.getRun(t, older); current.Status != "PENDING" {
		t.Fatalf("bravo's older queued run is %s; with bravo running 2 tests and charlie 1, the freed slot belongs to charlie", current.Status)
	}

	// The queue still drains: freeing another slot promotes the older run.
	s.stopAndWait(t, c1)
	s.waitRunning(t, older)
}

// --- Phase 4 ---------------------------------------------------------------

// lastSlotRace starts runs from two projects at the same instant when only one
// global slot is free. Exactly one may take it; the other must queue and then
// take the slot when it frees.
func (s *suite) lastSlotRace(t *testing.T) {
	s.requireGlobalMutation(t)
	s.setGlobalLimit(t, 3)
	alpha, bravo, charlie := s.byKey["alpha"], s.byKey["bravo"], s.byKey["charlie"]
	const phase = "last-slot-race"
	f1 := s.createRun(t, bravo, phase, "bravo-race-filler-1")
	f2 := s.createRun(t, bravo, phase, "bravo-race-filler-2")
	s.startTogether(t, f1, f2)
	s.waitRunning(t, f1)
	s.waitRunning(t, f2)

	winners := map[string]int{}
	for round := 1; round <= s.cfg.RaceRounds; round++ {
		left := s.createRun(t, alpha, phase, fmt.Sprintf("alpha-race-%d", round))
		right := s.createRun(t, charlie, phase, fmt.Sprintf("charlie-race-%d", round))
		s.startTogether(t, left, right)

		var winner, loser *trackedRun
		err := eventually(s.ctx, 2*time.Minute, 300*time.Millisecond, func() (bool, error) {
			l, r := s.getRun(t, left), s.getRun(t, right)
			lr, rr := reservingStatuses[l.Status], reservingStatuses[r.Status]
			if lr && rr {
				t.Fatalf("round %d: both racers hold capacity (%s, %s) with one free slot", round, l.Status, r.Status)
			}
			switch {
			case lr && r.Status == "PENDING":
				winner, loser = left, right
			case rr && l.Status == "PENDING":
				winner, loser = right, left
			default:
				return false, nil
			}
			return true, nil
		})
		if err != nil {
			t.Fatalf("round %d did not settle into one admitted run and one queued run: %v", round, err)
		}
		winners[winner.Tenant.Key]++
		s.waitRunning(t, winner)
		s.waitPending(t, loser, "global test slot")
		s.stopAndWait(t, winner)
		s.waitRunning(t, loser)
		s.stopAndWait(t, loser)
	}
	s.ev.mu.Lock()
	s.ev.Limits["race_winners"] = winners
	s.ev.mu.Unlock()
}

// --- Phase 5 ---------------------------------------------------------------

func (s *suite) resultIsolation(t *testing.T) {
	runs := s.trackedRuns()
	if len(runs) == 0 {
		t.Fatal("no runs were tracked")
	}
	for _, run := range runs {
		t.Run(run.Name, func(t *testing.T) {
			final := s.getRun(t, run)
			if !terminalStatuses[final.Status] {
				t.Fatalf("%s is %s, want a terminal status", run.Name, final.Status)
			}
			if final.Status == "FAILED" {
				t.Fatalf("%s FAILED: %s", run.Name, final.Error)
			}
			observed, err := s.observer.count(s.ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			ev := s.ev.run(run.ID)
			ev.TargetRequests = &observed
			// A distributed run reports RUNNING a couple of seconds before its
			// coordinated start barrier (StartedAt). One that ended before the
			// barrier must not have sent anything; only one that passed it has
			// traffic to reconcile.
			if final.StartedAt == nil || (final.EndedAt != nil && !final.EndedAt.After(*final.StartedAt)) {
				if observed != 0 {
					t.Fatalf("%s ended before its start barrier but the target counted %d requests for it", run.Name, observed)
				}
				t.Skipf("%s ended before its start barrier (final %s); no traffic to reconcile", run.Name, final.Status)
			}
			if observed == 0 {
				t.Fatalf("%s ran but the target counted none of its requests", run.Name)
			}

			report := s.waitArchiveReport(t, run)
			requests, errorsCount, names := reportRequestCounts(report)
			ev.ReportedRequests, ev.ReportedErrors, ev.RequestNames = requests, errorsCount, names
			if requests == nil {
				t.Fatalf("%s archive report has no request population", run.Name)
			}
			if errorsCount != nil && *errorsCount != 0 {
				t.Errorf("%s reported %d request errors; the target accepts every authorised request", run.Name, *errorsCount)
			}
			delta := *requests - int64(observed)
			if delta < 0 {
				delta = -delta
			}
			allowed := s.cfg.CountToleranceAt
			if ratio := int64(float64(observed) * s.cfg.CountTolerance); ratio > allowed {
				allowed = ratio
			}
			if delta > allowed {
				t.Errorf("%s reported %d requests but the target counted %d for this run (allowed difference %d)", run.Name, *requests, observed, allowed)
			}
			for _, name := range names {
				for _, other := range s.tenants {
					if other != run.Tenant && strings.Contains(name, other.Token) {
						t.Errorf("%s's results contain %q, a request from tenant %s", run.Name, name, other.Key)
					}
				}
			}
		})
	}

	t.Run("another tenant cannot read a run's results", func(t *testing.T) {
		alphaRun := s.firstRunOf("alpha")
		if alphaRun == nil {
			t.Skip("no alpha run to probe")
		}
		_, err := s.byKey["bravo"].client.do(s.ctx, http.MethodGet, "/api/v1/tests/"+alphaRun.ID+"/archive/report", nil, nil)
		if status := statusOf(err); status != http.StatusForbidden && status != http.StatusNotFound {
			t.Fatalf("bravo reading alpha's report: got %v, want 403 or 404", err)
		}
	})
}

// --- Phase 6 ---------------------------------------------------------------

func (s *suite) replicaConsistency(t *testing.T) {
	if len(s.replicas) < 2 {
		t.Skip("set APEXLOAD_MP_CONTROLLER_URLS to two or more direct controller URLs to compare replicas")
	}
	for _, run := range s.trackedRuns() {
		want := s.getRun(t, run)
		for index, replica := range s.replicas {
			err := eventually(s.ctx, 30*time.Second, time.Second, func() (bool, error) {
				var got testRun
				if err := replica.get(s.ctx, "/api/v1/tests/"+run.ID, &got); err != nil {
					return false, err
				}
				if got.ProjectID != want.ProjectID {
					return false, fmt.Errorf("project %q, want %q", got.ProjectID, want.ProjectID)
				}
				if status := strings.ToUpper(got.Status); status != want.Status {
					return false, fmt.Errorf("status %s, want %s", status, want.Status)
				}
				return true, nil
			})
			if err != nil {
				t.Errorf("replica %d (%s) disagrees about %s: %v", index+1, s.cfg.ControllerURLs[index], run.Name, err)
			}
		}
	}
}

// --- Phase 7 ---------------------------------------------------------------

func (s *suite) admissionInvariants(t *testing.T) {
	summary := s.sampler.summary()
	if summary.Samples < 10 {
		t.Fatalf("only %d admission samples were taken (errors: %d, last: %s)", summary.Samples, summary.Errors, summary.LastError)
	}
	if summary.Errors*10 > summary.Samples {
		t.Errorf("%d of %d admission samples failed (last: %s)", summary.Errors, summary.Samples+summary.Errors, summary.LastError)
	}
	for _, violation := range summary.Violations {
		t.Errorf("admission limit exceeded: %s", violation)
	}
	if alpha := s.byKey["alpha"]; alpha != nil && summary.MaxByProject[alpha.ProjectID] > alpha.MaxConcurrent {
		t.Errorf("alpha held %d slots at once; its limit is %d", summary.MaxByProject[alpha.ProjectID], alpha.MaxConcurrent)
	}
}

// --- Run helpers -------------------------------------------------------------

func (s *suite) createRun(t *testing.T, tn *tenant, phase, name string) *trackedRun {
	t.Helper()
	var created testRun
	if err := tn.client.post(s.ctx, "/api/v1/tests", map[string]any{
		"name":       s.cfg.Prefix + "-" + name,
		"project_id": tn.ProjectID,
		"script_id":  tn.ScriptID,
		"vusers":     s.cfg.VUsers,
		// Every run is stopped explicitly; the duration only bounds a run the
		// suite failed to stop.
		"duration": "20m",
	}, &created); err != nil {
		t.Fatalf("%s creates %s: %v", tn.Key, name, err)
	}
	if created.ID == "" || created.ProjectID != tn.ProjectID {
		t.Fatalf("%s created %s as id=%q project=%q, want project %q", tn.Key, name, created.ID, created.ProjectID, tn.ProjectID)
	}
	run := &trackedRun{ID: created.ID, Name: name, Phase: phase, Tenant: tn}
	s.mu.Lock()
	s.runs = append(s.runs, run)
	s.mu.Unlock()
	s.sampler.track(run.ID, tn.ProjectID)
	ev := s.ev.run(run.ID)
	ev.Name, ev.Tenant, ev.ProjectID, ev.Phase = name, tn.Key, tn.ProjectID, phase
	return run
}

// startRun requests a start as the run's own tenant. Whether it starts or
// queues is read back from the run, not from the response.
func (s *suite) startRun(run *trackedRun) error {
	return run.Tenant.client.post(s.ctx, "/api/v1/tests/"+run.ID+"/start", map[string]any{}, nil)
}

func (s *suite) start(t *testing.T, run *trackedRun) {
	t.Helper()
	if err := s.startRun(run); err != nil {
		t.Fatalf("%s starts %s: %v", run.Tenant.Key, run.Name, err)
	}
}

// startTogether releases every start request at the same instant.
func (s *suite) startTogether(t *testing.T, runs ...*trackedRun) {
	t.Helper()
	gate := make(chan struct{})
	type result struct {
		run *trackedRun
		err error
	}
	results := make(chan result, len(runs))
	var ready sync.WaitGroup
	for _, run := range runs {
		ready.Add(1)
		go func(run *trackedRun) {
			ready.Done()
			<-gate
			results <- result{run: run, err: s.startRun(run)}
		}(run)
	}
	ready.Wait()
	close(gate)
	for range runs {
		if r := <-results; r.err != nil {
			t.Errorf("%s starts %s: %v", r.run.Tenant.Key, r.run.Name, r.err)
		}
	}
	if t.Failed() {
		t.FailNow()
	}
}

func (s *suite) getRun(t *testing.T, run *trackedRun) testRun {
	t.Helper()
	var current testRun
	if err := s.admin.get(s.ctx, "/api/v1/tests/"+run.ID, &current); err != nil {
		t.Fatalf("read %s: %v", run.Name, err)
	}
	current.Status = strings.ToUpper(current.Status)
	return current
}

// waitFor polls until accept is satisfied. A run that turns terminal while the
// suite waits for something else fails at once rather than at the timeout.
func (s *suite) waitFor(t *testing.T, run *trackedRun, timeout time.Duration, what string, accept func(testRun) bool) testRun {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		current := s.getRun(t, run)
		if accept(current) {
			return current
		}
		if terminalStatuses[current.Status] {
			t.Fatalf("%s ended %s (%s) while waiting for it to be %s", run.Name, current.Status, current.Error, what)
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s is still %s (reason %q) after %s; want %s", run.Name, current.Status, current.PendingReason, timeout, what)
		}
		select {
		case <-s.ctx.Done():
			t.Fatal(s.ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// waitRunning waits until the run is RUNNING and its requests are reaching the
// target. A distributed run reports RUNNING a couple of seconds before its
// coordinated start barrier, so status alone does not mean load is flowing.
func (s *suite) waitRunning(t *testing.T, run *trackedRun) testRun {
	t.Helper()
	current := s.waitFor(t, run, 3*time.Minute, "RUNNING", func(r testRun) bool { return r.Status == "RUNNING" })
	if err := eventually(s.ctx, time.Minute, 250*time.Millisecond, func() (bool, error) {
		count, err := s.observer.count(s.ctx, run.ID)
		return count > 0, err
	}); err != nil {
		t.Fatalf("%s is RUNNING but no request reached the target: %v (status %s)", run.Name, err, s.getRun(t, run).Status)
	}
	current = s.getRun(t, run)
	ev := s.ev.run(run.ID)
	ev.StartedAt = current.StartedAt
	ev.Generators = shardTargets(current)
	return current
}

func (s *suite) waitPending(t *testing.T, run *trackedRun, reason string) testRun {
	t.Helper()
	current := s.waitFor(t, run, 30*time.Second, "PENDING for a "+reason, func(r testRun) bool {
		return r.Status == "PENDING" && strings.Contains(r.PendingReason, reason)
	})
	s.ev.run(run.ID).PendingReason = current.PendingReason
	return current
}

func (s *suite) stopAndWait(t *testing.T, run *trackedRun) testRun {
	t.Helper()
	if err := run.Tenant.client.post(s.ctx, "/api/v1/tests/"+run.ID+"/stop", map[string]any{}, nil); err != nil {
		t.Fatalf("%s stops %s: %v", run.Tenant.Key, run.Name, err)
	}
	final := s.waitFor(t, run, 3*time.Minute, "terminal", func(r testRun) bool { return terminalStatuses[r.Status] })
	s.recordFinal(run, final)
	return final
}

func (s *suite) recordFinal(run *trackedRun, final testRun) {
	ev := s.ev.run(run.ID)
	ev.FinalStatus = final.Status
	ev.EndedAt = final.EndedAt
	if final.StartedAt != nil {
		ev.StartedAt = final.StartedAt
	}
}

func shardTargets(run testRun) []string {
	set := map[string]bool{}
	for _, shard := range run.GeneratorShards {
		if shard.Target != "" {
			set[shard.Target] = true
		}
	}
	out := make([]string, 0, len(set))
	for target := range set {
		out = append(out, target)
	}
	sort.Strings(out)
	return out
}

// requireTrafficGrowth checks that every run's target counter rises within the
// same window.
func (s *suite) requireTrafficGrowth(t *testing.T, runs ...*trackedRun) {
	t.Helper()
	before := map[string]uint64{}
	for _, run := range runs {
		count, err := s.observer.count(s.ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		before[run.ID] = count
	}
	err := eventually(s.ctx, 30*time.Second, time.Second, func() (bool, error) {
		for _, run := range runs {
			count, err := s.observer.count(s.ctx, run.ID)
			if err != nil {
				return false, err
			}
			if count <= before[run.ID] {
				return false, fmt.Errorf("%s still at %d requests", run.Name, count)
			}
		}
		return true, nil
	})
	if err != nil {
		t.Fatalf("not every concurrently running test sent traffic: %v", err)
	}
}

func (s *suite) recordGeneratorSpread(t *testing.T, runs ...*trackedRun) {
	t.Helper()
	used := map[string][]string{}
	for _, run := range runs {
		for _, target := range shardTargets(s.getRun(t, run)) {
			used[target] = append(used[target], run.Name)
		}
	}
	targets := make([]string, 0, len(used))
	for target := range used {
		targets = append(targets, fmt.Sprintf("%s=%s", target, strings.Join(used[target], "+")))
	}
	sort.Strings(targets)
	s.ev.note("concurrent launch placed runs on generators: %s", strings.Join(targets, "; "))
}

func (s *suite) waitArchiveReport(t *testing.T, run *trackedRun) map[string]any {
	t.Helper()
	var report map[string]any
	// The archive finalizes asynchronously after the run turns terminal, so
	// keep asking until the report exists.
	err := eventually(s.ctx, 3*time.Minute, 2*time.Second, func() (bool, error) {
		report = nil
		if err := run.Tenant.client.get(s.ctx, "/api/v1/tests/"+run.ID+"/archive/report", &report); err != nil {
			return false, err
		}
		return report != nil, nil
	})
	if err != nil {
		t.Fatalf("archive report for %s: %v", run.Name, err)
	}
	return report
}

func (s *suite) trackedRuns() []*trackedRun {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*trackedRun(nil), s.runs...)
}

func (s *suite) firstRunOf(key string) *trackedRun {
	for _, run := range s.trackedRuns() {
		if run.Tenant.Key == key {
			return run
		}
	}
	return nil
}

// --- Capacity helpers -------------------------------------------------------

func (s *suite) currentGlobalLimit() (int, error) {
	var settings map[string]any
	if err := s.admin.get(s.ctx, "/api/v1/system/settings", &settings); err != nil {
		return 0, err
	}
	general, _ := settings["general"].(map[string]any)
	value, ok := general["max_concurrent_tests"].(float64)
	if !ok {
		return 0, fmt.Errorf("system settings have no general.max_concurrent_tests")
	}
	return int(value), nil
}

// setGlobalLimit changes only general.max_concurrent_tests, using the GET
// document's ETag so a concurrent settings edit fails instead of being lost.
func (s *suite) setGlobalLimit(t *testing.T, limit int) {
	t.Helper()
	if err := s.putGlobalLimit(limit); err != nil {
		t.Fatalf("set global max_concurrent_tests=%d: %v", limit, err)
	}
	s.globalLimit = limit
	s.sampler.setGlobalLimit(limit)
	s.ev.mu.Lock()
	s.ev.Limits[fmt.Sprintf("global_max_concurrent_tests_set_%s", time.Now().UTC().Format("150405.000"))] = limit
	s.ev.mu.Unlock()
}

// putGlobalLimit retries the read-modify-write on 412, as the API instructs:
// settings reads balance across HA replicas, and a replica that has not yet
// seen the previous update (about a second, measured) serves a stale ETag.
func (s *suite) putGlobalLimit(limit int) error {
	var last error
	for attempt := 1; attempt <= 10; attempt++ {
		var settings map[string]any
		header, err := s.admin.do(s.ctx, http.MethodGet, "/api/v1/system/settings", nil, &settings)
		if err != nil {
			return err
		}
		general, ok := settings["general"].(map[string]any)
		if !ok {
			return fmt.Errorf("system settings have no general section")
		}
		// Always write: a read already showing the target may be a replica
		// that has not seen the previous update yet.
		general["max_concurrent_tests"] = limit
		etag := header.Get("ETag")
		if etag == "" {
			return fmt.Errorf("system settings response has no ETag")
		}
		_, err = s.admin.doWithHeaders(s.ctx, http.MethodPut, "/api/v1/system/settings", http.Header{"If-Match": {etag}}, settings, nil)
		if statusOf(err) != http.StatusPreconditionFailed {
			if attempt > 1 && err == nil {
				s.ev.note("global limit %d needed %d settings attempts: a replica served a stale revision after the previous update", limit, attempt)
			}
			return err
		}
		last = err
		select {
		case <-s.ctx.Done():
			return s.ctx.Err()
		case <-time.After(time.Duration(attempt) * 300 * time.Millisecond):
		}
	}
	return last
}

func (s *suite) reservingTestCount(t *testing.T) int {
	t.Helper()
	var page struct {
		Tests []testRun `json:"tests"`
	}
	if err := s.admin.get(s.ctx, "/api/v1/tests?active_only=true&limit=2000", &page); err != nil {
		t.Fatalf("list active tests: %v", err)
	}
	count := 0
	for _, run := range page.Tests {
		if reservingStatuses[strings.ToUpper(run.Status)] {
			count++
		}
	}
	return count
}

func (s *suite) requireGlobalMutation(t *testing.T) {
	t.Helper()
	if !s.cfg.MutateGlobal {
		t.Skip("needs APEXLOAD_MP_MUTATE_GLOBAL_SETTINGS=1 on a dedicated deployment: it lowers the global concurrent-test limit")
	}
}

// requireGlobalHeadroom makes room for a phase that needs `need` slots. On a
// deployment the suite may reconfigure it sets the limit to `set`; otherwise
// it skips when other work leaves too little room.
func (s *suite) requireGlobalHeadroom(t *testing.T, need, set int) {
	t.Helper()
	if s.cfg.MutateGlobal {
		s.setGlobalLimit(t, set)
		return
	}
	if free := s.globalLimit - s.reservingTestCount(t); s.globalLimit > 0 && free < need {
		t.Skipf("phase needs %d free global slots but only %d of %d are free", need, free, s.globalLimit)
	}
}

// --- Cleanup -----------------------------------------------------------------

// quiesce stops or aborts every tracked run that is not terminal and waits for
// it, so a failed phase cannot leak capacity into the next one.
func (s *suite) quiesce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	for _, run := range s.trackedRuns() {
		var current testRun
		if err := s.admin.get(ctx, "/api/v1/tests/"+run.ID, &current); err != nil {
			t.Errorf("quiesce: read %s: %v", run.Name, err)
			continue
		}
		status := strings.ToUpper(current.Status)
		if terminalStatuses[status] {
			s.recordFinal(run, current)
			continue
		}
		action := "abort"
		if reservingStatuses[status] {
			action = "stop"
		}
		if err := s.admin.post(ctx, "/api/v1/tests/"+run.ID+"/"+action, map[string]any{}, nil); err != nil {
			t.Errorf("quiesce: %s %s: %v", action, run.Name, err)
		}
	}
	for _, run := range s.trackedRuns() {
		err := eventually(ctx, 3*time.Minute, time.Second, func() (bool, error) {
			var current testRun
			if err := s.admin.get(ctx, "/api/v1/tests/"+run.ID, &current); err != nil {
				return false, err
			}
			current.Status = strings.ToUpper(current.Status)
			if terminalStatuses[current.Status] {
				s.recordFinal(run, current)
				return true, nil
			}
			return false, fmt.Errorf("still %s", current.Status)
		})
		if err != nil {
			// Last resort, so the deployment is not left running load.
			_ = s.admin.post(ctx, "/api/v1/tests/"+run.ID+"/abort", map[string]any{}, nil)
			t.Errorf("quiesce: %s did not finish: %v", run.Name, err)
		}
	}
}

// teardown restores the global limit and deletes the tenants' keys. Projects
// and their runs stay as evidence unless APEXLOAD_MP_DELETE_PROJECTS is set; a
// Docker stack is discarded whole anyway.
func (s *suite) teardown(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	saved := s.ctx
	s.ctx = ctx
	defer func() { s.ctx = saved }()
	if s.originalGlobal > 0 && s.globalLimit != s.originalGlobal {
		if err := s.putGlobalLimit(s.originalGlobal); err != nil {
			t.Errorf("restore global max_concurrent_tests=%d: %v", s.originalGlobal, err)
		} else {
			s.ev.mu.Lock()
			s.ev.Limits["global_max_concurrent_tests_restored"] = s.originalGlobal
			s.ev.mu.Unlock()
		}
	}
	for _, tn := range s.tenants {
		if tn.ServiceAccountID == "" {
			continue
		}
		if _, err := s.admin.do(ctx, http.MethodDelete, "/api/v1/auth/service-accounts/"+tn.ServiceAccountID, nil, nil); err != nil {
			t.Errorf("delete %s service account: %v", tn.Key, err)
		}
	}
	if !envBool(strings.TrimSpace(os.Getenv("APEXLOAD_MP_DELETE_PROJECTS"))) {
		return
	}
	for _, run := range s.trackedRuns() {
		if _, err := s.admin.do(ctx, http.MethodDelete, "/api/v1/tests/"+run.ID, nil, nil); err != nil && statusOf(err) != http.StatusNotFound {
			t.Errorf("delete run %s: %v", run.Name, err)
		}
	}
	for _, tn := range s.tenants {
		if _, err := s.admin.do(ctx, http.MethodDelete, "/api/v1/projects/"+tn.ProjectID, nil, nil); err != nil && statusOf(err) != http.StatusNotFound {
			t.Errorf("delete project %s: %v", tn.ProjectName, err)
		}
	}
}
