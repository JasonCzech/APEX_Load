package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/appdynamics"
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
	Evidence        []appdynamics.Diagnostic
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
	out := canonicalMonitorPage{Points: []analysis.MetricPoint{}, Evidence: []appdynamics.Diagnostic{}}
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
	if kind != "point" && len(heads) > 0 {
		jobs, err := store.ListRunMonitorJobs(project, run)
		if err != nil {
			return out, err
		}
		for _, job := range jobs {
			sources[job.ID] = job.Source.ID
		}
	}
	for _, h := range heads {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		key := fmt.Sprintf("%s:%d", h.JobID, h.Sequence)
		batch, cached := cache[key]
		receipt := receipts[key]
		if !cached {
			page, err := store.ListMonitorBatchReceipts(project, run, h.JobID, h.Sequence-1, 1)
			if err != nil || len(page) != 1 || page[0].JobID != h.JobID || page[0].Sequence != h.Sequence {
				return out, errors.New("canonical receipt unavailable")
			}
			receipt = page[0]
			body, err := readMonitorArtifact(ctx, receipt.ArtifactKey, receipt.Digest)
			if err != nil {
				return out, err
			}
			batch, err = decodeMonitorArtifact(body)
			if err != nil || len(batch.Points) != receipt.SampleCount {
				return out, errors.New("canonical receipt is corrupt")
			}
			cache[key] = batch
			receipts[key] = receipt
		}
		if kind == "point" {
			if h.Index < 0 || h.Index >= len(batch.Points) {
				return out, errors.New("invalid canonical point index")
			}
			p := batch.Points[h.Index]
			if storage.ObservationKey(p) != h.Key {
				return out, errors.New("canonical point identity mismatch")
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
			if sources[h.JobID] == "" {
				return out, errors.New("canonical diagnostic source unavailable")
			}
			out.Evidence = append(out.Evidence, d)
			out.EvidenceSources = append(out.EvidenceSources, sources[h.JobID])
		}
		out.AfterKey = h.Key
	}
	return out, nil
}

// This is shared by direct workers and authenticated collector submissions.
// Artifacts are durable before their receipt, cursor and canonical heads commit.
func (s *Server) ingestAppDynamicsBatch(ctx context.Context, job storage.RunMonitorJob, sequence int64, payload storage.ProviderMonitorBatch, term executionLeaseTerm) error {
	store, ok := s.store.(providerMonitorStore)
	if !ok {
		return errors.New("provider monitor storage unavailable")
	}
	if job.ConnectionSnapshot == nil || job.Source.AppDynamics == nil || sequence < 0 || len(payload.Points) > 512 || len(payload.Evidence) > 100 || len(payload.Coverage) > 128 || len(payload.Topology) > 256 {
		return errors.New("invalid AppDynamics batch")
	}
	now := time.Now().UTC()
	if err := validateAppDynamicsProviderBatchCursor(job, &payload, now, sequence >= job.NextSequence); err != nil {
		return err
	}
	for i, p := range payload.Points {
		if p.Validate() != nil || p.Provider == nil || p.Provider.Name != "appdynamics" || p.ProjectID != job.ProjectID || p.RunID != job.RunID || p.Source != job.Source.ID || p.ScenarioID != job.ScenarioID || p.Host != job.Source.CollectorID || p.DatasetID != "" || !p.ReceivedAt.IsZero() || p.ActiveRuntimeMS != nil || p.Provider.BucketEnd.After(now.Add(time.Minute)) || p.Provider.BucketEnd.Before(job.StartedAt.Truncate(24*time.Hour)) {
			return errors.New("invalid or misattributed AppDynamics point")
		}
		if !slices.Contains(job.Source.AppDynamics.Applications, p.Provider.EntityIDs["application"]) {
			return errors.New("point application outside approved scope")
		}
		p, err := analysis.NormalizeRunClock(p, job.StartedAt)
		if err != nil {
			return err
		}
		p.Service = job.Source.Service
		payload.Points[i] = p
	}
	for i, d := range payload.Evidence {
		if d.ID == "" || len(d.ID) > 256 || len(d.EntityIDs) > 16 || len(d.Properties) > 16 || d.TimestampUTC.IsZero() || !slices.Contains(job.Source.AppDynamics.Applications, d.ApplicationID) {
			return errors.New("invalid diagnostic identity")
		}
		switch d.Kind {
		case "event", "violation", "snapshot", "log", "transaction":
		default:
			return errors.New("unsupported diagnostic kind")
		}
		d.Summary = scrubTextBounded(d.Summary, 1024)
		d.Status = scrubTextBounded(d.Status, 128)
		d.Severity = scrubTextBounded(d.Severity, 64)
		// Only safe provider summaries are retained. Never accept arbitrary captured properties.
		allowed := map[string]bool{"duration_ms": true, "experience": true, "error": true, "correlation": true, "run_id": true, "query": true, "query_name": true, "application": true, "applicationname": true, "transactionname": true, "tiername": true, "nodename": true, "userexperience": true, "requestguid": true, "type": true, "incident_start_utc": true, "incident_end_utc": true}
		for k, v := range d.Properties {
			if !allowed[k] {
				delete(d.Properties, k)
			} else if k == "requestguid" || k == "run_id" {
				// Cursor validation checks these provider identities against their
				// strict format or the managed run. Preserve correlation IDs.
				d.Properties[k] = v
			} else {
				d.Properties[k] = scrubTextBounded(v, 256)
			}
		}
		for k, v := range d.EntityIDs {
			if len(k) > 128 || len(v) > 128 || strings.ContainsAny(k+v, "\r\n") {
				return errors.New("invalid diagnostic entities")
			}
		}
		if d.DeepLink != "" {
			link, err := url.Parse(d.DeepLink)
			origin, originErr := url.Parse(job.ConnectionSnapshot.Config.Endpoint)
			if err != nil || originErr != nil || link.Scheme != "https" || link.Host != origin.Host || link.User != nil || len(d.DeepLink) > 2048 {
				d.DeepLink = ""
			}
		}
		payload.Evidence[i] = d
	}
	for key, c := range payload.Coverage {
		if len(key) > 256 || c.From.IsZero() || !c.To.After(c.From) || c.To.After(now.Add(time.Minute)) || c.Points < 0 || c.Evidence < 0 {
			return errors.New("invalid provider coverage")
		}
		c.Message = scrubTextBounded(c.Message, 256)
		payload.Coverage[key] = c
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
		if job.Source.Pack == "appdynamics" && (job.State == "reconciling" || job.State == "collecting") {
			return errMonitoringPending
		}
	}
	return nil
}
