package collectoragent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

type managementCandidate struct {
	job      RunJob
	signed   SignedRunJob
	target   ManagementTarget
	observer managementObserver
	cohort   []string
	key      string
	nonce    string
}
type managementTask struct {
	candidate managementCandidate
	cancel    context.CancelFunc
	done      chan struct{}
	pending   *RunSampleBatch
}
type managementScheduler struct {
	agent     *Agent
	instance  string
	observers map[string]managementObserver
	mu        sync.Mutex
	root      context.Context
	cancel    context.CancelFunc
	closed    bool
	active    map[string]*managementTask
	jobs      map[string][]managementCandidate
	next      map[string]time.Time
	last      map[string]string
	nonces    map[string]string
	pending   map[string]*RunSampleBatch
	rejected  []managementSpoolRejection
	wake      chan struct{}
	wakeDone  chan struct{}
	cadence   map[string]managementCadence
	origins   *managementOriginGate
}

func newManagementScheduler(a *Agent, instance string) *managementScheduler {
	return &managementScheduler{agent: a, instance: instance, observers: registeredManagementObservers(), active: map[string]*managementTask{}, jobs: map[string][]managementCandidate{}, next: map[string]time.Time{}, last: map[string]string{}, nonces: map[string]string{}, wake: make(chan struct{}, 1), cadence: map[string]managementCadence{}, origins: newManagementOriginGate(a.cfg.AdapterTargets), pending: map[string]*RunSampleBatch{}}
}
func managementSourceKey(project, source string) string { return project + "\x00" + source }
func managementCandidateKey(job RunJob) string {
	return strconv.FormatInt(job.SourceRevision, 10) + "\x00" + job.Management.ScopeHash + "\x00" + job.Management.IdentityHash
}

// Readiness uses only the immutable local approval and compiled observer
// inventory; disk I/O in the asynchronous spool cannot hold up a heartbeat.
func (s *managementScheduler) readiness() []ManagementReadinessScope {
	result := []ManagementReadinessScope{}
	seen := map[string]bool{}
	for _, t := range s.agent.cfg.AdapterTargets {
		if t.Management == nil || s.observers[t.Pack] == nil || !hasConfiguredPack(s.agent.cfg.Packs, t.Pack) {
			continue
		}
		if s.observers[t.Pack].validateTarget(t.Management) != nil {
			continue
		}
		scope, e := t.Management.Scope.Hash()
		if e != nil {
			continue
		}
		identity, e := t.Management.Identity.Fingerprint()
		if e != nil {
			continue
		}
		key := scope + identity
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, ManagementReadinessScope{scope, identity})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].ScopeHash+result[i].IdentityHash < result[j].ScopeHash+result[j].IdentityHash
	})
	return result
}
func hasConfiguredPack(packs []string, pack string) bool {
	for _, p := range packs {
		if p == pack {
			return true
		}
	}
	return false
}
func (s *managementScheduler) candidate(signed SignedRunJob, now time.Time) (managementCandidate, error) {
	job, err := VerifyRunJob(signed, s.agent.cfg.ControllerPublicKey, s.agent.cfg.CollectorID, now)
	if err != nil {
		return managementCandidate{}, err
	}
	if job.Management == nil {
		return managementCandidate{}, errors.New("management approval is missing")
	}
	target, ok := s.agent.adapterTarget(job)
	observer := s.observers[job.Pack]
	if !ok || target.Management == nil || observer == nil || !hasConfiguredPack(s.agent.cfg.Packs, job.Pack) {
		return managementCandidate{}, errors.New("management pack is unavailable or not locally approved")
	}
	if err := observer.validateTarget(target.Management); err != nil {
		return managementCandidate{}, err
	}
	scope, err := target.Management.Scope.Hash()
	if err != nil {
		return managementCandidate{}, err
	}
	identity, err := target.Management.Identity.Fingerprint()
	if err != nil {
		return managementCandidate{}, err
	}
	if scope != job.Management.ScopeHash || identity != job.Management.IdentityHash || job.IntervalSeconds < managementMinimumInterval(job.Pack, *target.Management) {
		return managementCandidate{}, errors.New("management source scope or identity is not locally approved")
	}
	return managementCandidate{job: job, signed: signed, target: target.Management.clone(), observer: observer, key: managementCandidateKey(job)}, nil
}
func (s *managementScheduler) update(ctx context.Context, envelopes []SignedRunJob) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("management scheduler is closed")
	}
	if s.root == nil {
		s.root, s.cancel = context.WithCancel(ctx)
		s.wakeDone = make(chan struct{})
		go s.wakeLoop()
	}
	groups := map[string]map[string]*managementCandidate{}
	var failures []error
	for _, signed := range envelopes {
		c, err := s.candidate(signed, time.Now().UTC())
		if err != nil {
			failures = append(failures, err)
			continue
		}
		source := managementSourceKey(c.job.ProjectID, c.job.SourceID)
		if groups[source] == nil {
			groups[source] = map[string]*managementCandidate{}
		}
		existing := groups[source][c.key]
		if existing == nil {
			copy := c
			copy.cohort = []string{}
			existing = &copy
			groups[source][c.key] = existing
		}
		existing.cohort = append(existing.cohort, c.job.ID)
		if c.job.ID < existing.job.ID {
			existing.job = c.job
			existing.signed = c.signed
		}
	}
	s.jobs = map[string][]managementCandidate{}
	for source, keys := range groups {
		for _, c := range keys {
			s.jobs[source] = append(s.jobs[source], *c)
		}
		sort.Slice(s.jobs[source], func(i, j int) bool { return s.jobs[source][i].key < s.jobs[source][j].key })
	}
	for source, task := range s.active {
		matched := false
		for _, c := range s.jobs[source] {
			matched = matched || c.key == task.candidate.key
		}
		if !matched {
			task.cancel()
		}
	}
	for source := range s.jobs {
		s.startLocked(source)
	}
	// Recovered completed polls are retried without any new observer execution.
	for _, batch := range s.pending {
		g, err := VerifyManagementPoll(*batch.ManagementGrant, s.agent.cfg.ControllerPublicKey, s.agent.cfg.CollectorID, batch.MessagingManagement.CollectorInstanceID, time.Now().UTC(), true)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		source := managementSourceKey(g.ProjectID, g.SourceID)
		if s.active[source] != nil {
			continue
		}
		job := RunJob{ID: g.LeaderJobID, ProjectID: g.ProjectID, SourceID: g.SourceID, SourceRevision: g.SourceRevision, Pack: g.Pack, Destination: g.Destination, Management: &ManagementJob{Version: 1, ScopeHash: g.ScopeHash, IdentityHash: g.IdentityHash}}
		c := managementCandidate{job: job, signed: batch.Job, key: managementCandidateKey(job)}
		taskCtx, cancel := context.WithCancel(s.root)
		task := &managementTask{candidate: c, cancel: cancel, done: make(chan struct{}), pending: batch}
		s.active[source] = task
		go s.runRecovered(taskCtx, source, task, g)
	}
	s.pruneLocked()
	s.signalWake()
	return errors.Join(failures...)
}
func (s *managementScheduler) startLocked(source string) {
	if s.closed || s.root == nil || s.root.Err() != nil || s.active[source] != nil {
		return
	}
	// A durable poll owns the source until its retry/drain is resolved.
	for _, b := range s.pending {
		if b.MessagingManagement != nil && managementSourceKeyFromBatch(b) == source {
			return
		}
	}
	candidates := s.jobs[source]
	now := time.Now().UTC()
	valid := candidates[:0]
	for _, c := range candidates {
		if c.job.ExpiresAt.After(now) && c.job.LeaseExpiresAt.After(now) {
			valid = append(valid, c)
		}
	}
	candidates = valid
	s.jobs[source] = valid
	if len(candidates) == 0 {
		return
	}
	index := 0
	for i, c := range candidates {
		if c.key > s.last[source] {
			index = i
			break
		}
		if i == len(candidates)-1 {
			index = 0
		}
	}
	selected := -1
	for n := 0; n < len(candidates); n++ {
		i := (index + n) % len(candidates)
		if !time.Now().Before(s.next[source+"\x00"+candidates[i].key]) {
			selected = i
			break
		}
	}
	if selected < 0 {
		return
	}
	c := candidates[selected]
	nonceKey := source + "\x00" + c.key
	nonce := s.nonces[nonceKey]
	if nonce == "" {
		var err error
		nonce, err = newCollectorInstanceID()
		if err != nil {
			return
		}
		s.nonces[nonceKey] = nonce
	}
	c.nonce = nonce
	if err := s.agent.spoolBudget.reserveFresh("management-active:"+source, c.cohort); err != nil {
		return
	}
	taskCtx, cancel := context.WithCancel(s.root)
	task := &managementTask{candidate: c, cancel: cancel, done: make(chan struct{})}
	s.active[source] = task
	s.last[source] = c.key
	go s.run(taskCtx, source, task)
}
func managementSourceKeyFromBatch(b *RunSampleBatch) string {
	if b == nil || b.ManagementGrant == nil {
		return ""
	}
	var grant ManagementPollGrant
	if json.Unmarshal([]byte(b.ManagementGrant.Payload), &grant) != nil {
		return ""
	}
	return managementSourceKey(grant.ProjectID, grant.SourceID)
}
func (s *managementScheduler) finish(source string, task *managementTask) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active[source] == task {
		delete(s.active, source)
		key := source + "\x00" + task.candidate.key
		if s.next[key].Before(time.Now().Add(time.Second)) {
			s.next[key] = time.Now().Add(time.Second)
		}
		s.agent.spoolBudget.release("management-active:" + source)
		s.startLocked(source)
		s.pruneLocked()
		s.signalWake()
	}
	close(task.done)
}
func (s *managementScheduler) control(ctx context.Context, leader string, c ManagementPollControl) (ManagementPollResponse, error) {
	var response ManagementPollResponse
	err := s.agent.jobRequest(ctx, http.MethodPost, "jobs/"+url.PathEscape(leader)+"/management-polls", c, &response)
	return response, err
}
func (s *managementScheduler) run(ctx context.Context, source string, task *managementTask) {
	defer s.finish(source, task)
	c := task.candidate
	response, err := s.control(ctx, c.job.ID, ManagementPollControl{Operation: "begin", InstanceID: s.instance, Nonce: c.nonce})
	if err != nil || response.Grant == nil {
		return
	}
	grant, err := VerifyManagementPoll(*response.Grant, s.agent.cfg.ControllerPublicKey, s.agent.cfg.CollectorID, s.instance, time.Now().UTC(), true)
	if err != nil {
		return
	}
	if err = s.matchGrant(c, grant); err != nil {
		s.abandon(grant)
		return
	}
	if response.State != "collecting" {
		if response.State == "accepted" || response.State == "abandoned" || response.State == "released" {
			s.mu.Lock()
			delete(s.nonces, source+"\x00"+c.key)
			s.mu.Unlock()
		}
		return
	}
	if _, err = VerifyManagementPoll(*response.Grant, s.agent.cfg.ControllerPublicKey, s.agent.cfg.CollectorID, s.instance, time.Now().UTC(), false); err != nil {
		s.abandon(grant)
		return
	}
	ids := make([]string, 0, len(grant.Cohort))
	for _, m := range grant.Cohort {
		ids = append(ids, m.JobID)
	}
	if err = s.agent.spoolBudget.reserve("management-active:"+source, ids); err != nil {
		s.abandon(grant)
		return
	}
	started := time.Now()
	// The signed credential lifetime can end before the collection deadline.
	// Expiry cancels provider work; retained resources still join physical cleanup.
	deadline := minTime(minTime(grant.Deadline, grant.AuthorizationExpiresAt), started.Add(8*time.Second))
	pollCtx, cancel := context.WithDeadline(ctx, deadline)
	reason := grant.UnavailableReason
	credentials := ManagementCredentials{Version: 2, Purpose: ManagementCredentialPurpose, Mode: managementCredentialMode(c.target.Identity.Mode)}
	if reason == "" {
		if grant.Credentials != nil {
			credentials, err = OpenManagementCredentials(grant, s.agent.cfg.CredentialPrivateKey, time.Now().UTC())
			if err != nil || !managementCredentialsMatchIdentity(credentials, c.target.Identity) {
				reason = "permission_denied"
			}
		} else if c.target.Identity.CredentialRef != "" {
			reason = "permission_denied"
		}
	}
	if reason == "" && !credentials.MatchesIdentity(c.target.Identity) {
		reason = "permission_denied"
	}
	poll := newManagementObservation(c.target, grant, credentials)
	poll.origins = s.origins
	var snapshot *analysis.MessagingManagementSnapshot
	if reason == "" && managementPollAuthorityError(pollCtx, grant) == nil {
		snapshot, err = observeManagement(pollCtx, c.observer, poll)
	}
	poll.finish()
	elapsed := time.Since(started)
	completed := time.Now()
	requests, pages := poll.counts()
	base := poll.base(started, completed, elapsed, c.job.IntervalSeconds, s.missedTicks(source, started, completed, c.job.IntervalSeconds))
	base.RequestCount = requests
	base.PageCount = pages
	if completionReason := managementPollCompletionReason(ctx, pollCtx, grant, completed); completionReason != "" {
		reason = completionReason
	}
	cancel()
	if reason == "" && err != nil {
		reason = "invalid"
		var typed *managementObservationError
		if errors.As(err, &typed) {
			reason = typed.reason
		}
		var budget *managementBudgetError
		if errors.As(err, &budget) {
			reason = "budget_exhausted"
		}
	}
	if reason == "" {
		if snapshot == nil || snapshot.ValidateFor(c.target.Scope, c.target.Identity) != nil {
			reason = "invalid"
		} else {
			// Providers contribute rows and coverage only; admission and request timing
			// belong to the source scheduler, never arbitrary callback attribution.
			base.Rows = snapshot.Clone().Rows
			base.InventoryComplete = snapshot.InventoryComplete
			base.Incomplete = snapshot.Incomplete
			base.UnavailableReason = snapshot.UnavailableReason
			snapshot = &base
		}
	}
	if reason != "" {
		snapshot, err = analysis.NewUnavailableManagementSnapshot(base, reason)
		if err != nil {
			snapshot, err = analysis.NewUnavailableManagementSnapshot(base, "invalid")
		}
		if err != nil {
			s.abandon(grant)
			return
		}
	}
	if err = snapshot.ValidateFor(c.target.Scope, c.target.Identity); err != nil {
		s.abandon(grant)
		return
	}
	sequence := int64(-1)
	for _, m := range grant.Cohort {
		if m.JobID == grant.LeaderJobID {
			sequence = m.NextSequence
		}
	}
	batch := &RunSampleBatch{Job: c.signed, Sequence: sequence, Points: []analysis.MetricPoint{}, EvidenceVersion: ManagementEvidenceVersion, MessagingManagement: snapshot, ManagementGrant: response.Grant}
	encoded, encodeErr := json.Marshal(batch)
	if encodeErr != nil || len(encoded) > 1<<20 {
		s.abandon(grant)
		return
	}
	if err = s.agent.spoolBudget.transferFresh("management-active:"+source, "management-pending:"+grant.PollID, ids); err != nil {
		s.abandon(grant)
		return
	}
	s.mu.Lock()
	task.pending = batch
	s.pending[grant.PollID] = batch
	_ = s.saveSpoolLocked()
	s.mu.Unlock() // evidence remains in memory; retry persists before transport
	s.publish(ctx, source, task, grant)
}
func observeManagement(ctx context.Context, observer managementObserver, poll *managementObservation) (snapshot *analysis.MessagingManagementSnapshot, err error) {
	defer func() {
		if recover() != nil {
			snapshot = nil
			err = errors.New("management observer failed")
		}
	}()
	return observer.observe(ctx, poll)
}

// Completed evidence is qualified only after physical cleanup. Check both the
// signed times and actual context deadlines even if timer delivery is delayed.
// Retry publication of already qualified durable evidence uses its original
// capture/receipt contract and does not start new provider work.
func managementPollCompletionReason(parent, poll context.Context, grant ManagementPollGrant, completed time.Time) string {
	if managementContextError(parent) != nil {
		return "cancelled"
	}
	if managementContextError(poll) != nil || !grant.Deadline.After(completed) || !grant.AuthorizationExpiresAt.After(completed) {
		return "timeout"
	}
	return ""
}

type managementObservationError struct{ reason string }

func (e *managementObservationError) Error() string { return "management observation unavailable" }
func (s *managementScheduler) matchGrant(c managementCandidate, g ManagementPollGrant) error {
	if g.Nonce != c.nonce || g.ProjectID != c.job.ProjectID || g.SourceID != c.job.SourceID || g.SourceRevision != c.job.SourceRevision || g.LeaderJobID != c.job.ID || g.Pack != c.job.Pack || g.Destination != c.job.Destination || g.ScopeHash != c.job.Management.ScopeHash || g.IdentityHash != c.job.Management.IdentityHash {
		return errors.New("management grant differs from locally approved source")
	}
	return nil
}
func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
func (s *managementScheduler) runRecovered(ctx context.Context, source string, task *managementTask, g ManagementPollGrant) {
	defer s.finish(source, task)
	s.publish(ctx, source, task, g)
}
func (s *managementScheduler) publish(ctx context.Context, source string, task *managementTask, grant ManagementPollGrant) {
	for {
		s.mu.Lock()
		err := s.saveSpoolLocked()
		s.mu.Unlock()
		if err != nil {
			if !managementRetryWait(ctx) {
				return
			}
			continue
		}
		response, err := s.control(ctx, grant.LeaderJobID, ManagementPollControl{Operation: "get", InstanceID: grant.CollectorInstanceID, PollID: grant.PollID, Generation: grant.Generation})
		if err == nil && response.Grant != nil {
			current, verifyErr := VerifyManagementPoll(*response.Grant, s.agent.cfg.ControllerPublicKey, s.agent.cfg.CollectorID, grant.CollectorInstanceID, time.Now().UTC(), true)
			if verifyErr == nil && sameManagementPollCore(grant, current) {
				if response.State == "accepted" {
					if !managementAcceptedMatches(task.pending, current) {
						if !managementRetryWait(ctx) {
							return
						}
						continue
					}
					if s.complete(task, grant, false) == nil {
						return
					}
				}
				if (response.State == "abandoned" || response.State == "released") && current.AcceptedCoreDigest == "" {
					if s.complete(task, grant, true) == nil {
						return
					}
				}
				if current.AcceptedCoreDigest != "" {
					if !managementRetryWait(ctx) {
						return
					}
					continue
				}
				s.mu.Lock()
				task.pending.ManagementGrant = response.Grant
				batch := *task.pending
				s.mu.Unlock()
				if err = s.agent.jobRequest(ctx, http.MethodPost, "jobs/"+url.PathEscape(grant.LeaderJobID)+"/samples", &batch, nil); err == nil {
					if s.complete(task, grant, false) == nil {
						return
					}
				}
			}
		}
		if ctx.Err() != nil || !grant.Deadline.After(time.Now()) {
			_ = s.abandonCompleted(task, grant)
			return
		}
		if !managementRetryWait(ctx) {
			_ = s.abandonCompleted(task, grant)
			return
		}
	}
}
func managementRetryWait(ctx context.Context) bool {
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}
func sameManagementPollCore(a, b ManagementPollGrant) bool {
	a.Credentials = nil
	b.Credentials = nil
	a.AcceptedCoreDigest = ""
	b.AcceptedCoreDigest = ""
	a.UnavailableReason = ""
	b.UnavailableReason = ""
	a.AuthorizationExpiresAt = time.Time{}
	b.AuthorizationExpiresAt = time.Time{}
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right)
}
func (s *managementScheduler) complete(task *managementTask, g ManagementPollGrant, rejected bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := task.pending
	oldRejected := append([]managementSpoolRejection(nil), s.rejected...)
	if rejected {
		body, _ := json.Marshal(old.MessagingManagement)
		digest := sha256.Sum256(body)
		s.rejected = append(s.rejected, managementSpoolRejection{PollID: g.PollID, SourceID: g.SourceID, Generation: g.Generation, Reason: "fenced", Digest: hex.EncodeToString(digest[:])})
		if len(s.rejected) > 32 {
			s.rejected = s.rejected[len(s.rejected)-32:]
		}
	}
	delete(s.pending, g.PollID)
	task.pending = nil
	if err := s.saveSpoolLocked(); err != nil {
		s.pending[g.PollID] = old
		task.pending = old
		s.rejected = oldRejected
		return err
	}
	s.agent.spoolBudget.release("management-pending:" + g.PollID)
	delete(s.nonces, managementSourceKey(g.ProjectID, g.SourceID)+"\x00"+task.candidate.key)
	s.next[managementSourceKey(g.ProjectID, g.SourceID)+"\x00"+task.candidate.key] = time.Now().Add(time.Duration(max(10, task.candidate.job.IntervalSeconds)) * time.Second)
	s.signalWake()
	return nil
}

func (s *managementScheduler) abandon(g ManagementPollGrant) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := s.control(ctx, g.LeaderJobID, ManagementPollControl{Operation: "abandon", InstanceID: g.CollectorInstanceID, PollID: g.PollID, Generation: g.Generation, CompletionConfirmed: true})
	return err
}
func (s *managementScheduler) close() {
	s.mu.Lock()
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
	tasks := make([]*managementTask, 0, len(s.active))
	for _, task := range s.active {
		task.cancel()
		tasks = append(tasks, task)
	}
	wakeDone := s.wakeDone
	s.mu.Unlock()
	if wakeDone != nil {
		<-wakeDone
	}
	for _, task := range tasks {
		<-task.done
	}
}

func managementBatchCoreDigest(batch *RunSampleBatch) (string, error) {
	if batch == nil || batch.EvidenceVersion != 2 || batch.MessagingManagement == nil {
		return "", errors.New("management evidence missing")
	}
	core, err := analysis.MonitorEvidenceCoreBytes(analysis.MonitorEvidenceEnvelope{Version: 2, Points: batch.Points, MessagingManagement: batch.MessagingManagement})
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(core)
	return hex.EncodeToString(hash[:]), nil
}
func managementAcceptedMatches(batch *RunSampleBatch, g ManagementPollGrant) bool {
	digest, err := managementBatchCoreDigest(batch)
	return err == nil && g.AcceptedCoreDigest != "" && digest == g.AcceptedCoreDigest
}
func (s *managementScheduler) abandonCompleted(task *managementTask, g ManagementPollGrant) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := s.control(ctx, g.LeaderJobID, ManagementPollControl{Operation: "abandon", InstanceID: g.CollectorInstanceID, PollID: g.PollID, Generation: g.Generation, CompletionConfirmed: true})
	if err != nil {
		return err
	}
	if response.Grant == nil {
		return errors.New("management drain confirmation identity missing")
	}
	confirmed, err := VerifyManagementPoll(*response.Grant, s.agent.cfg.ControllerPublicKey, s.agent.cfg.CollectorID, g.CollectorInstanceID, time.Now().UTC(), true)
	if err != nil || !sameManagementPollCore(g, confirmed) {
		return errors.New("management drain confirmation differs from poll")
	}
	if response.State == "accepted" {
		if !managementAcceptedMatches(task.pending, confirmed) {
			return errors.New("accepted management core differs from spool")
		}
		return s.complete(task, g, false)
	}
	if confirmed.AcceptedCoreDigest != "" {
		return errors.New("accepted management evidence cannot be abandoned")
	}
	if response.State != "abandoned" && response.State != "released" {
		return errors.New("management source drain remains unconfirmed")
	}
	return s.complete(task, g, true)
}

// The timer is independent of controller heartbeat I/O. Polls never catch up in
// bursts: every completed source has one next deadline and one active task.
func (s *managementScheduler) signalWake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func (s *managementScheduler) wakeLoop() {
	defer close(s.wakeDone)
	for {
		s.mu.Lock()
		if s.closed || s.root.Err() != nil {
			s.mu.Unlock()
			return
		}
		for source := range s.jobs {
			s.startLocked(source)
		}
		s.pruneLocked()
		delay := time.Hour
		now := time.Now()
		for source, cs := range s.jobs {
			if s.active[source] != nil {
				continue
			}
			for _, c := range cs {
				next := s.next[source+"\x00"+c.key]
				d := next.Sub(now)
				if d <= 0 {
					d = time.Second
				}
				delay = min(delay, d)
			}
		}
		root := s.root
		s.mu.Unlock()
		timer := time.NewTimer(delay)
		select {
		case <-root.Done():
			timer.Stop()
			return
		case <-s.wake:
			timer.Stop()
		case <-timer.C:
		}
	}
}
func (s *managementScheduler) pruneLocked() {
	keys := map[string]bool{}
	sources := map[string]bool{}
	for source, cs := range s.jobs {
		for _, c := range cs {
			keys[source+"\x00"+c.key] = true
			sources[source] = true
		}
	}
	for source, t := range s.active {
		keys[source+"\x00"+t.candidate.key] = true
		sources[source] = true
	}
	for _, b := range s.pending {
		if b.ManagementGrant == nil {
			continue
		}
		var g ManagementPollGrant
		if json.Unmarshal([]byte(b.ManagementGrant.Payload), &g) != nil {
			continue
		}
		source := managementSourceKey(g.ProjectID, g.SourceID)
		key := strconv.FormatInt(g.SourceRevision, 10) + "\x00" + g.ScopeHash + "\x00" + g.IdentityHash
		keys[source+"\x00"+key] = true
		sources[source] = true
	}
	for k := range s.next {
		if !keys[k] {
			delete(s.next, k)
		}
	}
	for k := range s.nonces {
		if !keys[k] {
			delete(s.nonces, k)
		}
	}
	for source := range s.last {
		if !sources[source] {
			delete(s.last, source)
		}
	}
	for source := range s.cadence {
		if !sources[source] {
			delete(s.cadence, source)
		}
	}
	for source, cs := range s.jobs {
		if len(cs) == 0 {
			delete(s.jobs, source)
		}
	}
}

type managementCadence struct {
	at       time.Time
	interval int
}

// Source-monotonic elapsed time accounts for skipped intervals, including a
// previous publication that blocked the source. Captures do not multiply jobs.
func (s *managementScheduler) missedTicks(source string, started, completed time.Time, interval int) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, exists := s.cadence[source]
	period := time.Duration(max(interval, 10)) * time.Second
	if !exists || old.interval != interval {
		old = managementCadence{at: started, interval: interval}
		exists = false
	}
	ticks := int64(completed.Sub(old.at) / period)
	missed := ticks
	if exists {
		missed--
	}
	if missed < 0 {
		missed = 0
	}
	if ticks > 0 {
		old.at = old.at.Add(time.Duration(ticks) * period)
	}
	s.cadence[source] = old
	return missed
}
