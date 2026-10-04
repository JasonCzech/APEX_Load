package collectoragent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/appdynamics"
)

// RunJob grants a short collection lease for one immutable run/source binding.
// Destination is also checked against local operator configuration before I/O.
type RunJob struct {
	AppDynamics       *appdynamics.JobConfig `json:"appdynamics,omitempty"`
	QueryFromUTC      time.Time              `json:"query_from_utc,omitempty"`
	QueryUntilUTC     time.Time              `json:"query_until_utc,omitempty"`
	LoadEndedAt       *time.Time             `json:"load_ended_at,omitempty"`
	DrainDeadline     *time.Time             `json:"drain_deadline,omitempty"`
	QueryWatermark    time.Time              `json:"query_watermark,omitempty"`
	Reconciling       bool                   `json:"reconciling,omitempty"`
	UnavailableReason string                 `json:"unavailable_reason,omitempty"`
	Credentials       *SealedCredentials     `json:"credentials,omitempty"`
	ID                string                 `json:"id"`
	ProjectID         string                 `json:"project_id"`
	RunID             string                 `json:"run_id"`
	ScenarioID        string                 `json:"scenario_id,omitempty"`
	CollectorID       string                 `json:"collector_id"`
	SourceID          string                 `json:"source_id"`
	SourceRevision    int64                  `json:"source_revision"`
	Pack              string                 `json:"pack"`
	Destination       string                 `json:"destination,omitempty"`
	IntervalSeconds   int                    `json:"interval_seconds"`
	StartedAt         time.Time              `json:"started_at"`
	ExpiresAt         time.Time              `json:"expires_at"`
	LeaseExpiresAt    time.Time              `json:"lease_expires_at"`
	NextSequence      int64                  `json:"next_sequence"`
	MaxBatches        int                    `json:"max_batches,omitempty"`
}

type SignedRunJob struct {
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}

type RunSampleBatch struct {
	Topology       []appdynamics.Entity            `json:"topology,omitempty"`
	Evidence       []appdynamics.Diagnostic        `json:"evidence,omitempty"`
	Coverage       map[string]appdynamics.Coverage `json:"coverage,omitempty"`
	Capabilities   appdynamics.Capabilities        `json:"capabilities,omitempty"`
	Progress       *appdynamics.QueryProgress      `json:"progress,omitempty"`
	QueryFromUTC   time.Time                       `json:"query_from_utc,omitempty"`
	QueryUntilUTC  time.Time                       `json:"query_until_utc,omitempty"`
	QueryWindowEnd *time.Time                      `json:"query_window_end,omitempty"`
	Final          bool                            `json:"final,omitempty"`
	Job            SignedRunJob                    `json:"job"`
	Sequence       int64                           `json:"sequence"`
	Points         []analysis.MetricPoint          `json:"points"`
}

func SignRunJob(job RunJob, key ed25519.PrivateKey) (SignedRunJob, error) {
	if len(key) != ed25519.PrivateKeySize {
		return SignedRunJob{}, errors.New("monitor signing key unavailable")
	}
	body, err := json.Marshal(job)
	if err != nil {
		return SignedRunJob{}, err
	}
	if len(body) > 96<<10 || job.Pack != "appdynamics" && len(body) > 32768 {
		return SignedRunJob{}, errors.New("monitor job exceeds payload limit")
	}
	return SignedRunJob{Payload: string(body), Signature: base64.RawStdEncoding.EncodeToString(ed25519.Sign(key, body))}, nil
}

func VerifyRunJob(signed SignedRunJob, key ed25519.PublicKey, collector string, now time.Time) (RunJob, error) {
	var job RunJob
	if len(key) != ed25519.PublicKeySize || len(signed.Payload) > 96<<10 || len(signed.Signature) > 128 {
		return job, errors.New("invalid signed monitor job")
	}
	sig, err := base64.RawStdEncoding.DecodeString(signed.Signature)
	if err != nil || !ed25519.Verify(key, []byte(signed.Payload), sig) {
		return job, errors.New("monitor job signature rejected")
	}
	if err = json.Unmarshal([]byte(signed.Payload), &job); err != nil {
		return job, err
	}
	if job.Pack != "appdynamics" && len(signed.Payload) > 32768 {
		return RunJob{}, errors.New("monitor job exceeds payload limit")
	}
	if job.MaxBatches < 0 || job.MaxBatches > 32 || job.Pack != "appdynamics" && job.MaxBatches > 1 {
		return RunJob{}, errors.New("monitor grant batch budget rejected")
	}
	if job.CollectorID != collector || job.ID == "" || job.ProjectID == "" || job.RunID == "" || job.SourceID == "" || job.SourceRevision < 1 || job.NextSequence < 0 || job.IntervalSeconds < 1 || job.IntervalSeconds > 3600 || job.StartedAt.IsZero() || job.StartedAt.After(now.Add(30*time.Second)) || !job.ExpiresAt.After(now) || !job.LeaseExpiresAt.After(now) || job.LeaseExpiresAt.After(now.Add(2*time.Minute)) || job.LeaseExpiresAt.After(job.ExpiresAt) {
		return RunJob{}, errors.New("monitor job identity or collection lease rejected")
	}
	if (job.Pack == "cloudwatch" || job.Pack == "azure-monitor") && job.IntervalSeconds < 60 {
		return RunJob{}, errors.New("cloud collection interval must be at least 60 seconds")
	}
	return job, nil
}

func (a *Agent) runJobTick(ctx context.Context) error {
	var signed []SignedRunJob
	a.stateMu.Lock()
	after := a.jobPollAfterKey
	a.stateMu.Unlock()
	query := url.Values{"page_size": {"32"}}
	if after != "" {
		query.Set("after_key", after)
	}
	var headers http.Header
	if err := a.jobRequestWithHeaders(ctx, http.MethodGet, "jobs?"+query.Encode(), nil, &signed, &headers); err != nil {
		return err
	}
	if len(signed) > 32 {
		return errors.New("too many assigned monitor jobs")
	}
	next := headers.Get("X-APEX-Monitor-Next-Key")
	if headers.Get("X-APEX-Monitor-Page") != "1" {
		next = "" // An older controller still returns the original complete array.
	} else if len(next) > maxRunMonitorJobIDBytes || strings.TrimSpace(next) != next || strings.ContainsAny(next, "\r\n\x00") || next != "" && next <= after {
		return errors.New("invalid collector job continuation")
	}
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	now := time.Now().UTC()
	if after == "" || a.jobScanUnseen == nil {
		a.jobScanUnseen = map[string]bool{}
		for id := range a.pendingBatches {
			a.jobScanUnseen[id] = true
		}
		for id := range a.providerBacklog {
			a.jobScanUnseen[id] = true
		}
		for id := range a.otlpAccepted {
			a.jobScanUnseen[id] = true
		}
	}
	a.jobPollAfterKey = next
	a.discardExpiredMonitorSpool(now)
	if len(signed) > 1 {
		offset := a.jobScanOffset % len(signed)
		ordered := make([]SignedRunJob, 0, len(signed))
		ordered = append(ordered, signed[offset:]...)
		signed = append(ordered, signed[:offset]...)
	}
	var failures []error
	for _, envelope := range signed {
		err := func() error {
			job, err := VerifyRunJob(envelope, a.cfg.ControllerPublicKey, a.cfg.CollectorID, now)
			if err != nil {
				return err
			}
			delete(a.jobScanUnseen, job.ID)
			if job.Pack == "appdynamics" {
				if !slices.Contains(a.cfg.Packs, job.Pack) || job.UnavailableReason != "" {
					return errors.New("AppDynamics pack is disabled or temporarily unavailable")
				}
				a.dispatchAppDynamicsJob(ctx, job, envelope)
				return nil
			}
			if job.UnavailableReason != "" {
				return errors.New("monitor job is temporarily unavailable")
			}
			_, adapterApproved := a.adapterTarget(job)
			localApproved := (job.Pack == "go-runtime" || job.Pack == "linux-hosts") && job.Destination == ""
			prometheusApproved := job.Pack == "prometheus" && job.Destination == a.cfg.PrometheusURL
			if !slices.Contains(a.cfg.Packs, job.Pack) || !(localApproved || prometheusApproved || adapterApproved) {
				return errors.New("monitor job destination or pack is not locally approved")
			}

			// A persisted batch survives process restart. The durable controller
			// cursor also resolves a lost response after successful acceptance.
			batch := a.pendingBatches[job.ID]
			if batch != nil && batch.Sequence < job.NextSequence {
				a.acknowledgeOTLP(job, batch.Points)
				delete(a.pendingBatches, job.ID)
				batch = nil
			}
			if batch == nil {
				if len(a.pendingBatches) >= maxCollectorSpoolJobs || job.Pack == "otel-metrics" && a.otlpAccepted[job.ID] == nil && len(a.otlpAccepted) >= maxCollectorSpoolJobs {
					return errors.New("collector retry storage job budget reached")
				}
				if until := a.nextCollection[job.ID]; now.Before(until) {
					return nil
				}
				var points []analysis.MetricPoint
				if adapterApproved {
					// Cloud throttling and delayed/empty data must not turn the
					// one-second control heartbeat into one-second provider queries.
					if job.Pack == "cloudwatch" || job.Pack == "azure-monitor" {
						a.nextCollection[job.ID] = now.Add(time.Duration(job.IntervalSeconds) * time.Second)
					}
					leaseCtx, cancel := context.WithDeadline(ctx, job.LeaseExpiresAt)
					points, err = a.collectAdapter(leaseCtx, job)
					cancel()
					if err != nil {
						return err
					}
					for i := range points {
						if err := normalizeAdapterPoint(&points[i]); err != nil {
							return err
						}
						points[i].ProjectID = job.ProjectID
						points[i].RunID = job.RunID
						points[i].ScenarioID = job.ScenarioID
						points[i].Source = job.SourceID
						points[i].Host = job.CollectorID
						points[i].ReceivedAt = time.Time{}
						points[i], err = analysis.NormalizeRunClock(points[i], job.StartedAt)
						if err != nil {
							return err
						}
					}
				} else {
					copyAgent := *a
					copyAgent.cfg.Packs = []string{job.Pack}
					leaseCtx, cancel := context.WithDeadline(ctx, job.LeaseExpiresAt)
					hb, collectErr := copyAgent.collect(leaseCtx)
					cancel()
					if collectErr != nil {
						return collectErr
					}
					points = []analysis.MetricPoint{}
					for _, pack := range hb.Packs {
						keys := make([]string, 0, len(pack.Samples))
						for name := range pack.Samples {
							keys = append(keys, name)
						}
						sort.Strings(keys)
						for _, name := range keys {
							value := pack.Samples[name]
							meta := pack.SampleMetadata[name]
							kind := meta.Kind
							if kind == "" {
								kind = analysis.KindGauge
							}
							at := pack.Timestamp
							if meta.Timestamp != nil {
								at = *meta.Timestamp
							}
							p := analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: job.ProjectID, RunID: job.RunID, ScenarioID: job.ScenarioID, Source: job.SourceID, Host: job.CollectorID, TimestampUTC: at.UTC(), MetricName: name, Value: &value, Kind: kind, Quality: analysis.QualityValid}
							p.Unit, p.Dimensions = meta.Unit, meta.Dimensions
							if meta.Name != "" {
								p.MetricName = meta.Name
							}
							if job.Pack != "prometheus" {
								p.Unit = localMetricUnit(name)
							}
							if p.Unit != "" {
								unit, scale, err := analysis.NormalizeUnit(p.Unit)
								if err != nil {
									return err
								}
								p.Unit = unit
								value *= scale
							}
							if meta.Quality == analysis.QualityMissing {
								p.Quality = analysis.QualityMissing
								p.Value = nil
							}
							p, err = analysis.NormalizeRunClock(p, job.StartedAt)
							if err != nil {
								return err
							}
							points = append(points, p)
						}
					}
				}
				if len(points) == 0 {
					if job.Pack == "otel-metrics" || job.Pack == "cloudwatch" || job.Pack == "azure-monitor" {
						return nil
					}
					return errors.New("monitor job returned no samples")
				}
				if job.Pack == "otel-metrics" {
					// Metadata expansion must not strand an acknowledged OTLP
					// export behind the controller's request-size bound. The
					// receiver retains the unacknowledged remainder durably.
					points, err = boundedMonitorPoints(points)
					if err != nil {
						return err
					}
				}
				batch = &RunSampleBatch{Job: envelope, Sequence: job.NextSequence, Points: points}
				encoded, err := json.Marshal(batch)
				if err != nil || len(encoded) > 1<<20 {
					return errors.New("monitor batch exceeds byte limit")
				}
				if err := a.admitMonitorRetry(job.ID, batch, nil); err != nil {
					return err
				}
			}
			// Renew only the signed authorization; source observations remain intact.
			previousEnvelope := batch.Job
			batch.Job = envelope
			if err := a.saveSpool(); err != nil {
				batch.Job = previousEnvelope
				return err
			}
			if err := a.jobRequest(ctx, http.MethodPost, "jobs/"+url.PathEscape(job.ID)+"/samples", batch, nil); err != nil {
				return err
			}
			a.acknowledgeOTLP(job, batch.Points)
			delete(a.pendingBatches, job.ID)
			if err := a.saveSpool(); err != nil {
				return err
			}
			a.nextCollection[job.ID] = now.Add(time.Duration(job.IntervalSeconds) * time.Second)
			if job.Pack == "otel-metrics" {
				remaining, _ := a.collectAdapter(ctx, job)
				if len(remaining) > 0 {
					a.nextCollection[job.ID] = now
				}
			}
			return nil
		}()
		if err != nil {
			failures = append(failures, err)
		}
	}
	// Absence from one page is not cancellation. Prune stopped jobs only after
	// the complete scan; persisted observations require a newly signed grant
	// before any retry is submitted.
	if next == "" {
		for id := range a.jobScanUnseen {
			delete(a.pendingBatches, id)
			delete(a.providerBacklog, id)
			delete(a.otlpAccepted, id)
			delete(a.nextCollection, id)
		}
		a.jobScanUnseen = nil
		a.jobScanOffset = (a.jobScanOffset + max(1, cap(a.providerSlots))) % 32
	}
	for id := range a.nextCollection {
		if !a.nextCollection[id].After(now) {
			delete(a.nextCollection, id)
		}
	}
	for len(a.nextCollection) > maxCollectorSpoolJobs {
		var earliest string
		for id, at := range a.nextCollection {
			if earliest == "" || at.Before(a.nextCollection[earliest]) {
				earliest = id
			}
		}
		delete(a.nextCollection, earliest)
	}
	return errors.Join(append(failures, a.saveSpool())...)
}

func (a *Agent) jobRequest(ctx context.Context, method, path string, body, result any) error {
	return a.jobRequestWithHeaders(ctx, method, path, body, result, nil)
}

func (a *Agent) jobRequestWithHeaders(ctx context.Context, method, path string, body, result any, headers *http.Header) error {
	var encoded []byte
	var err error
	if body != nil {
		encoded, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, fmt.Sprintf("%s/api/v1/monitoring/collectors/%s/%s", a.cfg.ControllerURL, url.PathEscape(a.cfg.CollectorID), path), bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+a.cfg.Token)
	req.Header.Set("Content-Type", "application/json")
	if path == "jobs" || strings.HasPrefix(path, "jobs?") {
		ad := RunJobReadiness{Packs: a.cfg.Packs, SigningKey: base64.StdEncoding.EncodeToString(a.cfg.ControllerPublicKey)}
		if slices.Contains(a.cfg.Packs, "appdynamics") {
			ad.Contracts = []string{AppDynamicsBatchContract}
		}
		ad.Destinations = a.approvedDestinations()
		payload, _ := json.Marshal(ad)
		req.Header.Set("X-APEX-Monitor-Readiness", string(payload))
	}
	client := *a.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if headers != nil {
		*headers = response.Header.Clone()
	}
	if response.StatusCode >= 300 {
		return fmt.Errorf("monitor job request returned %d", response.StatusCode)
	}
	if result == nil {
		_, err = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return err
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 4<<20+1))
	if err != nil {
		return err
	}
	if len(data) > 4<<20 {
		return errors.New("monitor job response exceeds limit")
	}
	return json.Unmarshal(data, result)
}

type RunJobReadiness struct {
	Packs        []string `json:"packs"`
	Contracts    []string `json:"contracts,omitempty"`
	Destinations []string `json:"destinations"`
	SigningKey   string   `json:"signing_key"`
}

// AppDynamicsBatchContract identifies signed query progress, diagnostic and
// topology batches; the pack name alone does not attest support for them.
const AppDynamicsBatchContract = "appdynamics.v1"

func normalizeAdapterPoint(point *analysis.MetricPoint) error {
	switch point.Unit {
	case "By":
		point.Unit = "bytes"
	case "1":
		point.Unit = "ratio"
	case "{count}":
		point.Unit = "count"
	}
	if point.Unit == "" {
		return nil
	}
	unit, scale, err := analysis.NormalizeUnit(point.Unit)
	if err != nil {
		return err
	}
	point.Unit = unit
	if point.Value != nil {
		value := *point.Value * scale
		point.Value = &value
	}
	return nil
}

// Reserve enough envelope space for the largest renewed signed lease. A retry
// can carry a larger credential envelope without changing source observations.
func boundedMonitorPoints(points []analysis.MetricPoint) ([]analysis.MetricPoint, error) {
	const limit = (1 << 20) - (96 << 10)
	low, high := 0, len(points)
	for low < high {
		middle := (low + high + 1) / 2
		body, err := json.Marshal(points[:middle])
		if err != nil {
			return nil, err
		}
		if len(body) <= limit {
			low = middle
		} else {
			high = middle - 1
		}
	}
	if low == 0 {
		return nil, errors.New("monitor point exceeds batch byte limit")
	}
	return points[:low], nil
}
