package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/elastic"
	"github.com/jasonczech/apexload/pkg/storage"
)

var elasticInspectionCleanupRunning sync.Map
var elasticUnacceptedCleanupPending sync.Map

type elasticUnacceptedCleanupScope string

const (
	elasticCleanupRun        elasticUnacceptedCleanupScope = "run"
	elasticCleanupInspection elasticUnacceptedCleanupScope = "inspection"
)

type elasticUnacceptedCleanupKey struct {
	server *Server
	scope  elasticUnacceptedCleanupScope
	jobID  string
	taskID string
}

type elasticUnacceptedCleanupTask struct {
	store       storage.ElasticInspectionStore
	job         storage.RunMonitorJob
	client      *elastic.Client
	owned       []*analysis.ProviderContinuation
	op          *storage.ElasticInspection
	running     atomic.Bool
	expiryTimer *time.Timer
}

func (s *Server) hasUnacceptedElasticCleanup(scope elasticUnacceptedCleanupScope, jobID string) bool {
	pending := false
	elasticUnacceptedCleanupPending.Range(func(key, _ any) bool {
		owned := key.(elasticUnacceptedCleanupKey)
		if owned.server == s && owned.scope == scope && owned.jobID == jobID {
			pending = true
			return false
		}
		return true
	})
	return pending
}

func (s *Server) resumeUnacceptedElasticCleanup() {
	elasticUnacceptedCleanupPending.Range(func(key, value any) bool {
		ownedKey := key.(elasticUnacceptedCleanupKey)
		if ownedKey.server == s {
			s.startUnacceptedElasticCleanupRetry(ownedKey, value.(*elasticUnacceptedCleanupTask))
		}
		return true
	})
}

func (s *Server) startUnacceptedElasticCleanupRetry(key elasticUnacceptedCleanupKey, task *elasticUnacceptedCleanupTask) {
	if !task.running.CompareAndSwap(false, true) {
		return
	}
	if !s.launchBackground(func() {
		defer task.running.Store(false)
		defer task.client.Close()
		// Normal provider handles expire within two minutes. An unknown or
		// longer expiry remains in the bounded per-job map for the next sweep;
		// stopping this worker never discards live ownership.
		parent := s.startupContext
		if parent == nil {
			parent = context.Background()
		}
		ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
		defer cancel()
		for {
			if ctx.Err() != nil {
				return
			}
			task.pruneExpired(time.Now().UTC())
			if len(task.owned) == 0 || task.retain(time.Now().UTC()) {
				releaseUnacceptedElasticCleanup(key, task)
				return
			}
			task.close(ctx)
			if len(task.owned) == 0 {
				releaseUnacceptedElasticCleanup(key, task)
				return
			}
			timer := time.NewTimer(time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}) {
		task.running.Store(false)
	}
}

func releaseUnacceptedElasticCleanup(key elasticUnacceptedCleanupKey, task *elasticUnacceptedCleanupTask) {
	elasticUnacceptedCleanupPending.CompareAndDelete(key, task)
	if task.expiryTimer != nil {
		task.expiryTimer.Stop()
	}
}

// An identity-checked expiry callback releases only this task's known-expired
// handles even if server shutdown prevents any further recovery sweeps.
func scheduleUnacceptedElasticCleanupExpiry(key elasticUnacceptedCleanupKey, task *elasticUnacceptedCleanupTask) {
	var latest time.Time
	for _, cursor := range task.owned {
		if cursor.KeepAliveUntil.IsZero() {
			return
		}
		if cursor.KeepAliveUntil.After(latest) {
			latest = cursor.KeepAliveUntil
		}
	}
	if !latest.IsZero() {
		task.expiryTimer = time.AfterFunc(time.Until(latest), func() {
			elasticUnacceptedCleanupPending.CompareAndDelete(key, task)
		})
	}
}

func (task *elasticUnacceptedCleanupTask) pruneExpired(now time.Time) {
	live := task.owned[:0]
	for _, cursor := range task.owned {
		if cursor.KeepAliveUntil.IsZero() || cursor.KeepAliveUntil.After(now) {
			live = append(live, cursor)
		}
	}
	task.owned = live
}

func (task *elasticUnacceptedCleanupTask) retain(now time.Time) bool {
	if task.store == nil {
		return false
	}
	if task.op != nil {
		retained, err := task.store.RetainElasticInspectionCleanup(task.op.ID, task.op.OwnerToken, task.owned, now)
		if err == nil && retained {
			return true
		}
		if err == nil && !retained {
			task.op = nil
		}
		return false
	}
	op, err := retainUnacceptedElasticCleanup(task.store, task.job, task.owned, now)
	task.op = op
	return err == nil
}

func (task *elasticUnacceptedCleanupTask) close(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	live := task.owned[:0]
	for _, cursor := range task.owned {
		err := task.client.CloseContinuation(ctx, cursor)
		var response *elastic.HTTPError
		if err != nil && !(errors.As(err, &response) && response.Status == http.StatusNotFound) && (cursor.KeepAliveUntil.IsZero() || cursor.KeepAliveUntil.After(time.Now().UTC())) {
			live = append(live, cursor)
		}
	}
	task.owned = live
}

func (s *Server) resumeElasticInspectionCleanup(ctx context.Context) {
	if ctx.Err() == nil {
		s.resumeUnacceptedElasticCleanup()
	}
	store, ok := s.store.(storage.ElasticInspectionStore)
	if !ok || ctx.Err() != nil {
		return
	}
	ops, err := store.ListElasticInspectionCleanup("", "", 2)
	if err != nil {
		return
	}
	for _, op := range ops {
		if _, running := elasticInspectionCleanupRunning.LoadOrStore(op.ID, true); running {
			continue
		}
		select {
		case elasticInspectionSlots <- struct{}{}:
		default:
			elasticInspectionCleanupRunning.Delete(op.ID)
			continue
		}
		if !s.launchBackground(func() {
			defer elasticInspectionCleanupRunning.Delete(op.ID)
			defer func() { <-elasticInspectionSlots }()
			s.closeDirectElasticInspectionCleanup(ctx, store, op)
		}) {
			<-elasticInspectionSlots
			elasticInspectionCleanupRunning.Delete(op.ID)
		}
	}
}

func (s *Server) closeDirectElasticInspectionCleanup(parent context.Context, store storage.ElasticInspectionStore, op *storage.ElasticInspection) {
	if op == nil || len(op.CleanupContinuations) == 0 || parent.Err() != nil {
		return
	}
	term, err := s.runner.captureExecutionLeaseTerm()
	if err != nil {
		return
	}
	var work elasticInspectionWork
	if json.Unmarshal([]byte(op.PayloadJSON), &work) != nil || work.Connection == nil {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	credentials, err := resolveElasticCredentials(ctx, s.runner.secretResolverForProject(op.ProjectID), work.Connection)
	if err != nil {
		return
	}
	client, err := elastic.NewClient(elasticPolicyConfig(work.Connection.Config), credentials)
	if err != nil {
		return
	}
	defer client.Close()
	complete := true
	for _, owned := range op.CleanupContinuations {
		if !owned.KeepAliveUntil.IsZero() && !owned.KeepAliveUntil.After(time.Now().UTC()) {
			continue
		}
		err := client.CloseContinuation(ctx, owned)
		var response *elastic.HTTPError
		if err != nil && !(errors.As(err, &response) && response.Status == http.StatusNotFound) {
			complete = false
		}
	}
	if complete && s.runner.executionLeaseTermCurrent(term) {
		_, _ = store.ClearElasticInspectionCleanup(op.ID, op.CleanupContinuations)
	}
}

// Unaccepted results cannot advance a run checkpoint. Keep their owned handles
// in a separate private operation before close attempts, including after an
// artifact failure or loss of the collection's execution term.
func (s *Server) finishUnacceptedDirectElasticResult(client *elastic.Client, job storage.RunMonitorJob, result elastic.Result) {
	owned := make([]*analysis.ProviderContinuation, 0, len(result.CleanupContinuations)+1)
	for _, cursor := range append(append([]*analysis.ProviderContinuation(nil), result.CleanupContinuations...), result.Continuation) {
		if cursor == nil || cursor.PITID == "" && cursor.AsyncID == "" || !cursor.KeepAliveUntil.IsZero() && !cursor.KeepAliveUntil.After(time.Now().UTC()) {
			continue
		}
		copy := *cursor
		copy.SearchAfter, copy.CompositeAfter, copy.ResolvedIndices = nil, nil, nil
		duplicate := false
		for _, prior := range owned {
			if prior.PITID == copy.PITID && prior.AsyncID == copy.AsyncID {
				duplicate = true
			}
		}
		if !duplicate {
			owned = append(owned, &copy)
		}
	}
	if len(owned) == 0 {
		return
	}
	store, _ := s.store.(storage.ElasticInspectionStore)
	// Retain only immutable configuration and owned handles in this fallback,
	// never the unaccepted points, evidence, coverage or pagination state.
	privateJob := storage.RunMonitorJob{ID: job.ID, ProjectID: job.ProjectID, Source: storage.MonitorSource{ConnectionID: job.Source.ConnectionID}}
	if job.ElasticConnectionSnapshot != nil {
		snapshot := *job.ElasticConnectionSnapshot
		snapshot.Config.ApprovedCIDRs = append([]string(nil), snapshot.Config.ApprovedCIDRs...)
		snapshot.Config.KibanaApprovedCIDRs = append([]string(nil), snapshot.Config.KibanaApprovedCIDRs...)
		snapshot.Config.LogstashApprovedCIDRs = append([]string(nil), snapshot.Config.LogstashApprovedCIDRs...)
		snapshot.Capabilities.Status = make(map[string]string, len(job.ElasticConnectionSnapshot.Capabilities.Status))
		for name, status := range job.ElasticConnectionSnapshot.Capabilities.Status {
			snapshot.Capabilities.Status[name] = status
		}
		privateJob.ElasticConnectionSnapshot = &snapshot
	}
	task := &elasticUnacceptedCleanupTask{store: store, job: privateJob, client: client, owned: owned}
	s.finishOwnedElasticCleanup(elasticUnacceptedCleanupKey{server: s, scope: elasticCleanupRun, jobID: job.ID}, task)
}

// An inspection already owns immutable configuration and an execution token.
// A failed private receipt never publishes its preview or verification result.
func (s *Server) finishUnacceptedElasticInspectionCleanup(client *elastic.Client, store storage.ElasticInspectionStore, op *storage.ElasticInspection, owner string, owned []*analysis.ProviderContinuation) {
	if client == nil || op == nil || owner == "" || len(owned) == 0 {
		return
	}
	copy := *op
	copy.OwnerToken = owner
	privateJob := storage.RunMonitorJob{ID: op.ID, ProjectID: op.ProjectID, Source: storage.MonitorSource{ConnectionID: op.ConnectionID}}
	var work elasticInspectionWork
	if json.Unmarshal([]byte(op.PayloadJSON), &work) == nil {
		privateJob.ElasticConnectionSnapshot = work.Connection
	}
	minimal := make([]*analysis.ProviderContinuation, 0, len(owned))
	for _, cursor := range owned {
		if cursor == nil || cursor.PITID == "" && cursor.AsyncID == "" || !cursor.KeepAliveUntil.IsZero() && !cursor.KeepAliveUntil.After(time.Now().UTC()) {
			continue
		}
		copy := *cursor
		copy.SearchAfter, copy.CompositeAfter, copy.ResolvedIndices = nil, nil, nil
		minimal = append(minimal, &copy)
	}
	if len(minimal) == 0 {
		return
	}
	_, _ = store.CancelElasticInspectionIfOwner(op.ID, op.ProjectID, op.ConnectionID, owner, time.Now().UTC())
	task := &elasticUnacceptedCleanupTask{store: store, job: privateJob, client: client, owned: minimal, op: &copy}
	s.finishOwnedElasticCleanup(elasticUnacceptedCleanupKey{server: s, scope: elasticCleanupInspection, jobID: op.ID}, task)
}

func (s *Server) finishOwnedElasticCleanup(key elasticUnacceptedCleanupKey, task *elasticUnacceptedCleanupTask) {
	task.running.Store(true)
	for {
		if _, alreadyPending := elasticUnacceptedCleanupPending.LoadOrStore(key, task); !alreadyPending {
			break
		}
		// A replaced inspection claim may finish while its former owner still
		// has live handles. Give each ownership set its own bounded retry task.
		key.taskID = uuid.NewString()
	}
	scheduleUnacceptedElasticCleanupExpiry(key, task)
	durable := task.retain(time.Now().UTC())
	acceptedOwnership := append([]*analysis.ProviderContinuation(nil), task.owned...)
	task.close(context.Background())
	if durable || len(task.owned) == 0 {
		if durable && len(task.owned) == 0 && task.op != nil {
			_, _ = task.store.ClearElasticInspectionCleanup(task.op.ID, acceptedOwnership)
		}
		releaseUnacceptedElasticCleanup(key, task)
		return
	}
	// A storage outage and failed DELETE leave a local owner. Pause this job
	// until that owner persists its operation or the known upstream TTL elapses.
	task.running.Store(false)
	s.startUnacceptedElasticCleanupRetry(key, task)
}

func retainUnacceptedElasticCleanup(store storage.ElasticInspectionStore, job storage.RunMonitorJob, owned []*analysis.ProviderContinuation, now time.Time) (*storage.ElasticInspection, error) {
	if job.ElasticConnectionSnapshot == nil || len(owned) == 0 || len(owned) > 32 {
		return nil, errors.New("invalid unaccepted Elastic cleanup")
	}
	work := elasticInspectionWork{Connection: job.ElasticConnectionSnapshot, Request: ElasticInspectionRequest{Kind: "verify"}}
	body, err := json.Marshal(work)
	if err != nil || len(body) > 96<<10 {
		return nil, errors.New("unaccepted Elastic cleanup configuration exceeds limits")
	}
	op := &storage.ElasticInspection{ID: "elastic-inspection-" + uuid.NewString(), ProjectID: job.ProjectID, ConnectionID: job.Source.ConnectionID, ConnectionRevision: job.ElasticConnectionSnapshot.Revision, Kind: "verify", PayloadJSON: string(body), CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	if err = store.CreateElasticInspection(op); err != nil {
		return nil, err
	}
	owner := uuid.NewString()
	op.OwnerToken = owner
	if claimed, claimErr := store.ClaimElasticInspection(op.ID, owner, now, op.ExpiresAt); claimErr != nil || !claimed {
		return nil, errors.New("unaccepted Elastic cleanup claim unavailable")
	}
	if retained, retainErr := store.RetainElasticInspectionCleanup(op.ID, owner, owned, now); retainErr != nil || !retained {
		_, _ = store.CancelElasticInspectionIfOwner(op.ID, op.ProjectID, op.ConnectionID, owner, now)
		return op, errors.New("unaccepted Elastic cleanup ownership unavailable")
	}
	// No query result, coverage or run progress is published by this handoff.
	if completed, completeErr := store.CompleteElasticInspection(op.ID, owner, "{}", "", now); completeErr != nil || !completed {
		_, _ = store.CancelElasticInspectionIfOwner(op.ID, op.ProjectID, op.ConnectionID, owner, now)
	}
	return op, nil
}
