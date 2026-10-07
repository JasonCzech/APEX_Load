package api

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"net/http"
	"reflect"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/collectoragent"
	"github.com/jasonczech/apexload/pkg/elastic"
	"github.com/jasonczech/apexload/pkg/storage"
)

type elasticCleanupStore interface {
	storage.RunMonitorStore
	ListElasticContinuationCleanupJobs(string, string, string, int) ([]storage.RunMonitorJob, string, error)
	ClearElasticContinuation(string, string, *analysis.ProviderContinuation, string, string, uint64) (bool, error)
}

func (s *Server) startElasticCleanup(ctx context.Context) {
	store, ok := s.store.(elasticCleanupStore)
	if !ok {
		return
	}
	s.launchBackground(func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		after := ""
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			if !s.runner.isExecutionLeader() {
				continue
			}
			s.resumeElasticInspectionCleanup(ctx)
			jobs, next, err := store.ListElasticContinuationCleanupJobs("", "", after, 4)
			if err != nil {
				continue
			}
			after = next
			for _, job := range jobs {
				if job.Source.ExecutorMode == "direct" {
					s.closeDirectElasticContinuation(ctx, store, job, true)
				}
			}
		}
	})
}

func (s *Server) closeDirectElasticContinuation(parent context.Context, store elasticCleanupStore, job storage.RunMonitorJob, clear bool) {
	if job.ElasticConnectionSnapshot == nil || job.ElasticContinuation == nil || parent.Err() != nil {
		return
	}
	term, err := s.runner.captureExecutionLeaseTerm()
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	creds, err := resolveElasticCredentials(ctx, s.runner.secretResolverForProject(job.ProjectID), job.ElasticConnectionSnapshot)
	if err != nil {
		return
	}
	client, err := elastic.NewClient(job.ElasticConnectionSnapshot.Config, creds)
	if err != nil {
		return
	}
	defer client.Close()
	err = client.CloseContinuation(ctx, job.ElasticContinuation)
	var response *elastic.HTTPError
	alreadyGone := errors.As(err, &response) && response.Status == 404
	expiry := job.ElasticContinuation.KeepAliveUntil
	expired := !expiry.IsZero() && !expiry.After(time.Now().UTC())
	if clear && (err == nil || alreadyGone || expired) && s.runner.executionLeaseTermCurrent(term) {
		name, owner, generation := "", "", uint64(0)
		if term.ownerID != "" {
			name, owner, generation = executionLeaseName, term.ownerID, term.generation
		}
		_, _ = store.ClearElasticContinuation(job.ProjectID, job.ID, job.ElasticContinuation, name, owner, generation)
	}
}

// Shutdown closes current direct handles after workers drain. The cursor stays
// durable so recovery can restart and deduplicate the same fixed window.
func (s *Server) closeElasticOnShutdown(ctx context.Context) {
	store, ok := s.store.(elasticCleanupStore)
	if !ok || s.runner == nil {
		return
	}
	if inspections, ok := s.store.(storage.ElasticInspectionStore); ok {
		ops, _ := inspections.ListElasticInspectionCleanup("", "", 100)
		for _, op := range ops {
			s.closeDirectElasticInspectionCleanup(ctx, inspections, op)
		}
	}
	if provider, ok := s.store.(providerMonitorStore); ok {
		jobs, _ := provider.PollDirectRunMonitorJobs(time.Now().UTC())
		for _, job := range jobs {
			if job.Source.Pack == "elastic" {
				s.closeDirectElasticContinuation(ctx, store, job, false)
			}
		}
	}
	after := ""
	for ctx.Err() == nil {
		jobs, next, err := store.ListElasticContinuationCleanupJobs("", "", after, 32)
		if err != nil {
			return
		}
		for _, job := range jobs {
			if job.Source.ExecutorMode == "direct" {
				s.closeDirectElasticContinuation(ctx, store, job, true)
			}
		}
		if next == "" {
			return
		}
		after = next
	}
}

func (s *Server) handlePollElasticCleanups(w http.ResponseWriter, r *http.Request) {
	c := s.authorizeMonitorCollector(w, r)
	if c == nil {
		return
	}
	store, ok := s.store.(elasticCleanupStore)
	if !ok {
		writeError(w, 503, "Elastic cleanup storage unavailable")
		return
	}
	key, err := monitorSigningKey()
	if err != nil {
		writeError(w, 503, "monitor signing key unavailable")
		return
	}
	after := r.URL.Query().Get("after_key")
	if len(after) > storage.MaxRunMonitorJobIDBytes {
		writeError(w, 400, "invalid cleanup continuation")
		return
	}
	jobs, next, err := store.ListElasticContinuationCleanupJobs(c.ProjectID, "", after, 32)
	if err != nil {
		writeError(w, 503, "cannot poll Elastic cleanup")
		return
	}
	w.Header().Set("X-APEX-Monitor-Next-Key", next)
	now := time.Now().UTC()
	out := []collectoragent.SignedRunJob{}
	for _, job := range jobs {
		if job.Source.ExecutorMode != "collector" || job.Source.CollectorID != c.ID || job.ElasticConnectionSnapshot == nil || job.Source.Elastic == nil {
			continue
		}
		caps := job.ElasticConnectionSnapshot.Capabilities
		grant := collectoragent.RunJob{Cleanup: true, ID: job.ID, ProjectID: job.ProjectID, RunID: job.RunID, ScenarioID: job.ScenarioID, CollectorID: c.ID, SourceID: job.Source.ID, SourceRevision: job.Source.Revision, Pack: "elastic", Destination: job.Source.Destination, IntervalSeconds: max(1, job.Source.IntervalSeconds), StartedAt: job.StartedAt, ExpiresAt: now.Add(time.Minute), LeaseExpiresAt: now.Add(time.Minute), NextSequence: job.NextSequence, Elastic: &elastic.JobConfig{Config: job.ElasticConnectionSnapshot.Config, Selection: *job.Source.Elastic, Continuation: job.ElasticContinuation, Capabilities: &caps}}
		creds, credentialErr := resolveElasticCredentials(r.Context(), s.runner.secretResolverForProject(c.ProjectID), job.ElasticConnectionSnapshot)
		public, decodeErr := base64.StdEncoding.DecodeString(job.Source.CollectorPublicKey)
		if credentialErr != nil || decodeErr != nil {
			continue
		}
		grant.Credentials, err = collectoragent.SealCredentials(grant, public, collectoragent.ElasticCredentialBundle(creds))
		if err != nil {
			continue
		}
		signed, signErr := collectoragent.SignRunJob(grant, key)
		if signErr == nil {
			out = append(out, signed)
		}
	}
	writeJSON(w, 200, out)
}

type ElasticCleanupReceipt struct {
	Job collectoragent.SignedRunJob `json:"job"`
}

func (s *Server) handleElasticCleanupReceipt(w http.ResponseWriter, r *http.Request) {
	c := s.authorizeMonitorCollector(w, r)
	if c == nil {
		return
	}
	store, ok := s.store.(elasticCleanupStore)
	if !ok {
		writeError(w, 503, "Elastic cleanup storage unavailable")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 128<<10)
	var receipt ElasticCleanupReceipt
	if !decodeJSONBody(w, r, &receipt) {
		return
	}
	key, err := monitorSigningKey()
	if err != nil {
		writeError(w, 503, "monitor signing key unavailable")
		return
	}
	grant, err := collectoragent.VerifyRunJob(receipt.Job, key.Public().(ed25519.PublicKey), c.ID, time.Now().UTC())
	if err != nil || !grant.Cleanup || grant.ProjectID != c.ProjectID || grant.ID != r.PathValue("job_id") {
		writeError(w, 403, "invalid cleanup grant")
		return
	}
	job, err := store.GetRunMonitorJob(c.ProjectID, grant.ID)
	if err != nil || job.Source.CollectorID != c.ID || job.Source.ID != grant.SourceID || job.Source.Revision != grant.SourceRevision || job.RunID != grant.RunID || job.ElasticConnectionSnapshot == nil || !reflect.DeepEqual(job.ElasticConnectionSnapshot.Config, grant.Elastic.Config) {
		writeError(w, 403, "cleanup assignment changed")
		return
	}
	cleared, err := store.ClearElasticContinuation(c.ProjectID, job.ID, grant.Elastic.Continuation, "", "", 0)
	if err != nil {
		writeError(w, 409, "cleanup cursor changed")
		return
	}
	writeJSON(w, 200, map[string]bool{"accepted": cleared})
}
