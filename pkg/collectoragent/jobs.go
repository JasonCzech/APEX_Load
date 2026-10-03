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
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

// RunJob grants a short collection lease for one immutable run/source binding.
// Destination is also checked against local operator configuration before I/O.
type RunJob struct {
	Management        *ManagementJob     `json:"management,omitempty"`
	UnavailableReason string             `json:"unavailable_reason,omitempty"`
	Credentials       *SealedCredentials `json:"credentials,omitempty"`
	ID                string             `json:"id"`
	ProjectID         string             `json:"project_id"`
	RunID             string             `json:"run_id"`
	ScenarioID        string             `json:"scenario_id,omitempty"`
	CollectorID       string             `json:"collector_id"`
	SourceID          string             `json:"source_id"`
	SourceRevision    int64              `json:"source_revision"`
	Pack              string             `json:"pack"`
	Destination       string             `json:"destination,omitempty"`
	IntervalSeconds   int                `json:"interval_seconds"`
	StartedAt         time.Time          `json:"started_at"`
	ExpiresAt         time.Time          `json:"expires_at"`
	LeaseExpiresAt    time.Time          `json:"lease_expires_at"`
	NextSequence      int64              `json:"next_sequence"`
}

type SignedRunJob struct {
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}

type RunSampleBatch struct {
	EvidenceVersion     uint32                                `json:"evidence_version,omitempty"`
	MessagingManagement *analysis.MessagingManagementSnapshot `json:"messaging_management,omitempty"`
	ManagementGrant     *SignedManagementPoll                 `json:"management_grant,omitempty"`
	Job                 SignedRunJob                          `json:"job"`
	Sequence            int64                                 `json:"sequence"`
	Points              []analysis.MetricPoint                `json:"points"`
}

func SignRunJob(job RunJob, key ed25519.PrivateKey) (SignedRunJob, error) {
	if len(key) != ed25519.PrivateKeySize {
		return SignedRunJob{}, errors.New("monitor signing key unavailable")
	}
	body, err := json.Marshal(job)
	if err != nil {
		return SignedRunJob{}, err
	}
	return SignedRunJob{Payload: string(body), Signature: base64.RawStdEncoding.EncodeToString(ed25519.Sign(key, body))}, nil
}

func VerifyRunJob(signed SignedRunJob, key ed25519.PublicKey, collector string, now time.Time) (RunJob, error) {
	var job RunJob
	if len(key) != ed25519.PublicKeySize || len(signed.Payload) > 32768 || len(signed.Signature) > 128 {
		return job, errors.New("invalid signed monitor job")
	}
	sig, err := base64.RawStdEncoding.DecodeString(signed.Signature)
	if err != nil || !ed25519.Verify(key, []byte(signed.Payload), sig) {
		return job, errors.New("monitor job signature rejected")
	}
	if err = json.Unmarshal([]byte(signed.Payload), &job); err != nil {
		return job, err
	}
	if job.CollectorID != collector || job.ID == "" || job.ProjectID == "" || job.RunID == "" || job.SourceID == "" || job.SourceRevision < 1 || job.NextSequence < 0 || job.IntervalSeconds < 1 || job.IntervalSeconds > 3600 || job.StartedAt.IsZero() || job.StartedAt.After(now.Add(30*time.Second)) || !job.ExpiresAt.After(now) || !job.LeaseExpiresAt.After(now) || job.LeaseExpiresAt.After(now.Add(2*time.Minute)) || job.LeaseExpiresAt.After(job.ExpiresAt) {
		return RunJob{}, errors.New("monitor job identity or collection lease rejected")
	}
	if (job.Pack == "cloudwatch" || job.Pack == "azure-monitor") && job.IntervalSeconds < 60 {
		return RunJob{}, errors.New("cloud collection interval must be at least 60 seconds")
	}
	if job.Management != nil {
		if !managementLabel(job.ID, 1024) {
			return RunJob{}, errors.New("invalid management job identity")
		}
		for _, identity := range []string{job.ProjectID, job.RunID, job.CollectorID, job.SourceID} {
			if !managementLabel(identity, 256) {
				return RunJob{}, errors.New("invalid management job identity")
			}
		}
		if job.Management.Version != 1 || !isManagementPack(job.Pack) || !managementHash(job.Management.ScopeHash) || !managementHash(job.Management.IdentityHash) || job.IntervalSeconds < 10 || job.Pack == "solace-semp" && job.IntervalSeconds < 30 {
			return RunJob{}, errors.New("invalid management job approval")
		}
	} else if isManagementPack(job.Pack) {
		return RunJob{}, errors.New("management job requires explicit versioned approval")
	}
	return job, nil
}

func (a *Agent) runJobTick(ctx context.Context) error {
	var signed []SignedRunJob
	if err := a.jobRequest(ctx, http.MethodGet, "jobs", nil, &signed); err != nil {
		return err
	}
	if len(signed) > 32 {
		return errors.New("too many assigned monitor jobs")
	}
	now := time.Now().UTC()
	active := map[string]bool{}
	var failures []error
	managementJobs := []SignedRunJob{}
	for _, envelope := range signed {
		err := func() error {
			job, err := VerifyRunJob(envelope, a.cfg.ControllerPublicKey, a.cfg.CollectorID, now)
			if err != nil {
				return err
			}
			active[job.ID] = true
			if job.Management != nil {
				managementJobs = append(managementJobs, envelope)
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
				if until := a.nextCollection[job.ID]; now.Before(until) {
					return nil
				}
				if err := a.spoolBudget.reserveFresh("legacy-active:"+job.ID, []string{job.ID}); err != nil {
					return err
				}
				defer func() {
					if a.pendingBatches[job.ID] == nil {
						a.spoolBudget.release("legacy-active:" + job.ID)
					}
				}()
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
				a.pendingBatches[job.ID] = batch
				if err := a.saveSpool(); err != nil {
					return err
				}
			}
			// Renew only the signed authorization; source observations remain intact.
			batch.Job = envelope
			if err := a.saveSpool(); err != nil {
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
	for id := range a.pendingBatches {
		if !active[id] {
			delete(a.pendingBatches, id)
		}
	}
	for id := range a.otlpAccepted {
		if !active[id] {
			delete(a.otlpAccepted, id)
		}
	}
	for id := range a.nextCollection {
		if !active[id] {
			delete(a.nextCollection, id)
		}
	}
	if a.management != nil {
		if err := a.management.update(ctx, managementJobs); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(append(failures, a.saveSpool())...)
}

func (a *Agent) jobRequest(ctx context.Context, method, path string, body, result any) error {
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
	if path == "jobs" {
		ad := RunJobReadiness{Packs: a.cfg.Packs, SigningKey: base64.StdEncoding.EncodeToString(a.cfg.ControllerPublicKey)}
		if a.management != nil {
			ad.InstanceID = a.management.instance
			ad.Capabilities = []string{FeatureMessagingManagementEvidenceV1}
			ad.ManagementScopes = a.management.readiness()
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
	if response.StatusCode >= 300 {
		return fmt.Errorf("monitor job request returned %d", response.StatusCode)
	}
	if result == nil {
		_, err = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return err
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 2<<20+1))
	if err != nil {
		return err
	}
	if len(data) > 2<<20 {
		return errors.New("monitor job response exceeds limit")
	}
	return json.Unmarshal(data, result)
}

type RunJobReadiness struct {
	InstanceID       string                     `json:"instance_id,omitempty"`
	Capabilities     []string                   `json:"capabilities,omitempty"`
	ManagementScopes []ManagementReadinessScope `json:"management_scopes,omitempty"`
	Packs            []string                   `json:"packs"`
	Destinations     []string                   `json:"destinations"`
	SigningKey       string                     `json:"signing_key"`
}

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
