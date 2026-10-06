package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"

	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"

	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"github.com/jasonczech/apexload/pkg/scrub"
	"github.com/jasonczech/apexload/pkg/storage"
)

type providerMonitorStore interface {
	storage.RunMonitorStore
	ListProviderObservationHeads(string, string, string, string, string, int) ([]storage.ObservationHead, error)
	AcceptProviderMonitorBatch(string, string, storage.MonitorBatchReceipt, storage.ProviderMonitorBatch, string, string, uint64) error
	CloseExpiredProviderMonitors(string, string, time.Time) error
	PollDirectRunMonitorJobs(time.Time) ([]storage.RunMonitorJob, error)
}
type canonicalMonitorPage struct {
	Points          []analysis.MetricPoint
	Evidence        []monitorprovider.Diagnostic
	EvidenceSources []string
	HasMore         bool
	AfterKey        string
}

func decodeMonitorArtifact(body []byte) (storage.ProviderMonitorBatch, error) {
	var out storage.ProviderMonitorBatch
	if len(body) > 0 && body[0] == '[' {
		return out, json.Unmarshal(body, &out.Points)
	}
	return out, json.Unmarshal(body, &out)
}
func loadCanonicalMonitorData(ctx context.Context, store storage.RunMonitorStore, project, run, source, kind, after string, limit int) (canonicalMonitorPage, error) {
	out := canonicalMonitorPage{Points: []analysis.MetricPoint{}, Evidence: []monitorprovider.Diagnostic{}}
	provider, ok := store.(providerMonitorStore)
	if !ok {
		return out, errors.New("canonical monitor storage unavailable")
	}
	if limit < 1 || limit > 999 {
		return out, errors.New("invalid canonical page size")
	}
	heads, err := provider.ListProviderObservationHeads(project, run, source, kind, after, limit+1)
	if err != nil {
		return out, err
	}
	if len(heads) > limit {
		out.HasMore = true
		heads = heads[:limit]
	}
	cache := map[string]storage.ProviderMonitorBatch{}
	receipts := map[string]storage.MonitorBatchReceipt{}
	sources := map[string]string{}
	jobsByID := map[string]storage.RunMonitorJob{}
	firstHeads := map[string]storage.ObservationHead{}
	traceProofs := map[string]map[string]bool{}
	if kind != "point" && len(heads) > 0 {
		jobs, err := store.ListRunMonitorJobs(project, run)
		if err != nil {
			return out, err
		}
		for _, job := range jobs {
			sources[job.ID] = job.Source.ID
			jobsByID[job.ID] = job
			if job.Source.Pack == monitorprovider.PackDynatrace {
				traceProofs[job.ID] = map[string]bool{}
				for _, id := range job.RunTraceIDs {
					traceProofs[job.ID][id] = true
				}
			}
		}
		if firstStore, ok := store.(interface {
			ListProviderFirstDiagnosticHeads(string, string, []string) ([]storage.ObservationHead, error)
		}); ok {
			keys := make([]string, 0, len(heads))
			for _, h := range heads {
				if jobsByID[h.JobID].Source.Pack == monitorprovider.PackPrometheusQuery {
					keys = append(keys, h.Key)
				}
			}
			first, err := firstStore.ListProviderFirstDiagnosticHeads(project, run, keys)
			if err != nil {
				return out, err
			}
			for _, h := range first {
				firstHeads[h.Key] = h
			}
		}
	}
	readHead := func(h storage.ObservationHead) (storage.ProviderMonitorBatch, storage.MonitorBatchReceipt, error) {
		if err := ctx.Err(); err != nil {
			return storage.ProviderMonitorBatch{}, storage.MonitorBatchReceipt{}, err
		}
		key := fmt.Sprintf("%s:%d", h.JobID, h.Sequence)
		batch, cached := cache[key]
		receipt := receipts[key]
		if !cached {
			page, err := store.ListMonitorBatchReceipts(project, run, h.JobID, h.Sequence-1, 1)
			if err != nil || len(page) != 1 || page[0].JobID != h.JobID || page[0].Sequence != h.Sequence {
				return batch, receipt, errors.New("canonical receipt unavailable")
			}
			receipt = page[0]
			body, err := readMonitorArtifact(ctx, receipt.ArtifactKey, receipt.Digest)
			if err != nil {
				return batch, receipt, err
			}
			batch, err = decodeMonitorArtifact(body)
			if err != nil || len(batch.Points) != receipt.SampleCount {
				return batch, receipt, errors.New("canonical receipt is corrupt")
			}
			cache[key] = batch
			receipts[key] = receipt
		}
		return batch, receipt, nil
	}
	for _, h := range heads {
		batch, receipt, err := readHead(h)
		if err != nil {
			return out, err
		}
		if kind == "point" {
			if h.Index < 0 || h.Index >= len(batch.Points) {
				return out, errors.New("invalid canonical point index")
			}
			p := batch.Points[h.Index]
			if storage.ObservationKey(p) != h.Key {
				return out, errors.New("canonical point identity mismatch")
			}
			if err := storage.VerifyProviderHeadContent(h, p); err != nil {
				return out, err
			}
			p.ReceivedAt = receipt.ReceivedAt
			out.Points = append(out.Points, p)
		} else {
			if h.Index < 0 || h.Index >= len(batch.Evidence) {
				return out, errors.New("invalid canonical diagnostic index")
			}
			d := batch.Evidence[h.Index]
			if storage.DiagnosticKeyForJob(h.JobID, d) != h.Key {
				return out, errors.New("canonical diagnostic identity mismatch")
			}
			if err := storage.VerifyProviderHeadContent(h, d); err != nil {
				return out, err
			}
			if d.Provider == monitorprovider.ProviderDynatrace && d.Properties["trace_id"] != "" {
				d.Properties = maps.Clone(d.Properties)
				if d.Properties["correlation"] == "trace_id" {
					d.Properties["correlation"] = "entity_time"
				}
				if d.Properties["correlation"] != "run_header" && traceProofs[h.JobID][strings.ToLower(d.Properties["trace_id"])] {
					d.Properties["correlation"] = "trace_id"
				}
				d.Properties["correlation_sampling"] = "up_to_64_run_observed_trace_ids"
			}
			if sources[h.JobID] == "" {
				return out, errors.New("canonical diagnostic source unavailable")
			}
			if job := jobsByID[h.JobID]; job.Source.Pack == monitorprovider.PackPrometheusQuery {
				first := d.TimestampUTC
				if firstHead, ok := firstHeads["first:"+h.Key]; ok {
					firstBatch, _, err := readHead(firstHead)
					if err != nil {
						return out, err
					}
					if firstHead.JobID != h.JobID || firstHead.Index < 0 || firstHead.Index >= len(firstBatch.Evidence) {
						return out, errors.New("invalid first diagnostic reference")
					}
					firstDiagnostic := firstBatch.Evidence[firstHead.Index]
					if "first:"+storage.DiagnosticKeyForJob(h.JobID, firstDiagnostic) != firstHead.Key {
						return out, errors.New("first diagnostic identity mismatch")
					}
					first = firstDiagnostic.TimestampUTC
				}
				d = projectPrometheusDiagnostic(d, first, job)
			}
			out.Evidence = append(out.Evidence, d)
			out.EvidenceSources = append(out.EvidenceSources, sources[h.JobID])
		}
		out.AfterKey = h.Key
	}
	return out, nil
}

// Successful current-state snapshots establish absence, not historical resolution.
// Derive this projection from durable receipts without altering retry content.
func projectPrometheusDiagnostic(d monitorprovider.Diagnostic, first time.Time, job storage.RunMonitorJob) monitorprovider.Diagnostic {
	properties := make(map[string]string, len(d.Properties)+3)
	for key, value := range d.Properties {
		properties[key] = value
	}
	properties["first_observed_utc"] = first.UTC().Format(time.RFC3339Nano)
	properties["last_observed_utc"] = d.TimestampUTC.UTC().Format(time.RFC3339Nano)
	key := "targets"
	if d.Kind == "alert" {
		key = "alerts"
	}
	coverage := job.Coverage[key]
	if successful, ok := job.Coverage[key+"_last_success"]; ok {
		coverage = successful
	}
	if (coverage.Status == "complete" || coverage.Status == "no_data") && !coverage.Truncated && len(coverage.Warnings) == 0 && coverage.ObservedAt != nil && coverage.ObservedAt.After(d.TimestampUTC) && (job.LoadEndedAt == nil || !coverage.ObservedAt.After(*job.LoadEndedAt)) {
		d.Status = "no_longer_observed"
		d.TimestampUTC = *coverage.ObservedAt
		properties["state_observed_utc"] = d.TimestampUTC.UTC().Format(time.RFC3339Nano)
	}
	d.Properties = properties
	return d
}

// providerSettleSkewTolerance absorbs small executor/controller clock skew when
// a collector marks an observation settled. The claim itself is never rewritten:
// the batch digest covers it, so an identical retry must stay identical.
const providerSettleSkewTolerance = 10 * time.Second

// providerSettledClaimPremature reports a settled claim made before the
// observation's settling allowance has elapsed on the controller clock.
func providerSettledClaimPremature(settled bool, observedEnd time.Time, settleSeconds int, now time.Time) bool {
	return settled && now.Add(providerSettleSkewTolerance).Before(observedEnd.Add(time.Duration(settleSeconds)*time.Second))
}

func (s *Server) persistProviderBatch(ctx context.Context, job storage.RunMonitorJob, sequence int64, payload storage.ProviderMonitorBatch, term executionLeaseTerm, now time.Time) error {
	store, ok := s.store.(providerMonitorStore)
	if !ok {
		return errors.New("provider monitor storage unavailable")
	}
	for i := range payload.Points {
		payload.Points[i].Timing = providerPointTiming(payload.Points[i])
	}
	body, err := json.Marshal(payload)
	if err != nil || len(body) > 1<<20 {
		return errors.New("provider batch exceeds byte limit")
	}
	artifact, digest := monitorArtifactKey(job.ProjectID, job.RunID, body)
	if err = writeMonitorArtifact(ctx, artifact, body); err != nil {
		return err
	}
	receipt := storage.MonitorBatchReceipt{JobID: job.ID, Sequence: sequence, Digest: digest, ArtifactKey: artifact, SampleCount: len(payload.Points), ReceivedAt: now}
	name, owner, generation := "", "", uint64(0)
	if job.Source.ExecutorMode == "direct" && s.runner.durableExecutionFence {
		name, owner, generation = executionLeaseName, term.ownerID, term.generation
		if owner == "" || generation == 0 {
			return errNotExecutionLeader
		}
	}
	return store.AcceptProviderMonitorBatch(job.ProjectID, job.Source.CollectorID, receipt, payload, name, owner, generation)
}

// providerPointTiming is the controller's account of a provider point's
// clock: never exchanged with the controller, so unknown, with uncertainty of
// half the bucket: the source bucket, a query window, or a Prometheus
// evaluation step. It depends only on the point, so re-reading a bucket
// persists identical content.
func providerPointTiming(p analysis.MetricPoint) *analysis.PointTiming {
	if p.Provider == nil {
		return nil
	}
	resolution := p.Provider.ResolutionSeconds
	if window := p.Provider.BucketEnd.Sub(p.Provider.BucketStart); resolution == 0 && p.Provider.QueryWindow && window > 0 {
		resolution = int(window / time.Second)
	}
	if resolution == 0 {
		resolution = p.Provider.EvaluationStepSeconds
	}
	var freshness *time.Duration
	if p.Provider.FreshnessKnown && p.Provider.UnderlyingSampleTime != nil {
		if age := p.TimestampUTC.Sub(*p.Provider.UnderlyingSampleTime); age >= 0 {
			freshness = &age
		}
	}
	return analysis.ProviderTiming(resolution, freshness)
}

// Durable persistence is shared across provider-specific validators.
func (s *Server) persistProviderMonitorBatch(ctx context.Context, job storage.RunMonitorJob, sequence int64, payload storage.ProviderMonitorBatch, term executionLeaseTerm) error {
	return s.persistProviderBatch(ctx, job, sequence, payload, term, time.Now().UTC())
}
func scrubTextBounded(value string, limit int) string {
	value = scrub.Value(value, scrub.Policy{})
	if len(value) > limit {
		value = value[:limit]
	}
	return value
}

var errMonitoringPending = errors.New("monitoring reconciliation pending")

func (tr *TestRunner) ensureRunMonitorFinalization(project, run string) error {
	store, ok := tr.testStore.(providerMonitorStore)
	if !ok {
		return nil
	}
	if err := tr.trackDurableWrite(func() error {
		return store.CloseExpiredProviderMonitors(project, run, time.Now().UTC())
	}); err != nil {
		return err
	}
	jobs, err := store.ListRunMonitorJobs(project, run)
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if (job.Source.CanonicalProvider() || job.FlushOnly) && (job.State == "reconciling" || job.State == "collecting" || job.State == "flushing") {
			return errMonitoringPending
		}
	}
	return nil
}
