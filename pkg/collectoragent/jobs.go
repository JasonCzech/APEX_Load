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
	ID              string    `json:"id"`
	ProjectID       string    `json:"project_id"`
	RunID           string    `json:"run_id"`
	ScenarioID      string    `json:"scenario_id,omitempty"`
	CollectorID     string    `json:"collector_id"`
	SourceID        string    `json:"source_id"`
	SourceRevision  int64     `json:"source_revision"`
	Pack            string    `json:"pack"`
	Destination     string    `json:"destination,omitempty"`
	IntervalSeconds int       `json:"interval_seconds"`
	StartedAt       time.Time `json:"started_at"`
	ExpiresAt       time.Time `json:"expires_at"`
	LeaseExpiresAt  time.Time `json:"lease_expires_at"`
	NextSequence    int64     `json:"next_sequence"`
}

type SignedRunJob struct {
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}

type RunSampleBatch struct {
	Job      SignedRunJob           `json:"job"`
	Sequence int64                  `json:"sequence"`
	Points   []analysis.MetricPoint `json:"points"`
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
	if len(key) != ed25519.PublicKeySize || len(signed.Payload) > 8192 || len(signed.Signature) > 128 {
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
	for _, envelope := range signed {
		err := func() error {
			job, err := VerifyRunJob(envelope, a.cfg.ControllerPublicKey, a.cfg.CollectorID, now)
			if err != nil {
				return err
			}
			active[job.ID] = true
			if !slices.Contains(a.cfg.Packs, job.Pack) || job.Pack == "prometheus" && job.Destination != a.cfg.PrometheusURL || job.Pack != "prometheus" && job.Destination != "" {
				return errors.New("monitor job destination or pack is not locally approved")
			}
			// A persisted batch survives process restart. The durable controller
			// cursor also resolves a lost response after successful acceptance.
			batch := a.pendingBatches[job.ID]
			if batch != nil && batch.Sequence < job.NextSequence {
				delete(a.pendingBatches, job.ID)
				batch = nil
			}
			if batch == nil {
				if until := a.nextCollection[job.ID]; now.Before(until) {
					return nil
				}
				copyAgent := *a
				copyAgent.cfg.Packs = []string{job.Pack}
				leaseCtx, cancel := context.WithDeadline(ctx, job.LeaseExpiresAt)
				hb, collectErr := copyAgent.collect(leaseCtx)
				cancel()
				if collectErr != nil {
					return collectErr
				}
				points := []analysis.MetricPoint{}
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
				if len(points) == 0 {
					return errors.New("monitor job returned no samples")
				}
				batch = &RunSampleBatch{Job: envelope, Sequence: job.NextSequence, Points: points}
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
			delete(a.pendingBatches, job.ID)
			if err := a.saveSpool(); err != nil {
				return err
			}
			a.nextCollection[job.ID] = now.Add(time.Duration(job.IntervalSeconds) * time.Second)
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
	for id := range a.nextCollection {
		if !active[id] {
			delete(a.nextCollection, id)
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
		if a.cfg.PrometheusURL != "" {
			ad.Destinations = []string{a.cfg.PrometheusURL}
		}
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
	data, err := io.ReadAll(io.LimitReader(response.Body, 512<<10+1))
	if err != nil {
		return err
	}
	if len(data) > 512<<10 {
		return errors.New("monitor job response exceeds limit")
	}
	return json.Unmarshal(data, result)
}

type RunJobReadiness struct {
	Packs        []string `json:"packs"`
	Destinations []string `json:"destinations"`
	SigningKey   string   `json:"signing_key"`
}
