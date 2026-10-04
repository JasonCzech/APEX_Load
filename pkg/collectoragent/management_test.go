package collectoragent

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

type managementObserverFunc func(context.Context, *managementObservation) (*analysis.MessagingManagementSnapshot, error)

// The generic controlled profile is an explicit test-only observer contract.
// Production New cannot inject this implementation or skip real validation.
func (f managementObserverFunc) prepareTarget(t *ManagementTarget) error { return f.validateTarget(t) }
func (f managementObserverFunc) validateTarget(t *ManagementTarget) error {
	if t == nil {
		return errors.New("controlled target missing")
	}
	if err := t.Scope.Validate(); err != nil {
		return err
	}
	return t.Identity.Validate()
}
func (f managementObserverFunc) observe(ctx context.Context, p *managementObservation) (*analysis.MessagingManagementSnapshot, error) {
	return f(ctx, p)
}
func managementTestTarget() ManagementTarget {
	return ManagementTarget{Scope: analysis.ManagementScope{Version: 1, Kind: "kafka_offsets", Provider: "approved-cluster", Isolation: "read_uncommitted", Entities: []analysis.ManagementEntity{{Kafka: &analysis.KafkaManagementEntity{Topic: "orders", Partition: 0, Group: "reader"}}}}, Identity: analysis.ManagementIdentity{Mode: "anonymous", Principal: "controlled-reader", PolicyRevision: "1"}, Endpoints: []string{"https://approved.example.test:443"}}
}
func managementTestGrant(instance string) ManagementPollGrant {
	target := managementTestTarget()
	scope, _ := target.Scope.Hash()
	identity, _ := target.Identity.Fingerprint()
	members := []ManagementPollMember{{JobID: "job", RunID: "run", NextSequence: 0}}
	hash, _ := ManagementCohortHash(members)
	now := time.Now().UTC()
	return ManagementPollGrant{Version: 1, PollID: "poll", ProjectID: "project", SourceID: "source", SourceRevision: 1, CollectorID: "collector", CollectorInstanceID: instance, LeaderJobID: "job", Nonce: "nonce", Pack: "kafka-offsets", Destination: "management://orders", ScopeHash: scope, IdentityHash: identity, Generation: "1", PollSequence: "1", CohortHash: hash, Cohort: members, IssuedAt: now, Deadline: now.Add(30 * time.Second), AuthorizationExpiresAt: now.Add(time.Minute)}
}
func managementTestSnapshot(g ManagementPollGrant) *analysis.MessagingManagementSnapshot {
	p := newManagementObservation(managementTestTarget(), g, ManagementCredentials{})
	now := time.Now().UTC()
	base := p.base(now, now, 0, 10, 0)
	snapshot, _ := analysis.NewUnavailableManagementSnapshot(base, "permission_denied")
	return snapshot
}
func TestManagementPollGrantBoundedIdentityAndExactCohort(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	grant := managementTestGrant("instance")
	signed, err := SignManagementPoll(grant, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = VerifyManagementPoll(signed, pub, "collector", "instance", time.Now(), false); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*ManagementPollGrant){"source": func(g *ManagementPollGrant) { g.SourceRevision = 0 }, "generation": func(g *ManagementPollGrant) { g.Generation = "01" }, "cohort": func(g *ManagementPollGrant) { g.Cohort = append(g.Cohort, g.Cohort[0]) }, "hash": func(g *ManagementPollGrant) { g.CohortHash = strings.Repeat("0", 64) }, "deadline": func(g *ManagementPollGrant) { g.Deadline = g.IssuedAt.Add(31 * time.Second) }, "availability": func(g *ManagementPollGrant) { g.UnavailableReason = "provider raw secret" }} {
		t.Run(name, func(t *testing.T) {
			bad := grant
			bad.Cohort = append([]ManagementPollMember(nil), grant.Cohort...)
			change(&bad)
			if _, err = SignManagementPoll(bad, key); err == nil {
				t.Fatal("invalid grant accepted")
			}
		})
	}
	if _, err = VerifyManagementPoll(signed, pub, "collector", "other", time.Now(), false); err == nil {
		t.Fatal("foreign instance accepted")
	}
	expired := grant.Deadline.Add(time.Hour)
	if _, err = VerifyManagementPoll(signed, pub, "collector", "instance", expired, false); err == nil {
		t.Fatal("expired grant admitted")
	}
	if _, err = VerifyManagementPoll(signed, pub, "collector", "instance", expired, true); err != nil {
		t.Fatal("completed durable grant identity rejected", err)
	}
	for _, poison := range []string{strings.Replace(signed.Payload, `"next_sequence":"0"`, `"next_sequence":null`, 1), strings.Replace(signed.Payload, `"next_sequence":"0"`, `"Next_sequence":"0"`, 1), strings.Replace(signed.Payload, `"generation":"1"`, `"generation":"1","Generation":"2"`, 1)} {
		bad := SignedManagementPoll{Payload: poison, Signature: base64.RawStdEncoding.EncodeToString(ed25519.Sign(key, []byte(poison)))}
		if _, err = VerifyManagementPoll(bad, pub, "collector", "instance", time.Now(), false); err == nil {
			t.Fatal("malformed signed grant accepted")
		}
	}
	if packs := ManagementPacks(); len(packs) != 1 || packs[0] != "kafka-offsets" {
		t.Fatal("compiled provider inventory is incorrect", packs)
	}
}
func TestManagementCredentialsPurposeAndOwnerBinding(t *testing.T) {
	private, _ := ecdh.X25519().GenerateKey(rand.Reader)
	grant := managementTestGrant("instance")
	credentials := ManagementCredentials{Version: 2, Purpose: ManagementCredentialPurpose, Mode: "basic", Basic: &ManagementBasicCredentials{Username: "observer", Password: "protected"}}
	sealed, err := SealManagementCredentials(grant, private.PublicKey().Bytes(), credentials)
	if err != nil {
		t.Fatal(err)
	}
	grant.Credentials = sealed
	got, err := OpenManagementCredentials(grant, private.Bytes(), time.Now())
	if err != nil || got.Basic.Password != "protected" {
		t.Fatal("sealed management credential failed", err)
	}
	for name, change := range map[string]func(*ManagementPollGrant){"revision": func(g *ManagementPollGrant) { g.SourceRevision++ }, "instance": func(g *ManagementPollGrant) { g.CollectorInstanceID = "other" }, "generation": func(g *ManagementPollGrant) { g.Generation = "2" }, "scope": func(g *ManagementPollGrant) { g.ScopeHash = strings.Repeat("a", 64) }, "identity": func(g *ManagementPollGrant) { g.IdentityHash = strings.Repeat("b", 64) }, "lease": func(g *ManagementPollGrant) { g.AuthorizationExpiresAt = g.AuthorizationExpiresAt.Add(time.Second) }} {
		t.Run(name, func(t *testing.T) {
			bad := grant
			change(&bad)
			if _, err = OpenManagementCredentials(bad, private.Bytes(), time.Now()); err == nil {
				t.Fatal("credential identity substitution accepted")
			}
		})
	}
	job := RunJob{ID: "job", ProjectID: "project", CollectorID: "collector", SourceID: "source", SourceRevision: 1, Pack: "ssh-linux", Destination: "ssh://host", LeaseExpiresAt: time.Now().Add(time.Minute), Credentials: sealed}
	if _, err = OpenCredentials(job, private.Bytes(), time.Now()); err == nil {
		t.Fatal("management credential accepted by legacy purpose")
	}
	legacy, err := SealCredentials(job, private.PublicKey().Bytes(), Credentials{Username: "ssh", Password: "still-valid"})
	if err != nil {
		t.Fatal(err)
	}
	job.Credentials = legacy
	if old, err := OpenCredentials(job, private.Bytes(), time.Now()); err != nil || old.Password != "still-valid" {
		t.Fatal("legacy envelope broken", err)
	}
}

type managementFixture struct {
	t                     *testing.T
	mu                    sync.Mutex
	jobs                  []RunJob
	grant                 ManagementPollGrant
	signed                SignedManagementPoll
	state                 string
	batches               []RunSampleBatch
	calls                 atomic.Int64
	releases              atomic.Int64
	heartbeats            atomic.Int64
	loseReply             bool
	unavailableReason     string
	authorizationLifetime time.Duration
	nilTerminalGrant      bool
	wrongAcceptedDigest   bool
	forceResponseState    string
	pub                   ed25519.PublicKey
	key                   ed25519.PrivateKey
	server                *httptest.Server
	agent                 *Agent
}

func newManagementFixture(t *testing.T, observer managementObserver) *managementFixture {
	f := &managementFixture{t: t, state: "collecting"}
	f.pub, f.key, _ = ed25519.GenerateKey(rand.Reader)
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.server.Close)
	target := managementTestTarget()
	cfg := Config{ControllerURL: f.server.URL, CollectorID: "collector", Token: "token", ControllerPublicKey: f.pub, Packs: []string{"go-runtime"}, SpoolDirectory: t.TempDir(), AdapterTargets: []AdapterTarget{{Pack: "kafka-offsets", Destination: "management://orders", Management: &target}}}
	var err error
	f.agent, err = New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	f.agent.cfg.Packs = append(f.agent.cfg.Packs, "kafka-offsets")
	f.agent.management.observers["kafka-offsets"] = observer
	t.Cleanup(f.agent.management.close)
	now := time.Now().UTC()
	scope, _ := target.Scope.Hash()
	identity, _ := target.Identity.Fingerprint()
	f.jobs = []RunJob{{ID: "job", ProjectID: "project", RunID: "run", CollectorID: "collector", SourceID: "source", SourceRevision: 1, Pack: "kafka-offsets", Destination: "management://orders", IntervalSeconds: 10, StartedAt: now, ExpiresAt: now.Add(time.Hour), LeaseExpiresAt: now.Add(time.Minute), Management: &ManagementJob{Version: 1, ScopeHash: scope, IdentityHash: identity}}}
	return f
}
func (f *managementFixture) handle(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/heartbeat") {
		f.heartbeats.Add(1)
		w.WriteHeader(200)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case strings.HasSuffix(r.URL.Path, "/jobs"):
		jobs := []SignedRunJob{}
		for _, j := range f.jobs {
			signed, _ := SignRunJob(j, f.key)
			jobs = append(jobs, signed)
		}
		json.NewEncoder(w).Encode(jobs)
	case strings.HasSuffix(r.URL.Path, "/management-polls"):
		var control ManagementPollControl
		if err := json.NewDecoder(r.Body).Decode(&control); err != nil {
			f.t.Error(err)
			w.WriteHeader(400)
			return
		}
		if control.Operation == "begin" {
			if f.grant.PollID == "" || f.grant.Nonce != control.Nonce {
				f.grant = managementTestGrant(control.InstanceID)
				f.grant.PollID = "poll-" + strconv.FormatInt(f.calls.Load()+1, 10)
				f.state = "collecting"
				leader := f.jobs[0]
				for _, job := range f.jobs {
					if strings.HasSuffix(r.URL.Path, "/jobs/"+job.ID+"/management-polls") {
						leader = job
						break
					}
				}
				f.grant.LeaderJobID = leader.ID
				f.grant.Nonce = control.Nonce
				f.grant.UnavailableReason = f.unavailableReason
				if f.authorizationLifetime > 0 {
					f.grant.AuthorizationExpiresAt = f.grant.IssuedAt.Add(f.authorizationLifetime)
				}
				f.grant.SourceRevision = leader.SourceRevision
				f.grant.IdentityHash = leader.Management.IdentityHash
				f.grant.ScopeHash = leader.Management.ScopeHash
				f.grant.Cohort = []ManagementPollMember{}
				for _, job := range f.jobs {
					if job.SourceRevision == leader.SourceRevision {
						f.grant.Cohort = append(f.grant.Cohort, ManagementPollMember{JobID: job.ID, RunID: job.RunID, NextSequence: job.NextSequence})
					}
				}
				f.grant.CohortHash, _ = ManagementCohortHash(f.grant.Cohort)
				f.signed, _ = SignManagementPoll(f.grant, f.key)
				f.calls.Add(1)
			}
		}
		if control.Operation == "abandon" {
			if !control.CompletionConfirmed {
				f.t.Error("missing completion proof")
			}
			f.releases.Add(1)
			if f.state != "accepted" {
				f.state = "abandoned"
			}
		}
		response := ManagementPollResponse{State: f.state, Grant: &f.signed}
		if f.forceResponseState != "" {
			response.State = f.forceResponseState
		}
		if f.nilTerminalGrant && (f.state == "accepted" || f.state == "abandoned") {
			response.Grant = nil
		}
		json.NewEncoder(w).Encode(response)
	case strings.HasSuffix(r.URL.Path, "/samples"):
		var batch RunSampleBatch
		if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
			f.t.Error(err)
			w.WriteHeader(400)
			return
		}
		if !snapshotMatchesGrant(batch.MessagingManagement, f.grant) || batch.EvidenceVersion != 2 || len(batch.Points) != 0 {
			f.t.Error("invalid durable typed batch")
		}
		f.batches = append(f.batches, batch)
		f.state = "accepted"
		digest, err := managementBatchCoreDigest(&batch)
		if err != nil {
			f.t.Error(err)
			w.WriteHeader(400)
			return
		}
		if f.wrongAcceptedDigest {
			digest = strings.Repeat("f", 64)
		}
		f.grant.AcceptedCoreDigest = digest
		f.grant.Credentials = nil
		f.signed, err = SignManagementPoll(f.grant, f.key)
		if err != nil {
			f.t.Error(err)
			w.WriteHeader(400)
			return
		}
		if f.loseReply {
			f.loseReply = false
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(200)
	default:
		w.WriteHeader(404)
	}
}
func waitManagement(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("management state did not complete")
		}
		time.Sleep(time.Millisecond)
	}
}
func (f *managementFixture) batchCount() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.batches) }
func TestManagementAsyncCoalescesCohortAndRetriesLostReply(t *testing.T) {
	var observed atomic.Int64
	f := newManagementFixture(t, managementObserverFunc(func(ctx context.Context, p *managementObservation) (*analysis.MessagingManagementSnapshot, error) {
		observed.Add(1)
		return analysis.NewUnavailableManagementSnapshot(p.base(time.Now(), time.Now(), 0, 10, 0), "permission_denied")
	}))
	f.mu.Lock()
	second := f.jobs[0]
	second.ID = "job2"
	second.RunID = "run2"
	f.jobs = append(f.jobs, second)
	f.loseReply = true
	f.mu.Unlock()
	if err := f.agent.runJobTick(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitManagement(t, func() bool { return f.batchCount() == 1 })
	waitManagement(t, func() bool {
		f.agent.management.mu.Lock()
		defer f.agent.management.mu.Unlock()
		return len(f.agent.management.pending) == 0 && len(f.agent.management.active) == 0
	})
	if observed.Load() != 1 || f.calls.Load() != 1 {
		t.Fatal("overlapping jobs did not coalesce", observed.Load(), f.calls.Load())
	}
	f.mu.Lock()
	batch := f.batches[0]
	cohort := len(f.grant.Cohort)
	f.mu.Unlock()
	if cohort != 2 || batch.MessagingManagement.UnavailableReason != "permission_denied" || len(batch.MessagingManagement.Rows) != 0 {
		t.Fatal("zero-point unavailable result lost", cohort, batch)
	}
	if err := f.agent.runJobTick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if observed.Load() != 1 {
		t.Fatal("heartbeat polled before cadence")
	}
}
func TestManagementIgnoredCancellationKeepsSourceChargedAndHeartbeatAvailable(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var observed atomic.Int64
	f := newManagementFixture(t, managementObserverFunc(func(ctx context.Context, p *managementObservation) (*analysis.MessagingManagementSnapshot, error) {
		observed.Add(1)
		_ = p.request(ctx, p.target.Endpoints[0], false, func(context.Context) error { close(entered); <-release; return nil })
		return analysis.NewUnavailableManagementSnapshot(p.base(time.Now(), time.Now(), 0, 10, 0), "permission_denied")
	}))
	if err := f.agent.runJobTick(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-entered
	f.mu.Lock()
	f.jobs = nil
	f.mu.Unlock()
	f.agent.tick(context.Background())
	f.agent.management.mu.Lock()
	active := len(f.agent.management.active)
	f.agent.management.mu.Unlock()
	if active != 1 || observed.Load() != 1 || f.releases.Load() != 0 || f.heartbeats.Load() == 0 {
		t.Fatal("cancellation prematurely released source or blocked heartbeat")
	}
	close(release)
	waitManagement(t, func() bool { return f.releases.Load() == 1 })
	waitManagement(t, func() bool {
		f.agent.management.mu.Lock()
		defer f.agent.management.mu.Unlock()
		return len(f.agent.management.active) == 0
	})
}
func TestManagementRequestBoundsAndActualReturnJoin(t *testing.T) {
	p := newManagementObservation(managementTestTarget(), managementTestGrant("instance"), ManagementCredentials{})
	var running atomic.Int64
	var peak atomic.Int64
	release := make(chan struct{})
	done := make(chan struct{}, 3)
	for i := 0; i < 3; i++ {
		go func() {
			_ = p.request(context.Background(), p.target.Endpoints[0], false, func(context.Context) error {
				n := running.Add(1)
				for {
					old := peak.Load()
					if n <= old || peak.CompareAndSwap(old, n) {
						break
					}
				}
				<-release
				running.Add(-1)
				return nil
			})
			done <- struct{}{}
		}()
	}
	waitManagement(t, func() bool { return running.Load() == 2 })
	joined := make(chan struct{})
	go func() { p.finish(); close(joined) }()
	select {
	case <-joined:
		t.Fatal("unreturned calls released")
	case <-time.After(10 * time.Millisecond):
	}
	close(release)
	for i := 0; i < 3; i++ {
		<-done
	}
	<-joined
	if peak.Load() > 2 {
		t.Fatal("request concurrency exceeded", peak.Load())
	}
	if err := p.request(context.Background(), p.target.Endpoints[0], false, func(context.Context) error { return nil }); err == nil {
		t.Fatal("completed poll admitted work")
	}
	p = newManagementObservation(managementTestTarget(), managementTestGrant("instance"), ManagementCredentials{})
	for i := 0; i < 16; i++ {
		if err := p.request(context.Background(), p.target.Endpoints[0], true, func(context.Context) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.request(context.Background(), p.target.Endpoints[0], true, func(context.Context) error { return nil }); err == nil {
		t.Fatal("page17 admitted")
	}
	if err := p.request(context.Background(), "https://foreign.example", false, func(context.Context) error { t.Fatal("unapproved callback ran"); return nil }); err == nil {
		t.Fatal("unapproved destination accepted")
	}
}
func TestManagementSpoolRecoveryRetriesWithoutObserverOrOwnerRelabel(t *testing.T) {
	f := newManagementFixture(t, managementObserverFunc(func(context.Context, *managementObservation) (*analysis.MessagingManagementSnapshot, error) {
		t.Fatal("recovery recollected")
		return nil, errors.New("unexpected")
	}))
	grant := managementTestGrant(f.agent.management.instance)
	grant.IssuedAt = grant.IssuedAt.Add(-time.Hour)
	grant.Deadline = grant.Deadline.Add(-time.Hour)
	grant.AuthorizationExpiresAt = grant.AuthorizationExpiresAt.Add(-time.Hour)
	signed, _ := SignManagementPoll(grant, f.key)
	batch := &RunSampleBatch{EvidenceVersion: 2, Points: []analysis.MetricPoint{}, Sequence: 0, MessagingManagement: managementTestSnapshot(grant), ManagementGrant: &signed}
	f.agent.management.mu.Lock()
	f.agent.management.pending[grant.PollID] = batch
	err := f.agent.management.saveSpoolLocked()
	f.agent.management.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.grant = grant
	f.grant.AcceptedCoreDigest, _ = managementBatchCoreDigest(batch)
	f.signed, _ = SignManagementPoll(f.grant, f.key)
	f.state = "accepted"
	f.jobs = nil
	f.mu.Unlock()
	restarted, err := New(f.agent.cfg)
	if err == nil {
		t.Fatal("explicit unsupported test pack should not survive production config")
	}
	cfg := f.agent.cfg
	cfg.Packs = []string{"go-runtime"}
	restarted, err = New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.management.close)
	if restarted.management.instance == grant.CollectorInstanceID {
		t.Fatal("startup instance reused")
	}
	if err = restarted.loadSpool(); err != nil {
		t.Fatal(err)
	}
	if err = restarted.management.loadSpool(); err != nil {
		t.Fatal(err)
	}
	if err = restarted.management.update(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	waitManagement(t, func() bool {
		restarted.management.mu.Lock()
		defer restarted.management.mu.Unlock()
		return len(restarted.management.pending) == 0
	})
	if f.batchCount() != 0 {
		t.Fatal("accepted durable retry reuploaded or recollected")
	}
}
func TestCollectorCombinedSpoolBudgetAndLegacyCompatibility(t *testing.T) {
	budget := newCollectorSpoolBudget()
	ids := []string{}
	for i := 0; i < 32; i++ {
		ids = append(ids, strconv.Itoa(i))
	}
	if err := budget.restore("legacy", []byte("{}"), ids); err != nil {
		t.Fatal(err)
	}
	if err := budget.reserve("management-active", []string{"extra"}); err == nil {
		t.Fatal("combined job33 admitted")
	}
	if err := budget.reserve("management-active", ids[:2]); err != nil {
		t.Fatal("same pending jobs double charged")
	}
	budget = newCollectorSpoolBudget()
	if err := budget.restore("legacy", make([]byte, maxCollectorSpoolBytes), nil); err != nil {
		t.Fatal(err)
	}
	if err := budget.restore("management", []byte("{}"), nil); err == nil {
		t.Fatal("combined byte limit ignored")
	}
	a, err := New(Config{ControllerURL: "http://localhost", CollectorID: "c", Token: "t", SpoolDirectory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	for _, document := range []string{`{}`, `{"version":2,"pending":{}}`, `{"version":3,"pending":{}}`} {
		if err = os.WriteFile(a.spoolPath(), []byte(document), 0600); err != nil {
			t.Fatal(err)
		}
		if err = a.loadSpool(); err != nil {
			t.Fatal("legacy spool failed", err)
		}
	}
	if err = os.WriteFile(a.spoolPath(), []byte(`{"version":4,"pending":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err = a.loadSpool(); err == nil {
		t.Fatal("unsupported spool silently discarded")
	}
}

func TestManagementCredentialsMatchesApprovedPrincipal(t *testing.T) {
	for _, tt := range []struct {
		name     string
		c        ManagementCredentials
		identity analysis.ManagementIdentity
		want     bool
	}{
		{"plain", ManagementCredentials{Version: 2, Purpose: ManagementCredentialPurpose, Mode: "sasl", SASL: &ManagementSASLCredentials{Mechanism: "plain", Username: "reader", Password: "protected"}}, analysis.ManagementIdentity{Mode: "sasl_plain", Principal: "reader", PolicyRevision: "1"}, true},
		{"different principal", ManagementCredentials{Version: 2, Purpose: ManagementCredentialPurpose, Mode: "basic", Basic: &ManagementBasicCredentials{Username: "other", Password: "protected"}}, analysis.ManagementIdentity{Mode: "basic", Principal: "reader", PolicyRevision: "1"}, false},
		{"missing bundle", ManagementCredentials{Version: 2, Purpose: ManagementCredentialPurpose, Mode: "sasl"}, analysis.ManagementIdentity{Mode: "sasl_plain", Principal: "reader", PolicyRevision: "1"}, false},
		{"mechanism mismatch", ManagementCredentials{Version: 2, Purpose: ManagementCredentialPurpose, Mode: "sasl", SASL: &ManagementSASLCredentials{Mechanism: "plain", Username: "reader", Password: "protected"}}, analysis.ManagementIdentity{Mode: "sasl_scram", Principal: "reader", PolicyRevision: "1"}, false},
		{"managed alias", ManagementCredentials{Version: 2, Purpose: ManagementCredentialPurpose, Mode: "managed"}, analysis.ManagementIdentity{Mode: "managed_identity", Principal: "reader", PolicyRevision: "1"}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.c.MatchesIdentity(tt.identity); got != tt.want {
				t.Fatal("identity match", got)
			}
		})
	}
}
func TestManagementSignedUnavailableAndMissingSecretSkipObserver(t *testing.T) {
	for _, tt := range []struct{ name, mode, reason string }{
		{"signed unavailable", "anonymous", "identity_mismatch"},
		{"missing basic secret", "basic", ""}, {"missing sasl secret", "sasl_plain", ""}, {"missing shared key", "shared_key", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var observed atomic.Int64
			f := newManagementFixture(t, managementObserverFunc(func(context.Context, *managementObservation) (*analysis.MessagingManagementSnapshot, error) {
				observed.Add(1)
				return nil, errors.New("must not observe")
			}))
			target := f.agent.cfg.AdapterTargets[0].Management
			target.Identity.Mode = tt.mode
			fingerprint, err := target.Identity.Fingerprint()
			if err != nil {
				t.Fatal(err)
			}
			f.jobs[0].Management.IdentityHash = fingerprint
			f.unavailableReason = tt.reason
			// Fixture creates grant from the currently signed job approval, as the controller does.
			f.grant = managementTestGrant(f.agent.management.instance)
			f.grant.PollID = "" // fresh begin initializes the immutable grant
			if err = f.agent.runJobTick(context.Background()); err != nil {
				t.Fatal(err)
			}
			waitManagement(t, func() bool { return f.batchCount() == 1 })
			if observed.Load() != 0 {
				t.Fatal("unavailable identity reached observer")
			}
			f.mu.Lock()
			batch := f.batches[0]
			f.mu.Unlock()
			want := tt.reason
			if want == "" {
				want = "permission_denied"
			}
			if batch.MessagingManagement.UnavailableReason != want || batch.MessagingManagement.RequestCount != 0 || len(batch.MessagingManagement.Rows) != 0 {
				t.Fatal("unavailable result", batch)
			}
		})
	}
}
func TestManagementAcceptedReceiptRequiresSignedExactCore(t *testing.T) {
	for _, mode := range []string{"missing terminal grant", "changed accepted core"} {
		t.Run(mode, func(t *testing.T) {
			f := newManagementFixture(t, managementObserverFunc(func(ctx context.Context, p *managementObservation) (*analysis.MessagingManagementSnapshot, error) {
				return analysis.NewUnavailableManagementSnapshot(p.base(time.Now(), time.Now(), 0, 10, 0), "permission_denied")
			}))
			f.loseReply = true
			f.nilTerminalGrant = mode == "missing terminal grant"
			f.wrongAcceptedDigest = mode == "changed accepted core"
			if err := f.agent.runJobTick(context.Background()); err != nil {
				t.Fatal(err)
			}
			waitManagement(t, func() bool { return f.batchCount() == 1 })
			time.Sleep(1100 * time.Millisecond)
			f.agent.management.mu.Lock()
			pending := len(f.agent.management.pending)
			f.agent.management.mu.Unlock()
			if pending != 1 {
				t.Fatal("unverified accepted receipt cleared durable evidence")
			}
			f.mu.Lock()
			f.nilTerminalGrant = false
			f.wrongAcceptedDigest = false
			f.grant.AcceptedCoreDigest, _ = managementBatchCoreDigest(&f.batches[0])
			f.signed, _ = SignManagementPoll(f.grant, f.key)
			f.mu.Unlock()
			waitManagement(t, func() bool {
				f.agent.management.mu.Lock()
				defer f.agent.management.mu.Unlock()
				return len(f.agent.management.pending) == 0
			})
			if f.batchCount() != 1 {
				t.Fatal("accepted reconciliation recollected or republished")
			}
			f.agent.management.mu.Lock()
			defer f.agent.management.mu.Unlock()
			if len(f.agent.management.rejected) != 0 {
				t.Fatal("accepted poll falsely audited rejected")
			}
		})
	}
}
func TestManagementTerminalGrantNeverAuthorizesObservation(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	g := managementTestGrant("instance")
	g.AcceptedCoreDigest = strings.Repeat("a", 64)
	signed, err := SignManagementPoll(g, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = VerifyManagementPoll(signed, pub, "collector", "instance", time.Now(), false); err == nil {
		t.Fatal("terminal grant authorized fresh observation")
	}
	if _, err = VerifyManagementPoll(signed, pub, "collector", "instance", time.Now(), true); err != nil {
		t.Fatal(err)
	}
	g.AcceptedCoreDigest = "wrong"
	if _, err = SignManagementPoll(g, key); err == nil {
		t.Fatal("malformed accepted digest signed")
	}
}

func TestManagementBeginRequiresCurrentCollectingAuthority(t *testing.T) {
	for _, state := range []string{"accepted", "abandoned", "released", "draining", "expired"} {
		t.Run(state, func(t *testing.T) {
			var observed atomic.Int64
			f := newManagementFixture(t, managementObserverFunc(func(context.Context, *managementObservation) (*analysis.MessagingManagementSnapshot, error) {
				observed.Add(1)
				return nil, nil
			}))
			g := managementTestGrant(f.agent.management.instance)
			f.grant = g
			f.state = state
			if state == "expired" {
				f.state = "collecting"
				g.IssuedAt = g.IssuedAt.Add(-time.Hour)
				g.Deadline = g.Deadline.Add(-time.Hour)
				g.AuthorizationExpiresAt = g.AuthorizationExpiresAt.Add(-time.Hour)
				f.grant = g
			}
			f.signed, _ = SignManagementPoll(g, f.key)
			signed, _ := SignRunJob(f.jobs[0], f.key)
			c, err := f.agent.management.candidate(signed, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			c.nonce = g.Nonce
			source := managementSourceKey(g.ProjectID, g.SourceID)
			f.agent.management.nonces[source+"\x00"+c.key] = g.Nonce
			task := &managementTask{candidate: c, done: make(chan struct{})}
			f.agent.management.run(context.Background(), source, task)
			if observed.Load() != 0 || f.batchCount() != 0 {
				t.Fatal("terminal/expired begin recollected")
			}
			if state == "accepted" || state == "abandoned" || state == "released" {
				if f.agent.management.nonces[source+"\x00"+c.key] != "" {
					t.Fatal("terminal nonce retained")
				}
			}
		})
	}
}
func TestManagementIndependentWakeAndRevisionSerialization(t *testing.T) {
	var observed atomic.Int64
	entered := make(chan int64, 4)
	release := make(chan struct{})
	f := newManagementFixture(t, managementObserverFunc(func(ctx context.Context, p *managementObservation) (*analysis.MessagingManagementSnapshot, error) {
		n := observed.Add(1)
		entered <- p.grant.SourceRevision
		if n == 1 {
			<-release
		}
		return analysis.NewUnavailableManagementSnapshot(p.base(time.Now(), time.Now(), 0, 10, 0), "permission_denied")
	}))
	second := f.jobs[0]
	second.ID = "job2"
	second.SourceRevision = 2
	f.jobs = append(f.jobs, second)
	if err := f.agent.runJobTick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if revision := <-entered; revision != 1 {
		t.Fatal("initial revision", revision)
	}
	time.Sleep(20 * time.Millisecond)
	if observed.Load() != 1 {
		t.Fatal("overlapping source polls")
	}
	close(release)
	select {
	case revision := <-entered:
		if revision != 2 {
			t.Fatal("second revision", revision)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("next revision depended on heartbeat")
	}
	waitManagement(t, func() bool {
		f.agent.management.mu.Lock()
		defer f.agent.management.mu.Unlock()
		return len(f.agent.management.active) == 0
	})
	s := f.agent.management
	s.mu.Lock()
	for key := range s.next {
		s.next[key] = time.Now().Add(20 * time.Millisecond)
	}
	s.signalWake()
	s.mu.Unlock()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("cadence timer depended on heartbeat")
	}
	if f.heartbeats.Load() != 0 {
		t.Fatal("test unexpectedly needed heartbeat")
	}
}
func TestManagementSchedulingStatePrunesChurnButKeepsAuthority(t *testing.T) {
	a, err := New(Config{ControllerURL: "http://localhost", CollectorID: "c", Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	s := newManagementScheduler(a, "instance")
	a.management = s
	g := managementTestGrant("instance")
	signed, _ := SignManagementPoll(g, func() ed25519.PrivateKey { _, k, _ := ed25519.GenerateKey(rand.Reader); return k }())
	source := managementSourceKey(g.ProjectID, g.SourceID)
	key := "1\x00" + g.ScopeHash + "\x00" + g.IdentityHash
	s.pending[g.PollID] = &RunSampleBatch{ManagementGrant: &signed}
	for i := 0; i < 1000; i++ {
		old := "removed-" + strconv.Itoa(i)
		s.next[old] = time.Now()
		s.nonces[old] = "n"
		s.last[old] = "k"
		s.cadence[old] = managementCadence{at: time.Now(), interval: 10}
	}
	s.next[source+"\x00"+key] = time.Now()
	s.nonces[source+"\x00"+key] = "still-owned"
	s.last[source] = key
	s.cadence[source] = managementCadence{at: time.Now(), interval: 10}
	s.pruneLocked()
	if len(s.next) != 1 || len(s.nonces) != 1 || len(s.last) != 1 || len(s.cadence) != 1 {
		t.Fatal("churn retained or durable authority discarded")
	}
	delete(s.pending, g.PollID)
	s.pruneLocked()
	if len(s.next)+len(s.nonces)+len(s.last)+len(s.cadence) != 0 {
		t.Fatal("released state retained")
	}
	now := time.Now()
	if n := s.missedTicks("source", now, now.Add(25*time.Second), 10); n != 2 {
		t.Fatal("blocked poll missed ticks", n)
	}
	if n := s.missedTicks("source", now.Add(30*time.Second), now.Add(30*time.Second), 10); n != 0 {
		t.Fatal("missed ticks counted twice", n)
	}
	if n := s.missedTicks("source", now.Add(80*time.Second), now.Add(80*time.Second), 10); n != 4 {
		t.Fatal("blocked publication ticks lost", n)
	}
}
func TestManagementSharedOriginBoundsAndSolaceThrottle(t *testing.T) {
	target := managementTestTarget()
	target.Endpoints = []string{"https://same.example/a", "https://same.example:443/b"}
	gate := newManagementOriginGate([]AdapterTarget{{Management: &target}})
	var active, peak atomic.Int64
	release := make(chan struct{})
	done := make(chan struct{}, 4)
	for i := 0; i < 4; i++ {
		go func(i int) {
			p := newManagementObservation(target, managementTestGrant("instance"), ManagementCredentials{})
			p.origins = gate
			_ = p.request(context.Background(), target.Endpoints[i%2], false, func(context.Context) error {
				n := active.Add(1)
				for {
					old := peak.Load()
					if n <= old || peak.CompareAndSwap(old, n) {
						break
					}
				}
				<-release
				active.Add(-1)
				return nil
			})
			done <- struct{}{}
		}(i)
	}
	waitManagement(t, func() bool { return active.Load() == 2 })
	if peak.Load() != 2 {
		t.Fatal("origin admission", peak.Load())
	}
	close(release)
	for i := 0; i < 4; i++ {
		<-done
	}
	if peak.Load() > 2 {
		t.Fatal("origin exceeded2 actual requests")
	}
	r, err := gate.admit(context.Background(), target.Endpoints[0], "solace-poll1", "solace-semp")
	if err != nil {
		t.Fatal(err)
	}
	r()
	r, err = gate.admit(context.Background(), target.Endpoints[1], "solace-poll1", "solace-semp")
	if err != nil {
		t.Fatal("same poll page throttled", err)
	}
	r()
	if _, err = gate.admit(context.Background(), target.Endpoints[0], "solace-poll2", "solace-semp"); err == nil {
		t.Fatal("distinct source bypassed origin throttle")
	}
}
func TestManagementFabricatedAbandonCannotRejectAcceptedEvidence(t *testing.T) {
	f := newManagementFixture(t, managementObserverFunc(func(ctx context.Context, p *managementObservation) (*analysis.MessagingManagementSnapshot, error) {
		return analysis.NewUnavailableManagementSnapshot(p.base(time.Now(), time.Now(), 0, 10, 0), "permission_denied")
	}))
	f.loseReply = true
	if err := f.agent.runJobTick(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitManagement(t, func() bool { return f.batchCount() == 1 })
	f.mu.Lock()
	f.forceResponseState = "abandoned"
	f.mu.Unlock()
	time.Sleep(1100 * time.Millisecond)
	s := f.agent.management
	s.mu.Lock()
	pending, rejected := len(s.pending), len(s.rejected)
	s.mu.Unlock()
	if pending != 1 || rejected != 0 {
		t.Fatal("fabricated abandon discarded signed accepted core", pending, rejected)
	}
	f.mu.Lock()
	f.forceResponseState = ""
	f.mu.Unlock()
	waitManagement(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return len(s.pending) == 0 })
}

func TestCollectorSpoolCombinedStartupJobsAndCorruption(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	cfg := Config{ControllerURL: "http://localhost", CollectorID: "collector", Token: "t", ControllerPublicKey: pub, SpoolDirectory: t.TempDir()}
	a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	value := 1.0
	point := analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: "p", Source: "s", MetricName: "cpu", Kind: analysis.KindGauge, Value: &value, Quality: analysis.QualityValid, TimestampUTC: time.Now().UTC()}
	old := map[string]*RunSampleBatch{}
	for i := 0; i < 32; i++ {
		old["old-"+strconv.Itoa(i)] = &RunSampleBatch{Sequence: 0, Points: []analysis.MetricPoint{point}}
	}
	body, _ := json.Marshal(collectorSpool{Version: 2, Pending: old})
	if err = os.WriteFile(a.spoolPath(), body, 0600); err != nil {
		t.Fatal(err)
	}
	g := managementTestGrant("prior-instance")
	signed, _ := SignManagementPoll(g, key)
	batch := &RunSampleBatch{Sequence: 0, EvidenceVersion: 2, Points: []analysis.MetricPoint{}, ManagementGrant: &signed, MessagingManagement: managementTestSnapshot(g)}
	document, _ := json.Marshal(managementSpool{Version: 3, Pending: map[string]*RunSampleBatch{g.PollID: batch}, Rejected: []managementSpoolRejection{}})
	if err = os.WriteFile(a.management.spoolPath(), document, 0600); err != nil {
		t.Fatal(err)
	}
	if err = a.loadSpool(); err != nil {
		t.Fatal(err)
	}
	if err = a.management.loadSpool(); err == nil {
		t.Fatal("combined durable job33 silently admitted")
	}
	for _, bad := range []string{`{"version":2}`, `{"version":3,"pending":null}`, `{"version":3,"pending":[]}`} {
		if err = os.WriteFile(a.spoolPath(), []byte(bad), 0600); err != nil {
			t.Fatal(err)
		}
		if err = a.loadSpool(); err == nil {
			t.Fatal("corrupt versioned spool became empty", bad)
		}
	}
}

func TestCollectorSpoolFreshByteReservationAndInstalledErrorAccounting(t *testing.T) {
	b := newCollectorSpoolBudget()
	if err := b.restore("legacy", make([]byte, maxCollectorSpoolBytes-(1<<20)), nil); err != nil {
		t.Fatal(err)
	}
	if err := b.reserveFresh("management-active:source", []string{"job"}); err == nil {
		t.Fatal("fresh poll admitted without complete batch headroom")
	}
	b = newCollectorSpoolBudget()
	if err := b.reserveFresh("management-active:source", []string{"job"}); err != nil {
		t.Fatal(err)
	}
	if err := b.transferFresh("management-active:source", "management-pending:poll", []string{"job"}); err != nil {
		t.Fatal(err)
	}
	b.release("management-active:source")
	if b.activeBytes["management-pending:poll"] == 0 {
		t.Fatal("completed unpersisted evidence lost byte reservation")
	}
	injected := errors.New("directory fsync unavailable")
	b.write = func(string, []byte) (bool, error) { return false, injected }
	if err := b.persist("management", "controlled", []byte("{}"), []string{"job"}, "management-pending:poll"); !errors.Is(err, injected) {
		t.Fatal(err)
	}
	if b.activeBytes["management-pending:poll"] == 0 || b.fileBytes["management"] != 0 {
		t.Fatal("failed pre-rename write changed accounting")
	}
	b.write = func(string, []byte) (bool, error) { return true, injected }
	if err := b.persist("management", "controlled", []byte("{}"), []string{"job"}, "management-pending:poll"); !errors.Is(err, injected) {
		t.Fatal(err)
	}
	if b.fileBytes["management"] != 2 || b.activeBytes["management-pending:poll"] != 0 {
		t.Fatal("installed write wasn't transferred despite fsync error")
	}
	if err := b.restore("legacy", make([]byte, maxCollectorSpoolBytes-1), nil); err == nil {
		t.Fatal("post-rename bytes were omitted from combined limit")
	}
}
func TestManagementCapacityExhaustionPreventsFreshGrantAndObserver(t *testing.T) {
	var observed atomic.Int64
	f := newManagementFixture(t, managementObserverFunc(func(context.Context, *managementObservation) (*analysis.MessagingManagementSnapshot, error) {
		observed.Add(1)
		return nil, nil
	}))
	if err := f.agent.spoolBudget.restore("legacy", make([]byte, maxCollectorSpoolBytes), nil); err != nil {
		t.Fatal(err)
	}
	signed, _ := SignRunJob(f.jobs[0], f.key)
	if err := f.agent.management.update(context.Background(), []SignedRunJob{signed}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if observed.Load() != 0 || f.calls.Load() != 0 {
		t.Fatal("full spool admitted fresh grant/collection")
	}
}

func TestManagementLocalAliasRejectsHiddenPortAndURLSyntax(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	target := managementTestTarget()
	for _, alias := range []string{"management://broker:", "management://[broker]", "management://broker?", "management://broker#", "management://børker", "management://broker:443", "management://user@broker", "management://broker/path"} {
		cfg := Config{ControllerURL: "http://localhost", CollectorID: "c", Token: "t", ControllerPublicKey: pub, SpoolDirectory: t.TempDir(), AdapterTargets: []AdapterTarget{{Pack: "kafka-offsets", Destination: alias, Management: &target}}}
		if _, err := New(cfg); err == nil {
			t.Fatal("malformed local alias admitted", alias)
		}
	}
}

func TestManagementReadinessDoesNotWaitForSpoolSchedulingLock(t *testing.T) {
	f := newManagementFixture(t, managementObserverFunc(func(context.Context, *managementObservation) (*analysis.MessagingManagementSnapshot, error) {
		return nil, nil
	}))
	s := f.agent.management
	s.mu.Lock()
	done := make(chan []ManagementReadinessScope, 1)
	go func() { done <- s.readiness() }()
	select {
	case scopes := <-done:
		s.mu.Unlock()
		if len(scopes) != 1 {
			t.Fatal("pure readiness lost approval")
		}
	case <-time.After(time.Second):
		s.mu.Unlock()
		t.Fatal("heartbeat waited for asynchronous spool lock")
	}
}
func TestManagementLocalApprovalOwnsInputCopies(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	target := managementTestTarget()
	targets := []AdapterTarget{{Pack: "kafka-offsets", Destination: "management://orders", Management: &target}}
	a, err := New(Config{ControllerURL: "http://localhost", CollectorID: "c", Token: "t", ControllerPublicKey: pub, SpoolDirectory: t.TempDir(), AdapterTargets: targets})
	if err != nil {
		t.Fatal(err)
	}
	target.Scope.Entities[0].Kafka.Topic = "changed"
	target.Identity.Principal = "changed"
	target.Endpoints[0] = "https://foreign.example"
	targets[0].Destination = "management://foreign"
	got := a.cfg.AdapterTargets[0]
	if got.Destination != "management://orders" || got.Management.Scope.Entities[0].Kafka.Topic != "orders" || got.Management.Identity.Principal != "controlled-reader" || got.Management.Endpoints[0] != "https://approved.example.test:443" {
		t.Fatal("caller mutated immutable local management approval")
	}
}

func TestManagementDormantTargetDoesNotPoisonLegacyReadiness(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	target := managementTestTarget()
	a, err := New(Config{ControllerURL: "http://localhost", CollectorID: "c", Token: "t", ControllerPublicKey: pub, SpoolDirectory: t.TempDir(), Packs: []string{"go-runtime"}, AdapterTargets: []AdapterTarget{{Pack: "kafka-offsets", Destination: "management://orders", Management: &target}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(a.approvedDestinations()) != 0 || len(a.management.readiness()) != 0 {
		t.Fatal("unavailable management observer polluted host-only readiness")
	}
	a.cfg.Packs = append(a.cfg.Packs, "kafka-offsets")
	if len(a.approvedDestinations()) != 0 {
		t.Fatal("configured name alone advertised implementation")
	}
	a.management.observers["kafka-offsets"] = managementObserverFunc(func(context.Context, *managementObservation) (*analysis.MessagingManagementSnapshot, error) {
		return nil, nil
	})
	if got := a.approvedDestinations(); len(got) != 1 || got[0] != "management://orders" {
		t.Fatal("controlled available scope was omitted", got)
	}
}
